package hardwareio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// failing returns a hook that fails while *enabled is true and counts calls.
func failing(enabled *atomic.Bool, calls *atomic.Int64, message string) func() error {
	return func() error {
		calls.Add(1)
		if enabled.Load() {
			return errors.New(message)
		}
		return nil
	}
}

// requireRows checks that the stream holds exactly want, in order and
// without duplicates.
func requireRows(t *testing.T, store *Store, key StreamKey, want []int64) {
	t.Helper()
	records, latest, err := store.After(key, 0, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != len(want) {
		t.Fatalf("stream has %d records, want %d", len(records), len(want))
	}
	for index, record := range records {
		if record.Sequence != want[index] {
			t.Fatalf("record %d has sequence %d, want %d", index, record.Sequence, want[index])
		}
	}
	if len(want) > 0 && latest != want[len(want)-1] {
		t.Fatalf("latest = %d, want %d", latest, want[len(want)-1])
	}
}

func dirEntries(t *testing.T, store *Store, key StreamKey) (closed, active, temp, plain []string) {
	t.Helper()
	dir, _ := store.streamDir(key)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, tempSuffix):
			temp = append(temp, name)
		case strings.HasSuffix(name, closedSuffix):
			closed = append(closed, name)
		case strings.HasSuffix(name, activeSuffix):
			active = append(active, name)
		default:
			plain = append(plain, name)
		}
	}
	return
}

func reopenStore(t *testing.T, root string, clock *testClock, maxSegment int64) *Store {
	t.Helper()
	store := newTestStore(t, root, clock, 0, maxSegment)
	if _, err := store.LastSequence(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestCompressFailureKeepsAppendingWithBackoff(t *testing.T) {
	for _, stage := range []string{"compress", "rename"} {
		t.Run(stage, func(t *testing.T) {
			clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
			root := t.TempDir()
			store := newTestStore(t, root, clock, 0, 300)
			var enabled atomic.Bool
			var calls atomic.Int64
			enabled.Store(true)
			hook := failing(&enabled, &calls, stage+" failed")
			if stage == "compress" {
				store.faults.compress = hook
			} else {
				store.faults.rename = hook
			}
			all := appendN(t, store, keyA, clock, 30, "x")
			if calls.Load() != 1 {
				t.Fatalf("%s attempted %d times within the back-off, want 1", stage, calls.Load())
			}
			requireRows(t, store, keyA, all)
			closed, active, temp, _ := dirEntries(t, store, keyA)
			if len(closed) != 0 || len(active) != 1 || len(temp) != 0 {
				t.Fatalf("after failed rotation: closed=%v active=%v temp=%v", closed, active, temp)
			}
			stats, _ := store.Stats()
			if stats.Failures.ByOperation["compress"] != 1 || !strings.Contains(stats.Failures.LastError, stage+" failed") {
				t.Fatalf("failure not visible in stats: %+v", stats.Failures)
			}

			// Back-off doubles: retried after 1 s, then not again within 2 s.
			clock.Add(1500 * time.Millisecond)
			all = append(all, appendN(t, store, keyA, clock, 1, "retry1")...)
			clock.Add(time.Second)
			all = append(all, appendN(t, store, keyA, clock, 1, "within")...)
			if calls.Load() != 2 {
				t.Fatalf("attempts = %d, want 2 (one retry after the first back-off)", calls.Load())
			}

			enabled.Store(false)
			clock.Add(10 * time.Second)
			all = append(all, appendN(t, store, keyA, clock, 3, "healed")...)
			requireRows(t, store, keyA, all)
			closed, _, _, _ = dirEntries(t, store, keyA)
			if len(closed) != 1 {
				t.Fatalf("rotation did not succeed once the fault cleared: %v", closed)
			}
			_ = store.Close()
			requireRows(t, reopenStore(t, root, clock, 300), keyA, all)
		})
	}
}

func TestRemoveFailureAfterInstallRegistersClosedSegment(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 300)
	var enabled atomic.Bool
	var calls atomic.Int64
	enabled.Store(true)
	store.faults.removeActive = failing(&enabled, &calls, "sharing violation")
	all := appendN(t, store, keyA, clock, 6, "x")
	closed, active, _, _ := dirEntries(t, store, keyA)
	if len(closed) < 1 || len(active) < 2 {
		t.Fatalf("want the installed gz, the stale old active and a new active: closed=%v active=%v", closed, active)
	}
	item := store.streams[keyA]
	if len(item.closed) < 1 || item.active == nil || len(item.stale) < 1 {
		t.Fatalf("stream state: closed=%d active=%v stale=%v", len(item.closed), item.active != nil, item.stale)
	}
	requireRows(t, store, keyA, all)
	stats, _ := store.Stats()
	if stats.StaleFiles < 1 || stats.Failures.ByOperation["remove"] < 1 {
		t.Fatalf("stale file not visible: %+v", stats)
	}
	all = append(all, appendN(t, store, keyA, clock, 2, "more")...)

	// Restart while the stale file is still there: the gz already holds
	// its records, so it is dropped and nothing is counted twice.
	_ = store.Close()
	reopened := reopenStore(t, root, clock, 300)
	requireRows(t, reopened, keyA, all)
	if _, err := reopened.Prune(Policy{}); err != nil {
		t.Fatal(err)
	}
	stats, _ = reopened.Stats()
	if stats.StaleFiles != 0 {
		t.Fatalf("stale files after restart = %d", stats.StaleFiles)
	}
}

func TestStaleFileRemovalIsRetriedByMaintenance(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	store := newTestStore(t, t.TempDir(), clock, 0, 300)
	var enabled atomic.Bool
	var calls atomic.Int64
	enabled.Store(true)
	store.faults.removeActive = failing(&enabled, &calls, "sharing violation")
	all := appendN(t, store, keyA, clock, 4, "x")
	enabled.Store(false)
	if _, err := store.Prune(Policy{}); err != nil {
		t.Fatal(err)
	}
	stats, _ := store.Stats()
	if stats.StaleFiles != 0 {
		t.Fatalf("stale files after maintenance = %d", stats.StaleFiles)
	}
	_, active, _, _ := dirEntries(t, store, keyA)
	if len(active) != 1 {
		t.Fatalf("stale active file still on disk: %v", active)
	}
	requireRows(t, store, keyA, all)
}

func TestCloseFailureAfterInstallDoesNotWedgeTheStream(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 300)
	var enabled atomic.Bool
	var calls atomic.Int64
	enabled.Store(true)
	store.faults.closeActive = failing(&enabled, &calls, "close failed")
	all := appendN(t, store, keyA, clock, 12, "x")
	if calls.Load() < 2 {
		t.Fatalf("want several rotations despite close failures, got %d", calls.Load())
	}
	requireRows(t, store, keyA, all)
	stats, _ := store.Stats()
	if stats.Failures.ByOperation["close"] < 2 {
		t.Fatalf("close failures not visible: %+v", stats.Failures)
	}
	_ = store.Close()
	requireRows(t, reopenStore(t, root, clock, 300), keyA, all)
}

func TestSyncFailureReopensHandleAndKeepsAppending(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 300)
	var enabled atomic.Bool
	var calls atomic.Int64
	enabled.Store(true)
	store.faults.sync = failing(&enabled, &calls, "sync failed")
	all := appendN(t, store, keyA, clock, 10, "x")
	requireRows(t, store, keyA, all)
	enabled.Store(false)
	clock.Add(time.Minute)
	all = append(all, appendN(t, store, keyA, clock, 2, "healed")...)
	closed, _, _, _ := dirEntries(t, store, keyA)
	if len(closed) != 1 {
		t.Fatalf("rotation after sync recovered: %v", closed)
	}
	_ = store.Close()
	requireRows(t, reopenStore(t, root, clock, 300), keyA, all)
}

// writeGzipLines installs a gz segment holding the first count lines of an
// active file, as an interrupted older rotation would have left it.
func writeGzipLines(t *testing.T, dir string, lines [][]byte, count int, first, last int64) string {
	t.Helper()
	path := filepath.Join(dir, closedSegmentName(first, last, 1, int64(count)))
	if _, err := writeGzipAtomic(path, func(writer io.Writer) error {
		for _, line := range lines[:count] {
			if _, err := writer.Write(append(append([]byte(nil), line...), '\n')); err != nil {
				return err
			}
		}
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPrefersNewerActiveAndDeduplicatesGzipSegments(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	all := appendN(t, store, keyA, clock, 8, "x")
	activePath := store.streams[keyA].active.path
	dir, _ := store.streamDir(keyA)
	_ = store.Close()
	raw, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	// Two older gz copies of the same first: one with 5 records, one with 3.
	five := writeGzipLines(t, dir, lines, 5, all[0], all[4])
	three := writeGzipLines(t, dir, lines, 3, all[0], all[2])

	reopened := reopenStore(t, root, clock, 0)
	requireRows(t, reopened, keyA, all)
	for _, path := range []string{five, three} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("outdated gz %s kept: %v", filepath.Base(path), err)
		}
	}
	next := appendN(t, reopened, keyA, clock, 1, "after")
	requireRows(t, reopened, keyA, append(all, next...))
}

func TestLoadDeduplicatesGzipSegmentsWithoutActive(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	all := appendN(t, store, keyA, clock, 6, "x")
	item := store.streams[keyA]
	activeCopy, _ := os.ReadFile(item.active.path)
	item.mu.Lock()
	if err := store.rotateLocked(item); err != nil {
		t.Fatal(err)
	}
	item.mu.Unlock()
	dir, _ := store.streamDir(keyA)
	_ = store.Close()
	lines := bytes.Split(bytes.TrimRight(activeCopy, "\n"), []byte("\n"))
	shorter := writeGzipLines(t, dir, lines, 4, all[0], all[3])
	reopened := reopenStore(t, root, clock, 0)
	requireRows(t, reopened, keyA, all)
	if _, err := os.Stat(shorter); !os.IsNotExist(err) {
		t.Fatalf("shorter duplicate gz kept: %v", err)
	}
}

func TestLoadCompressFailureKeepsPlainSegmentReadable(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	all := appendN(t, store, keyA, clock, 5, "day1")
	_ = store.Close()
	clock.Add(24 * time.Hour)

	var enabled atomic.Bool
	var calls atomic.Int64
	enabled.Store(true)
	reopened := newTestStore(t, root, clock, 0, 0)
	reopened.faults.compress = failing(&enabled, &calls, "compress failed")
	if _, err := reopened.LastSequence(); err != nil {
		t.Fatalf("a failed load-time compression must not fail the store: %v", err)
	}
	requireRows(t, reopened, keyA, all)
	stats, _ := reopened.Stats()
	if stats.PlainSegments != 1 || stats.Failures.ByOperation["compress"] != 1 {
		t.Fatalf("plain segment not reported: %+v", stats)
	}
	all = append(all, appendN(t, reopened, keyA, clock, 2, "day2")...)
	requireRows(t, reopened, keyA, all)

	enabled.Store(false)
	result, err := reopened.Prune(Policy{})
	if err != nil || result.RecompressedPlain != 1 {
		t.Fatalf("prune = %+v %v, want the plain segment compressed", result, err)
	}
	requireRows(t, reopened, keyA, all)
	_ = reopened.Close()
	requireRows(t, reopenStore(t, root, clock, 0), keyA, all)
}

// TestRestartAfterEveryFailureModeKeepsExactRows runs a long write with
// faults switching on and off at every rotation step and checks the exact
// row set after a restart.
func TestRestartAfterEveryFailureModeKeepsExactRows(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 400)
	var calls atomic.Int64
	toggles := []*atomic.Bool{{}, {}, {}, {}, {}}
	store.faults.sync = failing(toggles[0], &calls, "sync")
	store.faults.compress = failing(toggles[1], &calls, "compress")
	store.faults.rename = failing(toggles[2], &calls, "rename")
	store.faults.closeActive = failing(toggles[3], &calls, "close")
	store.faults.removeActive = failing(toggles[4], &calls, "remove")
	var all []int64
	for round := 0; round < 40; round++ {
		for index, toggle := range toggles {
			toggle.Store((round+index)%3 == 0)
		}
		all = append(all, appendN(t, store, keyA, clock, 4, fmt.Sprintf("r%d", round))...)
		clock.Add(7 * time.Second)
		if round == 20 {
			clock.Add(24 * time.Hour)
		}
	}
	requireRows(t, store, keyA, all)
	_ = store.Close()
	reopened := reopenStore(t, root, clock, 400)
	requireRows(t, reopened, keyA, all)
	if _, err := reopened.Prune(Policy{}); err != nil {
		t.Fatal(err)
	}
	requireRows(t, reopened, keyA, all)
	stats, _ := reopened.Stats()
	if stats.StaleFiles != 0 || stats.PlainSegments != 0 {
		t.Fatalf("leftovers after restart and maintenance: %+v", stats)
	}
}

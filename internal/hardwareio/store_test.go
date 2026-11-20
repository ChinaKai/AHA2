package hardwareio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestStore(t *testing.T, root string, clock *testClock, seed int64, maxSegment int64) *Store {
	t.Helper()
	store := Open(Options{
		Root:            root,
		SeedSequence:    func() (int64, error) { return seed, nil },
		MaxSegmentBytes: maxSegment,
		Now:             clock.Now,
	})
	t.Cleanup(func() { _ = store.Close() })
	return store
}

var keyA = StreamKey{TaskID: "task_a", HardwareID: "board", Transport: "serial"}
var keyB = StreamKey{TaskID: "task_b", HardwareID: "board", Transport: "network"}

func appendN(t *testing.T, store *Store, key StreamKey, clock *testClock, count int, prefix string) []int64 {
	t.Helper()
	sequences := make([]int64, 0, count)
	for index := 0; index < count; index++ {
		sequence, err := store.Append(Record{
			ID: fmt.Sprintf("%s-%d", prefix, index), TaskID: key.TaskID, HardwareID: key.HardwareID,
			Transport: key.Transport, Direction: "rx", Data: fmt.Sprintf("%s line %d\r\n", prefix, index),
			Encoding: "text", Source: "runtime", CreatedAt: clock.Now().UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
		sequences = append(sequences, sequence)
	}
	return sequences
}

func TestAppendContinuesAfterLegacySequenceAndReadsBack(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	store := newTestStore(t, t.TempDir(), clock, 12_000_000, 0)
	first := appendN(t, store, keyA, clock, 3, "a")
	second := appendN(t, store, keyB, clock, 2, "b")
	if first[0] != 12_000_001 || first[2] != 12_000_003 || second[0] != 12_000_004 {
		t.Fatalf("sequences = %v %v, want continuation after the legacy maximum", first, second)
	}
	records, latest, err := store.After(keyA, 12_000_001, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Sequence != 12_000_002 || latest != 12_000_003 {
		t.Fatalf("after = %+v latest=%d", records, latest)
	}
	if records[0].TaskID != "task_a" || records[0].Transport != "serial" || records[0].Data != "a line 1\r\n" || records[0].Source != "runtime" {
		t.Fatalf("record fields not preserved: %+v", records[0])
	}
	records, latest, err = store.Latest(keyB, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Sequence != 12_000_005 || latest != 12_000_005 {
		t.Fatalf("latest = %+v latest=%d", records, latest)
	}
	empty, latest, err := store.After(StreamKey{TaskID: "task_none", HardwareID: "x", Transport: "serial"}, 0, 10)
	if err != nil || len(empty) != 0 || latest != 0 {
		t.Fatalf("unknown stream = %v %d %v", empty, latest, err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "v1", "task_none")); !os.IsNotExist(err) {
		t.Fatalf("reading an unknown stream must not create it: %v", err)
	}
}

func TestRotationBySizeAndDayCompressesAndReadsAcrossSegments(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 300)
	all := appendN(t, store, keyA, clock, 10, "day1")
	clock.Add(24 * time.Hour)
	all = append(all, appendN(t, store, keyA, clock, 5, "day2")...)
	dir, _ := store.streamDir(keyA)
	entries, _ := os.ReadDir(dir)
	closed, active := 0, 0
	for _, entry := range entries {
		switch {
		case strings.HasSuffix(entry.Name(), closedSuffix):
			closed++
		case strings.HasSuffix(entry.Name(), activeSuffix):
			active++
		}
	}
	if closed < 3 || active != 1 {
		t.Fatalf("closed=%d active=%d entries=%v, want several gz segments and one active", closed, active, entries)
	}
	records, _, err := store.After(keyA, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != len(all) {
		t.Fatalf("after(0) returned %d records, want %d", len(records), len(all))
	}
	for index, record := range records {
		if record.Sequence != all[index] {
			t.Fatalf("record %d sequence %d, want %d", index, record.Sequence, all[index])
		}
	}
	middle, _, err := store.After(keyA, all[6], 3)
	if err != nil || len(middle) != 3 || middle[0].Sequence != all[7] {
		t.Fatalf("after middle = %+v %v", middle, err)
	}
	latest, maximum, err := store.Latest(keyA, 7)
	if err != nil || len(latest) != 7 || latest[0].Sequence != all[8] || latest[6].Sequence != all[14] || maximum != all[14] {
		t.Fatalf("latest across segments = %+v max=%d %v", latest, maximum, err)
	}

	// A reopened store sees the same data and never reuses a sequence.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := newTestStore(t, root, clock, 0, 300)
	next := appendN(t, reopened, keyA, clock, 1, "after-reopen")
	if next[0] <= all[len(all)-1] {
		t.Fatalf("sequence after reopen = %d, want > %d", next[0], all[len(all)-1])
	}
	records, _, err = reopened.After(keyA, 0, 100)
	if err != nil || len(records) != len(all)+1 {
		t.Fatalf("reopened read = %d records %v", len(records), err)
	}
}

func TestReopenClosesActiveSegmentFromEarlierDay(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	appendN(t, store, keyA, clock, 2, "late")
	_ = store.Close()
	clock.Add(2 * time.Hour)
	reopened := newTestStore(t, root, clock, 0, 0)
	if _, err := reopened.LastSequence(); err != nil {
		t.Fatal(err)
	}
	dir, _ := reopened.streamDir(keyA)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), closedSuffix) {
		t.Fatalf("entries after reopen on the next day = %v, want one compressed segment", entries)
	}
}

func TestSequencesAreNeverReusedAfterEverythingIsDeleted(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 500, 0)
	used := appendN(t, store, keyA, clock, 3, "x")
	if _, err := store.RemoveTask(keyA.TaskID); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	// The legacy seed is gone (table emptied, sqlite_sequence lost) and no
	// segment is left; only the reservation file remembers the floor.
	reopened := newTestStore(t, root, clock, 0, 0)
	next := appendN(t, reopened, keyA, clock, 1, "y")
	if next[0] <= used[2] {
		t.Fatalf("sequence %d reused a value <= %d", next[0], used[2])
	}
}

func TestSeedFloorIsPersistedBeforeFirstAppend(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 9_999, 0)
	if floor, err := store.LastSequence(); err != nil || floor != 9_999 {
		t.Fatalf("floor = %d %v", floor, err)
	}
	_ = store.Close()
	reopened := newTestStore(t, root, clock, 0, 0)
	next := appendN(t, reopened, keyA, clock, 1, "z")
	if next[0] != 10_000 {
		t.Fatalf("first sequence after a lost seed = %d, want 10000", next[0])
	}
}

func TestCrashLeftoversAreRepaired(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	sequences := appendN(t, store, keyA, clock, 3, "ok")
	dir, _ := store.streamDir(keyA)
	activePath := store.streams[keyA].active.path
	_ = store.Close()
	// A torn last line and a half-written temp segment from a crash.
	file, err := os.OpenFile(activePath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(`{"s":99,"id":"torn","d":"rx","t":"half`)
	_ = file.Close()
	tempPath := filepath.Join(dir, "seg-00000000000000000001-00000000000000000002-1-2.jsonl.gz.tmp")
	if err := os.WriteFile(tempPath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened := newTestStore(t, root, clock, 0, 0)
	records, latest, err := reopened.After(keyA, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || latest != sequences[2] {
		t.Fatalf("records after repair = %d latest=%d", len(records), latest)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp segment not removed: %v", err)
	}
	next := appendN(t, reopened, keyA, clock, 1, "after")
	records, _, err = reopened.After(keyA, 0, 10)
	if err != nil || len(records) != 4 || records[3].Sequence != next[0] {
		t.Fatalf("append after repair = %+v %v", records, err)
	}
}

func TestActiveFileWithCompressedCopyIsDropped(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	appendN(t, store, keyA, clock, 2, "dup")
	item := store.streams[keyA]
	item.mu.Lock()
	activePath := item.active.path
	activeCopy, _ := os.ReadFile(activePath)
	if err := store.rotateLocked(item); err != nil {
		t.Fatal(err)
	}
	item.mu.Unlock()
	_ = store.Close()
	// Crash between installing the gz file and removing the active file.
	if err := os.WriteFile(activePath, activeCopy, 0o600); err != nil {
		t.Fatal(err)
	}
	reopened := newTestStore(t, root, clock, 0, 0)
	records, _, err := reopened.After(keyA, 0, 10)
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %d %v, want 2 without duplicates", len(records), err)
	}
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Fatalf("duplicate active file kept: %v", err)
	}
}

func TestInvalidUTF8DataRoundTrips(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	store := newTestStore(t, t.TempDir(), clock, 0, 0)
	raw := string([]byte{0xff, 0xfe, 'o', 'k', 0xd4, 0xb4})
	if _, err := store.Append(Record{ID: "bin", TaskID: keyA.TaskID, HardwareID: keyA.HardwareID, Transport: keyA.Transport, Direction: "rx", Data: raw}); err != nil {
		t.Fatal(err)
	}
	records, _, err := store.Latest(keyA, 1)
	if err != nil || len(records) != 1 || records[0].Data != raw {
		t.Fatalf("data = %+v %v, want the original bytes", records, err)
	}
	if records[0].CreatedAt == "" {
		t.Fatal("created_at must default to the store clock")
	}
}

func TestPruneRetentionCapacityAndOrphans(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	keyC := StreamKey{TaskID: "task_deleted", HardwareID: "board", Transport: "serial"}
	for day := 0; day < 10; day++ {
		appendN(t, store, keyA, clock, 20, fmt.Sprintf("a%d", day))
		appendN(t, store, keyB, clock, 20, fmt.Sprintf("b%d", day))
		clock.Add(24 * time.Hour)
	}
	appendN(t, store, keyC, clock, 5, "c")
	// Day 10: the last appends rotated the day 9 segments; stale actives
	// from earlier days are closed by Prune.
	result, err := store.Prune(Policy{
		RetentionDays: 5,
		KeepTask:      func(taskID string) (bool, error) { return taskID != keyC.TaskID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OrphanTasks != 1 || result.RetentionSegments == 0 || result.RotatedSegments == 0 {
		t.Fatalf("prune result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "v1", keyC.TaskID)); !os.IsNotExist(err) {
		t.Fatalf("orphan task directory kept: %v", err)
	}
	records, _, err := store.After(keyA, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := clock.Now().Add(-5 * 24 * time.Hour)
	for _, record := range records {
		created, _ := time.Parse(time.RFC3339Nano, record.CreatedAt)
		if created.Before(cutoff.Add(-24 * time.Hour)) {
			t.Fatalf("record from %s survived 5-day retention", record.CreatedAt)
		}
	}
	if len(records) == 0 || len(records) >= 200 {
		t.Fatalf("records after retention = %d", len(records))
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	// Cap at roughly half: the oldest segments of either stream go first and
	// writes keep working.
	result, err = store.Prune(Policy{MaxBytes: stats.Bytes / 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.CapacitySegments == 0 || result.TotalBytes > stats.Bytes/2 || result.OverLimit {
		t.Fatalf("capacity prune = %+v (before %d bytes)", result, stats.Bytes)
	}
	after, _ := store.Stats()
	if after.OldestAt == nil || stats.OldestAt == nil || !after.OldestAt.After(*stats.OldestAt) {
		t.Fatalf("oldest data must be removed first: before %v after %v", stats.OldestAt, after.OldestAt)
	}
	if _, err := store.Append(Record{ID: "still-writable", TaskID: keyA.TaskID, HardwareID: keyA.HardwareID, Transport: keyA.Transport, Direction: "tx", Data: "ls\n"}); err != nil {
		t.Fatalf("append after capacity prune: %v", err)
	}

	// A cap below the active segments cannot be met; it is reported, not
	// enforced by refusing writes.
	result, err = store.Prune(Policy{MaxBytes: 1})
	if err != nil || !result.OverLimit {
		t.Fatalf("tiny cap = %+v %v", result, err)
	}
	if _, err := store.Append(Record{ID: "over-limit", TaskID: keyA.TaskID, HardwareID: keyA.HardwareID, Transport: keyA.Transport, Direction: "rx", Data: "x"}); err != nil {
		t.Fatalf("append while over the cap: %v", err)
	}
}

func TestPruneRemovesEmptyStreamDirectories(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	appendN(t, store, keyA, clock, 3, "old")
	clock.Add(40 * 24 * time.Hour)
	if _, err := store.Prune(Policy{RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "v1", keyA.TaskID)); !os.IsNotExist(err) {
		t.Fatalf("empty stream directories kept: %v", err)
	}
	next := appendN(t, store, keyA, clock, 1, "new")
	records, latest, err := store.After(keyA, 0, 10)
	if err != nil || len(records) != 1 || latest != next[0] {
		t.Fatalf("stream after directory removal = %+v %d %v", records, latest, err)
	}
}

func TestPathNamesAreEncodedSafely(t *testing.T) {
	for _, value := range []string{"task_0123abcd", "CON", "con", "nul", "Board-1", "../escape", "a/b", `c:\x`, "中文", strings.Repeat("a", 200)} {
		encoded := encodePathName(value)
		if strings.ContainsAny(encoded, `/\:.`) || encoded == "" {
			t.Fatalf("encoded %q = %q is not a safe single path element", value, encoded)
		}
		decoded, ok := decodePathName(encoded)
		if !ok || decoded != value {
			t.Fatalf("round trip %q -> %q -> %q", value, encoded, decoded)
		}
	}
	if encodePathName("con") == "con" || encodePathName("Task") == "Task" {
		t.Fatal("device names and uppercase must be encoded")
	}
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	odd := StreamKey{TaskID: "../../evil", HardwareID: "CON", Transport: "serial"}
	appendN(t, store, odd, clock, 1, "odd")
	keys, err := store.listStreamKeys()
	if err != nil || len(keys) != 1 || keys[0] != odd {
		t.Fatalf("stream keys = %+v %v", keys, err)
	}
	escaped, _ := filepath.Glob(filepath.Join(filepath.Dir(root), "evil*"))
	if len(escaped) != 0 {
		t.Fatalf("path escaped the root: %v", escaped)
	}
}

func TestConcurrentAppendsAndReads(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	store := newTestStore(t, t.TempDir(), clock, 0, 2048)
	keys := []StreamKey{keyA, keyB, {TaskID: "task_c", HardwareID: "board", Transport: "serial"}}
	var wait sync.WaitGroup
	for _, key := range keys {
		for worker := 0; worker < 3; worker++ {
			wait.Add(1)
			go func(key StreamKey, worker int) {
				defer wait.Done()
				for index := 0; index < 100; index++ {
					if _, err := store.Append(Record{ID: fmt.Sprintf("%d-%d", worker, index), TaskID: key.TaskID, HardwareID: key.HardwareID, Transport: key.Transport, Direction: "rx", Data: "data\n"}); err != nil {
						t.Error(err)
						return
					}
				}
			}(key, worker)
		}
		wait.Add(1)
		go func(key StreamKey) {
			defer wait.Done()
			for index := 0; index < 50; index++ {
				if _, _, err := store.Latest(key, 50); err != nil {
					t.Error(err)
					return
				}
			}
		}(key)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for index := 0; index < 20; index++ {
			if _, err := store.Prune(Policy{RetentionDays: 30, MaxBytes: 1 << 30}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wait.Wait()
	seen := make(map[int64]bool)
	for _, key := range keys {
		records, _, err := store.After(key, 0, 2000)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 300 {
			t.Fatalf("stream %v has %d records, want 300", key, len(records))
		}
		for index, record := range records {
			if seen[record.Sequence] {
				t.Fatalf("sequence %d used twice", record.Sequence)
			}
			seen[record.Sequence] = true
			if index > 0 && record.Sequence <= records[index-1].Sequence {
				t.Fatalf("stream %v not in sequence order at %d", key, index)
			}
		}
	}
}

// TestReadsMatchModelAcrossIndexMarksSegmentsAndReopen checks After and
// Latest against a plain slice for every cursor, with enough records in the
// active segment to use several sparse index marks.
func TestReadsMatchModelAcrossIndexMarksSegmentsAndReopen(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	check := func(store *Store, all []int64) {
		t.Helper()
		for _, limit := range []int{1, 7, markEvery, markEvery + 1, 500, 2000} {
			for cursor := int64(0); cursor <= all[len(all)-1]+1; cursor += 37 {
				records, latest, err := store.After(keyA, cursor, limit)
				if err != nil {
					t.Fatal(err)
				}
				var want []int64
				for _, sequence := range all {
					if sequence > cursor && len(want) < limit {
						want = append(want, sequence)
					}
				}
				if len(records) != len(want) || latest != all[len(all)-1] {
					t.Fatalf("after(%d,%d) = %d records latest %d, want %d", cursor, limit, len(records), latest, len(want))
				}
				for index := range want {
					if records[index].Sequence != want[index] {
						t.Fatalf("after(%d,%d)[%d] = %d, want %d", cursor, limit, index, records[index].Sequence, want[index])
					}
				}
			}
			records, _, err := store.Latest(keyA, limit)
			if err != nil {
				t.Fatal(err)
			}
			want := all[max(0, len(all)-limit):]
			if len(records) != len(want) || records[0].Sequence != want[0] || records[len(records)-1].Sequence != want[len(want)-1] {
				t.Fatalf("latest(%d) = %d records, want %d", limit, len(records), len(want))
			}
		}
	}
	store := newTestStore(t, root, clock, 100, 64<<10)
	var all []int64
	all = append(all, appendN(t, store, keyA, clock, 900, "one")...)
	// Interleave another stream so sequences have gaps.
	for index := 0; index < 3; index++ {
		appendN(t, store, keyB, clock, 5, "gap")
		all = append(all, appendN(t, store, keyA, clock, 50, "two")...)
	}
	if len(store.streams[keyA].closed) == 0 || len(store.streams[keyA].active.marks) < 2 {
		t.Fatalf("fixture must span closed segments and several marks: closed=%d marks=%d", len(store.streams[keyA].closed), len(store.streams[keyA].active.marks))
	}
	check(store, all)
	_ = store.Close()
	reopened := newTestStore(t, root, clock, 0, 64<<10)
	check(reopened, all)
}

func BenchmarkCaughtUpPoll(b *testing.B) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	store := Open(Options{Root: b.TempDir(), Now: clock.Now})
	defer store.Close()
	var last int64
	for index := 0; index < 20_000; index++ {
		last, _ = store.Append(Record{ID: "x", TaskID: keyA.TaskID, HardwareID: keyA.HardwareID, Transport: keyA.Transport, Direction: "rx", Data: strings.Repeat("y", 150)})
	}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, _, err := store.After(keyA, last-10, 500); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDamagedLinesAreSkippedNotFatal(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := newTestStore(t, root, clock, 0, 0)
	appendN(t, store, keyA, clock, 2, "before")
	activePath := store.streams[keyA].active.path
	_ = store.Close()
	file, err := os.OpenFile(activePath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("not json at all\n")
	_ = file.Close()
	reopened := newTestStore(t, root, clock, 0, 0)
	appendN(t, reopened, keyA, clock, 2, "after")
	records, _, err := reopened.After(keyA, 0, 10)
	if err != nil || len(records) != 4 {
		t.Fatalf("records = %d %v, want the 4 good records around the damaged line", len(records), err)
	}
	stats, _ := reopened.Stats()
	if stats.CorruptLines == 0 {
		t.Fatal("damaged line must be counted")
	}
}

func TestTotalBytesTracksDiskAndSignalsCapacity(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	root := t.TempDir()
	store := Open(Options{Root: root, Now: clock.Now, MaxSegmentBytes: 1024, MaxBytes: 2000})
	defer store.Close()
	consistent := func(step string) {
		t.Helper()
		stats, err := store.Stats()
		if err != nil {
			t.Fatal(err)
		}
		if store.TotalBytes() != stats.Bytes {
			t.Fatalf("%s: tracked %d bytes, segments hold %d", step, store.TotalBytes(), stats.Bytes)
		}
	}
	for day := 0; day < 3; day++ {
		appendN(t, store, keyA, clock, 40, fmt.Sprintf("a%d", day))
		appendN(t, store, keyB, clock, 10, fmt.Sprintf("b%d", day))
		clock.Add(24 * time.Hour)
	}
	consistent("after appends and rotations")
	select {
	case <-store.CapacitySignal():
	default:
		t.Fatalf("no capacity signal at %d bytes over a 2000 byte cap", store.TotalBytes())
	}
	if _, err := store.Prune(Policy{RetentionDays: 2}); err != nil {
		t.Fatal(err)
	}
	consistent("after retention")
	if _, err := store.Prune(Policy{MaxBytes: 1500}); err != nil {
		t.Fatal(err)
	}
	consistent("after capacity prune")
	if _, err := store.RemoveTask(keyB.TaskID); err != nil {
		t.Fatal(err)
	}
	consistent("after task removal")
	_ = store.Close()
	reopened := Open(Options{Root: root, Now: clock.Now, MaxSegmentBytes: 1024})
	defer reopened.Close()
	if _, err := reopened.LastSequence(); err != nil {
		t.Fatal(err)
	}
	stats, _ := reopened.Stats()
	if reopened.TotalBytes() != stats.Bytes {
		t.Fatalf("after reopen: tracked %d, segments hold %d", reopened.TotalBytes(), stats.Bytes)
	}
}

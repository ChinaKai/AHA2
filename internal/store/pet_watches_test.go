package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func openPetWatchTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database, ctx
}

func TestRestorePetTaskWatchKeepsRealFollow(t *testing.T) {
	database, ctx := openPetWatchTestStore(t)
	now := time.Now().UTC()
	const taskID, ignoreOnlyReason = "task-followed-ignore", "ignored"

	if err := database.UpsertPetTaskWatch(ctx, taskID, "manual", now); err != nil {
		t.Fatal(err)
	}
	if err := database.IgnorePetTaskWatch(ctx, taskID, ignoreOnlyReason, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	watch, err := database.PetTaskWatch(ctx, taskID)
	if err != nil || !watch.Ignored || watch.Reason != "manual" {
		t.Fatalf("followed ignored watch=%#v err=%v", watch, err)
	}

	removed, found, err := database.RestorePetTaskWatch(ctx, taskID, ignoreOnlyReason, now.Add(2*time.Second))
	if err != nil || removed || !found {
		t.Fatalf("restore removed=%v found=%v err=%v", removed, found, err)
	}
	watch, err = database.PetTaskWatch(ctx, taskID)
	if err != nil || watch.Ignored || watch.Reason != "manual" {
		t.Fatalf("restored follow=%#v err=%v", watch, err)
	}
}

func TestIgnoredRowCannotBeOverwrittenByFollowAndRestoresToUnfollowed(t *testing.T) {
	database, ctx := openPetWatchTestStore(t)
	now := time.Now().UTC()
	const taskID, ignoreOnlyReason = "task-ignore-only", "ignored"

	if err := database.IgnorePetTaskWatch(ctx, taskID, ignoreOnlyReason, now); err != nil {
		t.Fatal(err)
	}
	watch, err := database.PetTaskWatch(ctx, taskID)
	if err != nil || !watch.Ignored || watch.Reason != ignoreOnlyReason {
		t.Fatalf("ignore-only watch=%#v err=%v", watch, err)
	}

	// A manual or automatic follow that lands after the ignore must not rewrite
	// the ignore-only row into a follow.
	if err := database.UpsertPetTaskWatch(ctx, taskID, "auto", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	watch, err = database.PetTaskWatch(ctx, taskID)
	if err != nil || !watch.Ignored || watch.Reason != ignoreOnlyReason {
		t.Fatalf("follow overwrote ignore row=%#v err=%v", watch, err)
	}

	removed, found, err := database.RestorePetTaskWatch(ctx, taskID, ignoreOnlyReason, now.Add(2*time.Second))
	if err != nil || !removed || !found {
		t.Fatalf("restore removed=%v found=%v err=%v", removed, found, err)
	}
	if _, err := database.PetTaskWatch(ctx, taskID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ignore-only restore left a watch row: %v", err)
	}
}

func TestConcurrentIgnoreAndFollowNeverCorruptsWatchState(t *testing.T) {
	database, ctx := openPetWatchTestStore(t)
	now := time.Now().UTC()
	const ignoreOnlyReason = "ignored"

	const taskCount = 24
	taskIDs := make([]string, taskCount)
	for index := range taskIDs {
		taskIDs[index] = "task-race-" + strconv.Itoa(index)
	}

	var wait sync.WaitGroup
	for _, taskID := range taskIDs {
		taskID := taskID
		wait.Add(2)
		go func() {
			defer wait.Done()
			if err := database.IgnorePetTaskWatch(ctx, taskID, ignoreOnlyReason, now); err != nil {
				t.Errorf("ignore %s: %v", taskID, err)
			}
		}()
		go func() {
			defer wait.Done()
			if err := database.UpsertPetTaskWatch(ctx, taskID, "auto", now); err != nil {
				t.Errorf("follow %s: %v", taskID, err)
			}
		}()
	}
	wait.Wait()

	for _, taskID := range taskIDs {
		watch, err := database.PetTaskWatch(ctx, taskID)
		if err != nil || !watch.Ignored {
			t.Fatalf("watch after race=%#v err=%v", watch, err)
		}
		removed, found, err := database.RestorePetTaskWatch(ctx, taskID, ignoreOnlyReason, now.Add(time.Second))
		if err != nil || !found {
			t.Fatalf("restore %s removed=%v found=%v err=%v", taskID, removed, found, err)
		}
		watch, err = database.PetTaskWatch(ctx, taskID)
		if errors.Is(err, sql.ErrNoRows) {
			if !removed {
				t.Fatalf("restore %s deleted an ignore-only row without reporting removal", taskID)
			}
			continue
		}
		if err != nil {
			t.Fatalf("watch after restore %s: %v", taskID, err)
		}
		// A surviving row must be a real follow that was un-ignored, never an
		// ignore-only row left in a half-restored state.
		if removed || watch.Ignored || watch.Reason == ignoreOnlyReason {
			t.Fatalf("corrupt watch after restore %s: %#v removed=%v", taskID, watch, removed)
		}
	}
}

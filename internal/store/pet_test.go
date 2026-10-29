package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetStateIsSingletonAndVersioned(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	first, err := database.EnsurePetState(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "owner_pet" || first.Action != "idle" || first.Version != 1 {
		t.Fatalf("first=%#v", first)
	}
	first.Action = "jump"
	first.Mood = "happy"
	updated, err := database.SavePetState(ctx, first, first.Version)
	if err != nil || updated.Version != 2 || updated.Action != "jump" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err := database.SavePetState(ctx, domain.PetState{ID: "owner_pet", Action: "walk", UpdatedAt: now}, 1); err != ErrPetStateConflict {
		t.Fatalf("stale write err=%v", err)
	}
}

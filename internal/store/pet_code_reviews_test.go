package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestPetCodeReviewLinksMigrationAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { database.Close() }()
	sourceID := attachmentTestTask(t, database)
	source, err := database.Task(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	review := source
	review.ID, review.Code = "task-review", ""
	if err := database.CreateTask(ctx, review); err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-link v125 schema; old Tasks must not be backfilled by
	// title, request or any other user-controlled text.
	if _, err := database.db.ExecContext(ctx, `DROP TABLE pet_code_reviews; DELETE FROM schema_migrations WHERE version=126`); err != nil {
		t.Fatal(err)
	}
	if err := database.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PetCodeReviewSource(ctx, review.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy task acquired link: %v", err)
	}
	if ids, err := database.PetCodeReviewsForSource(ctx, source.ID); err != nil || len(ids) != 0 {
		t.Fatalf("unlinked source acquired reviews: %v %v", ids, err)
	}
	if err := database.CreatePetCodeReviewLink(ctx, review.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	second := review
	second.ID = "task-review-second"
	if err := database.CreateTask(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := database.CreatePetCodeReviewLink(ctx, second.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := database.PetCodeReviewSource(ctx, review.ID); err != nil || got != source.ID {
		t.Fatalf("link did not survive reopen/migration: %q %v", got, err)
	}
	if ids, err := database.PetCodeReviewsForSource(ctx, source.ID); err != nil || len(ids) != 2 || ids[0] != review.ID || ids[1] != second.ID {
		t.Fatalf("source query lost reviews after reopen: %v %v", ids, err)
	}
	if ids, err := database.PetCodeReviewsForSource(ctx, review.ID); err != nil || len(ids) != 0 {
		t.Fatalf("source query leaked unrelated reviews: %v %v", ids, err)
	}
	if err := database.CreatePetCodeReviewLink(ctx, review.ID, source.ID); err == nil {
		t.Fatal("existing immutable link overwritten")
	}
	// Even a corrupt cross-project row cannot grant the completion exemption.
	other := domain.Project{ID: "other-project", Name: "Other", ProjectType: "folder", CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt}
	if err := database.CreateProject(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET project_id=? WHERE id=?`, other.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PetCodeReviewSource(ctx, review.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project link trusted: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE tasks SET project_id=? WHERE id=?`, review.ProjectID, source.ID); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{source.ID, source.ID}, {source.ID, "missing"}, {"missing", source.ID}} {
		if err := database.CreatePetCodeReviewLink(ctx, pair[0], pair[1]); err == nil {
			t.Fatalf("invalid link accepted: %v", pair)
		}
	}
	if err := database.DeleteTask(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PetCodeReviewSource(ctx, review.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted source left trusted link: %v", err)
	}
	var count int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pet_code_reviews`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("link did not cascade: count=%d err=%v", count, err)
	}
}

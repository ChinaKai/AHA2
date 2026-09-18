package httpapi

import (
	"testing"
	"time"
)

// TestListCursorDecodesBothTimestampKeys pins that the cursor stays readable
// across the field rename.
//
// The cursor's timestamp is now written as "at" because it holds whatever column
// the list sorts by, which is no longer always the update time. Cursors are opaque
// and a client may already be holding one minted under the old "updated_at" key;
// rejecting those would surface as a spurious invalid_cursor on a page the user
// simply had open.
func TestListCursorDecodesBothTimestampKeys(t *testing.T) {
	t.Parallel()
	at := "2026-01-01T00:00:00Z"
	for _, cursor := range []listCursor{
		{Kind: "projects", At: at, ID: "p1"},        // current shape
		{Kind: "projects", UpdatedAt: at, ID: "p1"}, // pre-rename shape
	} {
		encoded := encodeListCursor(cursor)
		decoded, err := decodeListCursor(encoded, "projects")
		if err != nil {
			t.Fatalf("decode(%+v): %v", cursor, err)
		}
		if decoded.position() != at {
			t.Fatalf("position = %q, want %q", decoded.position(), at)
		}
	}
	// A cursor carrying neither timestamp is still rejected.
	if _, err := decodeListCursor(encodeListCursor(listCursor{Kind: "projects", ID: "p1"}), "projects"); err == nil {
		t.Fatal("a cursor with no timestamp was accepted")
	}
}

// TestStoreListPageCursorFollowsTheSortKey pins that the emitted cursor is built
// from the same timestamp the list is sorted by.
//
// The cursor is only correct if it matches the ORDER BY column: comparing it to a
// different column skips or repeats rows at page boundaries, which shows up as
// missing projects rather than as an error.
func TestStoreListPageCursorFollowsTheSortKey(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)

	page := storeListPage(
		[]struct {
			CreatedAt time.Time
			UpdatedAt time.Time
			ID        string
		}{{CreatedAt: created, UpdatedAt: updated, ID: "p1"}},
		true, "projects",
		func(item struct {
			CreatedAt time.Time
			UpdatedAt time.Time
			ID        string
		}) (time.Time, string) {
			return item.CreatedAt, item.ID
		},
	)
	cursor, err := decodeListCursor(page.NextCursor, "projects")
	if err != nil {
		t.Fatal(err)
	}
	if cursor.position() != created.Format(time.RFC3339Nano) {
		t.Fatalf("cursor = %q, want the creation time %q", cursor.position(), created.Format(time.RFC3339Nano))
	}
	if cursor.position() == updated.Format(time.RFC3339Nano) {
		t.Fatal("cursor used the update time while the list sorts by creation time")
	}
}

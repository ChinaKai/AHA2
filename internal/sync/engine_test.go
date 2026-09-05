package sync

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type fakeRemote struct {
	pull   PullResponse
	pushed PushRequest
}

func (f *fakeRemote) Push(_ context.Context, r PushRequest) (PushResponse, error) {
	f.pushed = r
	keys := make([]string, len(r.Objects))
	for i, o := range r.Objects {
		keys[i] = o.IdempotencyKey
	}
	return PushResponse{AckedKeys: keys}, nil
}
func (f *fakeRemote) Pull(context.Context, string, string, string, int) (PullResponse, error) {
	return f.pull, nil
}

func TestEnginePushPullAndIdempotency(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	if err := database.EnqueueSync(ctx, domain.SyncOutboxItem{ID: "out", Scope: "default", Object: domain.SyncObject{Type: "note", ID: "local", Operation: "upsert", Payload: json.RawMessage(`{}`), IdempotencyKey: "local-key"}}); err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{pull: PullResponse{Cursor: "cursor-2", Objects: []domain.SyncObject{{Type: "note", ID: "remote", Operation: "upsert", Payload: json.RawMessage(`{"x":1}`), IdempotencyKey: "remote-key", RemoteVersion: "2"}}}}
	engine := Engine{Store: database, Remote: remote, Scope: "default", DeviceID: "device", Now: func() time.Time { return now }}
	calls := 0
	engine.Register("note", func(context.Context, domain.SyncObject) error { calls++; return nil })
	if err := engine.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if len(remote.pushed.Objects) != 1 {
		t.Fatalf("pushed=%d", len(remote.pushed.Objects))
	}
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("handler called %d times", calls)
	}
	state, err := database.SyncState(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if state.Cursor != "cursor-2" {
		t.Fatalf("cursor=%q", state.Cursor)
	}
}

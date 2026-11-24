package workspaceterminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type testProcess struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func newTestProcess() *testProcess {
	reader, writer := io.Pipe()
	return &testProcess{reader: reader, writer: writer}
}

func (p *testProcess) Read(data []byte) (int, error)  { return p.reader.Read(data) }
func (p *testProcess) Write(data []byte) (int, error) { return len(data), nil }
func (p *testProcess) Resize(int, int) error          { return nil }
func (p *testProcess) Close() error {
	_ = p.writer.Close()
	return p.reader.Close()
}

func TestWorkspaceTerminalSessionManager(t *testing.T) {
	manager := New()
	t.Cleanup(manager.Close)
	epoch := time.Now()
	var elapsed atomic.Int64
	manager.now = func() time.Time { return epoch.Add(time.Duration(elapsed.Load())) }
	advance := func(delta time.Duration) { elapsed.Add(int64(delta)) }
	scope := Scope{OwnerID: "owner", LoginID: "login", WorkspaceID: "one"}
	expires := epoch.Add(5 * time.Hour)
	start := func(context.Context) (Process, error) { return newTestProcess(), nil }
	first := newTestProcess()
	info, err := manager.Create(t.Context(), scope, expires, true, func(context.Context) (Process, error) { return first, nil })
	if err != nil || len(info.ID) != 48 || info.Number != 1 {
		t.Fatalf("first session: %+v %v", info, err)
	}
	if _, err := manager.Create(t.Context(), scope, expires, true, start); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate initial session: %v", err)
	}
	for number := 2; number <= Limit; number++ {
		info, err := manager.Create(t.Context(), scope, expires, false, start)
		if err != nil || info.Number != number {
			t.Fatalf("create %d: %+v %v", number, info, err)
		}
	}
	otherCheckout := scope
	otherCheckout.Target[0] = 1
	if _, err := manager.Create(t.Context(), otherCheckout, expires, false, start); !errors.Is(err, ErrLimit) {
		t.Fatalf("checkout should share workspace quota: %v", err)
	}
	otherWorkspace := scope
	otherWorkspace.WorkspaceID = "two"
	if _, err := manager.Create(t.Context(), otherWorkspace, expires, false, start); err != nil {
		t.Fatalf("independent workspace quota: %v", err)
	}
	otherLogin := scope
	otherLogin.LoginID = "second-login"
	for _, unrelated := range []Scope{otherWorkspace, otherCheckout, otherLogin} {
		if _, err := manager.Attach(unrelated, info.ID); !errors.Is(err, ErrMissing) {
			t.Fatalf("unrelated view attached: %v", err)
		}
	}
	link, err := manager.Attach(scope, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	manager.Detach(link)
	advance(reconnectWindow - time.Second)

	transcript := bytes.Repeat([]byte("0123456789λxyz"), (outputCapacity+8192)/15)
	go func() { _, _ = first.writer.Write(transcript) }()
	link, err = manager.Attach(scope, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Detach(link)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// Wait for output to reach the ring before examining the replay window.
	for {
		_, end, notify, _ := link.Session.Output(uint64(len(transcript)))
		if end == uint64(len(transcript)) {
			break
		}
		select {
		case <-notify:
		case <-ctx.Done():
			t.Fatal("timed out reading terminal output")
		}
	}
	var got []byte
	for cursor := uint64(0); cursor < uint64(len(transcript)); {
		data, next, _, _ := link.Session.Output(cursor)
		got = append(got, data...)
		cursor = next
	}
	if len(got) != outputCapacity || !bytes.Equal(got, transcript[len(transcript)-outputCapacity:]) {
		t.Fatal("reconnection did not replay the most recent 256 KiB")
	}

	advance(time.Second)
	manager.reap()
	if len(manager.List(scope)) != 1 || len(manager.List(otherWorkspace)) != 0 {
		t.Fatal("the disconnected sessions should expire at the ten-minute boundary")
	}
	advance(idleTimeout - time.Second)
	manager.reap()
	select {
	case <-link.Session.Done():
	default:
		t.Fatal("an attached, idle session should expire")
	}
	if _, err := first.writer.Write([]byte("closed")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("expired terminal process is still open")
	}
}

func TestWorkspaceTerminalLogoutCancelsStartingSession(t *testing.T) {
	manager := New()
	t.Cleanup(manager.Close)
	scope := Scope{OwnerID: "owner", LoginID: "login", WorkspaceID: "work"}
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := manager.Create(t.Context(), scope, time.Now().Add(time.Hour), false, func(ctx context.Context) (Process, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		result <- err
	}()
	<-started
	manager.CloseOwner(scope.OwnerID)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || len(manager.List(scope)) != 0 {
			t.Fatalf("logout did not cancel shell startup: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup was not canceled")
	}
}

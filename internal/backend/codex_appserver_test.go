package backend

import (
	"context"
	"testing"
	"time"
)

func TestCodexAppServerRequestMatchesResponse(t *testing.T) {
	input := make(chan string, 1)
	messages := make(chan codexRPCMessage, 4)
	session := &codexAppServerSession{
		input: input, messages: messages, ctx: context.Background(),
		runDone: make(chan struct{}),
	}
	go func() {
		<-input
		messages <- codexRPCMessage{ID: int64Ptr(1), Result: []byte(`{"ok":true}`)}
	}()
	result, err := session.requestWithTimeout("initialize", map[string]any{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"ok":true}` {
		t.Fatalf("result = %s", result)
	}
}

// app-server reads newline-delimited JSON-RPC. The runner writes Command.Input
// values verbatim, so a missing newline leaves the server waiting for the rest
// of the message (measured: initialize timed out until the framing was fixed).
func TestCodexAppServerFramesRequestsWithNewline(t *testing.T) {
	input := make(chan string, 1)
	session := &codexAppServerSession{
		input: input, ctx: context.Background(), runDone: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = session.send("initialize", map[string]any{})
	}()
	select {
	case line := <-input:
		if len(line) == 0 || line[len(line)-1] != '\n' {
			t.Fatalf("request is not newline framed: %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("request was not written")
	}
	<-done
}

func int64Ptr(value int64) *int64 { return &value }

package backend

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestCodexAppServerResumeExcludesHistoryKeepsConfiguration(t *testing.T) {
	for _, resume := range []bool{false, true} {
		name := "start"
		if resume {
			name = "resume"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			input := make(chan string)
			messages := make(chan codexRPCMessage, 8)
			captured := make(chan map[string]any, 1)
			request := Request{WorkDir: "/synthetic-workspace", Model: "synthetic-model", Environment: map[string]string{"AHA_PROVIDER_ID": "synthetic-provider"}}
			if resume {
				request.ProviderSessionID = "original-thread"
			}
			session := &codexAppServerSession{request: request, input: input, messages: messages, runDone: make(chan struct{}), ctx: ctx, emit: func(Event) {}}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					select {
					case <-ctx.Done():
						return
					case line := <-input:
						var msg struct {
							ID     int64          `json:"id"`
							Method string         `json:"method"`
							Params map[string]any `json:"params"`
						}
						_ = json.Unmarshal([]byte(line), &msg)
						switch msg.Method {
						case "initialize":
							messages <- codexRPCMessage{ID: int64Ptr(msg.ID), Result: json.RawMessage(`{}`)}
						case "thread/start", "thread/resume":
							captured <- msg.Params
							messages <- codexRPCMessage{ID: int64Ptr(msg.ID), Result: json.RawMessage(`{"thread":{"id":"original-thread"}}`)}
						case "turn/start":
							messages <- codexRPCMessage{ID: int64Ptr(msg.ID), Result: json.RawMessage(`{"turn":{"id":"turn-1"}}`)}
							messages <- codexRPCMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"original-thread","turn":{"id":"turn-1","status":"completed"}}`)}
							return
						}
					}
				}
			}()
			if err := session.run(); err != nil {
				t.Fatal(err)
			}
			<-done
			want := map[string]any{"cwd": request.WorkDir, "model": request.Model, "modelProvider": codexProviderID(request), "approvalPolicy": "never", "sandbox": codexSandbox(request.Filesystem)}
			if resume {
				want["threadId"] = "original-thread"
				want["excludeTurns"] = true
			}
			if got := <-captured; !reflect.DeepEqual(got, want) {
				t.Fatalf("thread params=%v, want %v", got, want)
			}
			if session.threadID != "original-thread" {
				t.Fatalf("thread changed: %s", session.threadID)
			}
		})
	}
}

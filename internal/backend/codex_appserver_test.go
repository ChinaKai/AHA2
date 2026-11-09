package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

func newCodexNotificationSession(turnID string) (*codexAppServerSession, *[]Event) {
	events := []Event{}
	session := &codexAppServerSession{
		ctx: context.Background(), runDone: make(chan struct{}), turnID: turnID,
		emit: func(event Event) { events = append(events, event) },
	}
	return session, &events
}

func handleCodexNotification(session *codexAppServerSession, method, params string) error {
	return session.handle(codexRPCMessage{Method: method, Params: json.RawMessage(params)})
}

// codex 0.166.0 app-server carries the cause in a nested TurnError:
// {"error":{"message","codexErrorInfo","additionalDetails"},"willRetry"}. The
// old parser only read a top-level message, so this payload always fell back
// to the fixed generic text.
func TestCodexAppServerErrorNotificationKeepsNestedTurnError(t *testing.T) {
	t.Parallel()
	session, events := newCodexNotificationSession("turn-1")
	params := `{"threadId":"t","turnId":"turn-1","willRetry":true,"error":{"message":"exceeded retry limit, last status: 401 Unauthorized","codexErrorInfo":{"responseStreamDisconnected":{"httpStatusCode":401}},"additionalDetails":"request id req-123"}}`
	if err := handleCodexNotification(session, "error", params); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 1 || (*events)[0].Type != "agent_error" {
		t.Fatalf("error notification was not projected: %#v", *events)
	}
	message := fmt.Sprint((*events)[0].Data["message"])
	for _, want := range []string{"exceeded retry limit", "responseStreamDisconnected", "httpStatusCode", "401", "req-123", "will retry"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error message lost %q: %s", want, message)
		}
	}
	if (*events)[0].Data["will_retry"] != true {
		t.Fatalf("willRetry flag was dropped: %#v", (*events)[0].Data)
	}
	if session.providerError == "" {
		t.Fatal("provider error must be retained for the final Turn error")
	}
}

func TestCodexAppServerErrorNotificationKeepsLegacyTopLevelMessage(t *testing.T) {
	t.Parallel()
	session, events := newCodexNotificationSession("turn-1")
	if err := handleCodexNotification(session, "error", `{"message":"upstream 500: model overloaded"}`); err != nil {
		t.Fatal(err)
	}
	message := fmt.Sprint((*events)[0].Data["message"])
	if !strings.Contains(message, "upstream 500: model overloaded") {
		t.Fatalf("legacy message was lost: %s", message)
	}
}

func TestCodexAppServerErrorNotificationFallsBackToRawParams(t *testing.T) {
	t.Parallel()
	session, events := newCodexNotificationSession("turn-1")
	if err := handleCodexNotification(session, "error", `{"code":-32000,"data":{"detail":"transport exploded"}}`); err != nil {
		t.Fatal(err)
	}
	message := fmt.Sprint((*events)[0].Data["message"])
	if !strings.Contains(message, "-32000") || !strings.Contains(message, "transport exploded") {
		t.Fatalf("raw params were dropped: %s", message)
	}
}

func TestCodexAppServerErrorNotificationUsesGenericTextOnlyWhenEmpty(t *testing.T) {
	t.Parallel()
	session, events := newCodexNotificationSession("turn-1")
	if err := handleCodexNotification(session, "error", `{}`); err != nil {
		t.Fatal(err)
	}
	if message := fmt.Sprint((*events)[0].Data["message"]); message != codexAppServerGenericError {
		t.Fatalf("expected generic text only for an empty payload, got %q", message)
	}
}

func TestCodexAppServerTurnFailedKeepsTurnErrorDetails(t *testing.T) {
	t.Parallel()
	session, _ := newCodexNotificationSession("turn-1")
	params := `{"threadId":"t","turn":{"id":"turn-1","status":"failed","error":{"message":"model overloaded","codexErrorInfo":"serverOverloaded","additionalDetails":"trace 42"}}}`
	err := handleCodexNotification(session, "turn/completed", params)
	if err == nil {
		t.Fatal("failed turn must return an error")
	}
	for _, want := range []string{"model overloaded", "serverOverloaded", "trace 42"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("turn error lost %q: %v", want, err)
		}
	}
}

func TestCodexAppServerErrorRedactsCredentialsAndClips(t *testing.T) {
	t.Parallel()
	secret := "sk-ant-super-secret-token-123456"
	long := strings.Repeat("x", 6000)
	params, err := json.Marshal(map[string]any{"error": map[string]any{
		"message": "request failed: Authorization: Bearer " + secret + " api_key=another-secret-value " + long,
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, events := newCodexNotificationSession("turn-1")
	if err := handleCodexNotification(session, "error", string(params)); err != nil {
		t.Fatal(err)
	}
	message := fmt.Sprint((*events)[0].Data["message"])
	if strings.Contains(message, secret) || strings.Contains(message, "another-secret-value") {
		t.Fatalf("credential leaked into the error message: %s", message)
	}
	if !strings.Contains(message, "[redacted]") {
		t.Fatalf("redaction marker missing: %s", message)
	}
	if length := len([]rune(message)); length > providerDetailLimit+1 {
		t.Fatalf("error message is not bounded: %d", length)
	}
}

func TestCodexAppServerRequestErrorKeepsCodeAndData(t *testing.T) {
	t.Parallel()
	input := make(chan string, 1)
	messages := make(chan codexRPCMessage, 1)
	session := &codexAppServerSession{
		input: input, messages: messages, ctx: context.Background(), runDone: make(chan struct{}),
	}
	go func() {
		<-input
		messages <- codexRPCMessage{ID: int64Ptr(1), Error: &codexRPCError{
			Code: -32602, Message: "invalid params", Data: json.RawMessage(`{"field":"input"}`),
		}}
	}()
	_, err := session.requestWithTimeout("turn/start", map[string]any{}, time.Second)
	if err == nil {
		t.Fatal("JSON-RPC error response must fail the request")
	}
	for _, want := range []string{"invalid params", "-32602", "field"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("JSON-RPC error lost %q: %v", want, err)
		}
	}
}

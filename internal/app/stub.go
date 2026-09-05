package app

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type StubExecutor struct{}

func (StubExecutor) Execute(ctx context.Context, request ExecutionRequest, emit func(ExecutionEvent)) (ExecutionResult, error) {
	phases := []string{"prompt_ready", "backend_started", "agent_working"}
	for _, phase := range phases {
		select {
		case <-ctx.Done():
			return ExecutionResult{}, ctx.Err()
		case <-time.After(25 * time.Millisecond):
			emit(ExecutionEvent{Type: "agent_progress", Data: map[string]any{"phase": phase}})
		}
	}
	session := request.ProviderSessionID
	if session == "" {
		session = "stub-" + request.Task.ID
	}
	message := strings.TrimSpace(request.Prompt)
	if len(message) > 120 {
		message = message[len(message)-120:]
	}
	return ExecutionResult{
		Reply:             fmt.Sprintf("Stub Backend 已完成 Turn %d。\n\n当前 Prompt 尾部：%s", request.Turn.Sequence, message),
		ProviderSessionID: session,
	}, nil
}

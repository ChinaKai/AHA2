package execution

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

type Executor struct {
	Codex  backend.Codex
	Claude backend.Claude
}

func (executor Executor) Execute(ctx context.Context, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	if request.Snapshot.Backend == "claude" {
		return executor.runClaude(ctx, request, emit)
	}
	return executor.runCodex(ctx, request, emit)
}

func (executor Executor) runCodex(ctx context.Context, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	result, err := executor.Codex.Execute(ctx, backend.Request{
		Runner:  workspace.RunnerFor(request.Workspace),
		WorkDir: taskWorkDir(request),
		Model:   request.Model.WireModel, ContextWindow: request.Model.ContextWindow,
		ReasoningEffort: request.Snapshot.ReasoningEffort, Environment: request.Environment,
		Prompt: request.Prompt, ProviderSessionID: request.ProviderSessionID,
		Filesystem: request.Filesystem, Approval: request.Approval,
	}, func(event backend.Event) {
		emit(app.ExecutionEvent{Type: event.Type, Data: event.Data})
	})
	reply, patch, candidates := parseCheckpoint(result.Reply)
	return app.ExecutionResult{
		Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: result.ProviderSessionID,
		MemoryPatch: patch, KnowledgeCandidates: candidates,
	}, err
}

func (executor Executor) runClaude(ctx context.Context, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	result, err := executor.Claude.Execute(ctx, backend.Request{
		Runner:  workspace.RunnerFor(request.Workspace),
		WorkDir: taskWorkDir(request),
		Model:   request.Model.WireModel, ContextWindow: request.Model.ContextWindow,
		ReasoningEffort: request.Snapshot.ReasoningEffort, Environment: request.Environment,
		Prompt: request.Prompt, ProviderSessionID: request.ProviderSessionID,
		Filesystem: request.Filesystem, Approval: request.Approval,
	}, func(event backend.Event) {
		emit(app.ExecutionEvent{Type: event.Type, Data: event.Data})
	})
	reply, patch, candidates := parseCheckpoint(result.Reply)
	return app.ExecutionResult{
		Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: result.ProviderSessionID,
		MemoryPatch: patch, KnowledgeCandidates: candidates,
	}, err
}

func taskWorkDir(request app.ExecutionRequest) string {
	if request.Task.TaskWorkspacePath != "" {
		return request.Task.TaskWorkspacePath
	}
	return request.Workspace.RootPath
}

type checkpointPayload struct {
	Decisions    []string `json:"decisions"`
	Facts        []string `json:"facts"`
	Excluded     []string `json:"excluded"`
	Progress     []string `json:"progress"`
	Verification []string `json:"verification"`
	NextActions  []string `json:"next_actions"`
	Knowledge    []struct {
		Scope      string  `json:"scope"`
		Type       string  `json:"type"`
		Title      string  `json:"title"`
		Body       string  `json:"body"`
		Confidence float64 `json:"confidence"`
	} `json:"knowledge_candidates"`
}

func parseCheckpoint(reply string) (string, app.MemoryPatch, []app.KnowledgeCandidate) {
	const startMarker = "<aha2_checkpoint>"
	const endMarker = "</aha2_checkpoint>"
	start := strings.LastIndex(reply, startMarker)
	end := strings.LastIndex(reply, endMarker)
	if start < 0 || end <= start {
		return strings.TrimSpace(reply), app.MemoryPatch{}, nil
	}
	var payload checkpointPayload
	raw := strings.TrimSpace(reply[start+len(startMarker) : end])
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return strings.TrimSpace(reply), app.MemoryPatch{}, nil
	}
	visible := strings.TrimSpace(reply[:start] + reply[end+len(endMarker):])
	patch := app.MemoryPatch{
		Decisions: payload.Decisions, Facts: payload.Facts, Excluded: payload.Excluded,
		Progress: payload.Progress, Verification: payload.Verification, NextActions: payload.NextActions,
	}
	candidates := make([]app.KnowledgeCandidate, 0, len(payload.Knowledge))
	for _, item := range payload.Knowledge {
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Body) == "" {
			continue
		}
		candidates = append(candidates, app.KnowledgeCandidate{
			Scope: item.Scope, Type: item.Type, Title: item.Title, Body: item.Body, Confidence: item.Confidence,
		})
	}
	return visible, patch, candidates
}

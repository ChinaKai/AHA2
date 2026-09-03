package execution

import (
	"context"
	"encoding/json"
	"strconv"
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
	reply, patch, candidates, actions, mainFollowup := parseCheckpoint(result.Reply)
	return app.ExecutionResult{
		Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: result.ProviderSessionID,
		MainFollowup: mainFollowup, MemoryPatch: patch, KnowledgeCandidates: candidates, AgentActions: actions,
	}, err
}

func (executor Executor) runClaude(ctx context.Context, request app.ExecutionRequest, emit func(app.ExecutionEvent)) (app.ExecutionResult, error) {
	environment := make(map[string]string, len(request.Environment)+1)
	for key, value := range request.Environment {
		environment[key] = value
	}
	if request.Model.ContextWindow > 0 && environment["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] == "" {
		environment["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.FormatInt(request.Model.ContextWindow, 10)
	}
	result, err := executor.Claude.Execute(ctx, backend.Request{
		Runner:  workspace.RunnerFor(request.Workspace),
		WorkDir: taskWorkDir(request),
		Model:   request.Model.WireModel, ContextWindow: request.Model.ContextWindow,
		ReasoningEffort: request.Snapshot.ReasoningEffort, Environment: environment,
		Prompt: request.Prompt, ProviderSessionID: request.ProviderSessionID,
		Filesystem: request.Filesystem, Approval: request.Approval,
	}, func(event backend.Event) {
		emit(app.ExecutionEvent{Type: event.Type, Data: event.Data})
	})
	reply, patch, candidates, actions, mainFollowup := parseCheckpoint(result.Reply)
	return app.ExecutionResult{
		Reply: reply, ExitCode: result.ExitCode, ProviderSessionID: result.ProviderSessionID,
		MainFollowup: mainFollowup, MemoryPatch: patch, KnowledgeCandidates: candidates, AgentActions: actions,
	}, err
}

func taskWorkDir(request app.ExecutionRequest) string {
	if request.Task.TaskWorkspacePath != "" {
		return request.Task.TaskWorkspacePath
	}
	return request.Workspace.RootPath
}

type checkpointPayload struct {
	Decisions    json.RawMessage `json:"decisions"`
	Facts        json.RawMessage `json:"facts"`
	Excluded     json.RawMessage `json:"excluded"`
	Progress     json.RawMessage `json:"progress"`
	Verification json.RawMessage `json:"verification"`
	NextActions  json.RawMessage `json:"next_actions"`
	Knowledge    json.RawMessage `json:"knowledge_candidates"`
	MainFollowup string          `json:"main_followup"`
	AgentActions json.RawMessage `json:"agent_actions"`
}

type checkpointKnowledge struct {
	Scope      string  `json:"scope"`
	Type       string  `json:"type"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	Confidence float64 `json:"confidence"`
}

type checkpointAgentAction struct {
	AgentID         string `json:"agent_id"`
	Title           string `json:"title"`
	Assignment      string `json:"assignment"`
	Required        *bool  `json:"required"`
	Backend         string `json:"backend"`
	ModelID         string `json:"model_id"`
	ReasoningEffort string `json:"reasoning_effort"`
	Filesystem      string `json:"filesystem"`
	Approval        string `json:"approval"`
}

func parseCheckpoint(reply string) (string, app.MemoryPatch, []app.KnowledgeCandidate, []app.AgentAction, string) {
	const startMarker = "<aha2_checkpoint>"
	const endMarker = "</aha2_checkpoint>"
	start := strings.LastIndex(reply, startMarker)
	end := strings.LastIndex(reply, endMarker)
	if start < 0 || end <= start {
		return strings.TrimSpace(reply), app.MemoryPatch{}, nil, nil, ""
	}
	var payload checkpointPayload
	raw := strings.TrimSpace(reply[start+len(startMarker) : end])
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return strings.TrimSpace(reply), app.MemoryPatch{}, nil, nil, ""
	}
	visible := strings.TrimSpace(reply[:start] + reply[end+len(endMarker):])
	patch := app.MemoryPatch{
		Decisions: checkpointStrings(payload.Decisions), Facts: checkpointStrings(payload.Facts),
		Excluded: checkpointStrings(payload.Excluded), Progress: checkpointStrings(payload.Progress),
		Verification: checkpointStrings(payload.Verification), NextActions: checkpointStrings(payload.NextActions),
	}
	var knowledge []checkpointKnowledge
	_ = json.Unmarshal(payload.Knowledge, &knowledge)
	candidates := make([]app.KnowledgeCandidate, 0, len(knowledge))
	for _, item := range knowledge {
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Body) == "" {
			continue
		}
		candidates = append(candidates, app.KnowledgeCandidate{
			Scope: item.Scope, Type: item.Type, Title: item.Title, Body: item.Body, Confidence: item.Confidence,
		})
	}
	var agentActions []checkpointAgentAction
	_ = json.Unmarshal(payload.AgentActions, &agentActions)
	actions := make([]app.AgentAction, 0, len(agentActions))
	for _, item := range agentActions {
		if strings.TrimSpace(item.Assignment) == "" {
			continue
		}
		required := true
		if item.Required != nil {
			required = *item.Required
		}
		actions = append(actions, app.AgentAction{
			AgentID: strings.TrimSpace(item.AgentID), Title: strings.TrimSpace(item.Title),
			Assignment: strings.TrimSpace(item.Assignment), Required: required,
			Backend: strings.TrimSpace(item.Backend), ModelID: strings.TrimSpace(item.ModelID),
			ReasoningEffort: strings.TrimSpace(item.ReasoningEffort),
			Filesystem:      strings.TrimSpace(item.Filesystem), Approval: strings.TrimSpace(item.Approval),
		})
	}
	return visible, patch, candidates, actions, strings.TrimSpace(payload.MainFollowup)
}

func checkpointStrings(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var stringsOnly []string
	if json.Unmarshal(raw, &stringsOnly) == nil {
		return stringsOnly
	}
	var values []any
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	keys := []string{
		"decision", "fact", "item", "progress", "verification", "next_action",
		"text", "summary", "body", "description", "value",
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				result = append(result, text)
			}
		case map[string]any:
			text := ""
			for _, key := range keys {
				if candidate, ok := typed[key].(string); ok && strings.TrimSpace(candidate) != "" {
					text = strings.TrimSpace(candidate)
					break
				}
			}
			if text == "" {
				for _, candidate := range typed {
					if candidate, ok := candidate.(string); ok && strings.TrimSpace(candidate) != "" {
						text = strings.TrimSpace(candidate)
						break
					}
				}
			}
			if text != "" {
				result = append(result, text)
			}
		}
	}
	return result
}

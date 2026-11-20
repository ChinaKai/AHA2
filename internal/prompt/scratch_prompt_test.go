package prompt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestBuildExposesAgentScratchDirectory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	workDir := filepath.Join("repo", ".worktree")
	input := BuildInput{
		Project:   domain.Project{Name: "Project"},
		Workspace: domain.Workspace{Name: "Workspace", RootPath: filepath.Join("repo"), Transport: "native"},
		Task: domain.Task{
			ID: "task-scratch", Code: "task-001", Title: "Scratch", TaskWorkspacePath: workDir,
			CollaborationMode: "single", MaxAgents: 1,
		},
		Agent:       domain.TaskAgent{AgentID: "main", Role: "main"},
		Snapshot:    domain.RuntimeConfigSnapshot{Backend: "codex"},
		UserMessage: "hello",
		Language:    domain.LanguageEnglish,
	}
	result, err := engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(workDir, ".aha2-context", "task-scratch", "scratch", "main")
	for _, expected := range []string{
		"- scratch dir: " + want,
		"scratch is the only writable AHA context directory",
		"`AHA2_SCRATCH_DIR`",
		"never write them to the workspace root",
		"Clean scratch before finishing",
	} {
		if !strings.Contains(result.EffectivePrompt, expected) {
			t.Fatalf("English prompt is missing %q:\n%s", expected, result.EffectivePrompt)
		}
	}

	input.Language = domain.LanguageSimplified
	result, err = engine.Build(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"- scratch 目录：" + want,
		"scratch 是唯一可写的 AHA 上下文目录",
		"`AHA2_SCRATCH_DIR`",
		"不要写入 workspace 根目录",
		"最终答复前清理 scratch 目录",
	} {
		if !strings.Contains(result.EffectivePrompt, expected) {
			t.Fatalf("Chinese prompt is missing %q:\n%s", expected, result.EffectivePrompt)
		}
	}
}

func TestBuildKeepsEachAgentScratchSeparate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	engine := NewEngine(database)
	base := BuildInput{
		Project:   domain.Project{Name: "Project"},
		Workspace: domain.Workspace{Name: "Workspace", RootPath: "/repo", Transport: "native"},
		Task: domain.Task{
			ID: "task-scratch", Code: "task-001", Title: "Scratch", TaskWorkspacePath: "/repo",
			CollaborationMode: "auto", MaxAgents: 2,
		},
		Snapshot:    domain.RuntimeConfigSnapshot{Backend: "codex"},
		UserMessage: "hello",
	}
	for _, agent := range []domain.TaskAgent{
		{AgentID: "main", Role: "main"},
		{AgentID: "sub-001", Role: "sub"},
	} {
		input := base
		input.Agent = agent
		result, err := engine.Build(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join("/repo", ".aha2-context", "task-scratch", "scratch", agent.AgentID)
		if !strings.Contains(result.EffectivePrompt, want) {
			t.Fatalf("%s prompt does not expose its own scratch path %q", agent.AgentID, want)
		}
		if agent.AgentID != "main" {
			mainScratch := filepath.Join("/repo", ".aha2-context", "task-scratch", "scratch", "main")
			if strings.Contains(result.EffectivePrompt, mainScratch) {
				t.Fatalf("%s prompt reused the Main scratch path:\n%s", agent.AgentID, result.EffectivePrompt)
			}
		}
	}
}

// The Task state card is the handoff note between Turns. A Task Agent must see
// what the last Turn left, must be warned when that Turn skipped the update,
// and a Task with no history must not be shown an empty card.
func TestTaskStateCardRendersInTheTaskSection(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "memory-card.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	engine := NewEngine(db)
	finished := time.Now().UTC()
	base := BuildInput{
		Project:   domain.Project{Name: "Fixture"},
		Workspace: domain.Workspace{Name: "Workspace", RootPath: "/repo", Transport: "native"},
		Task: domain.Task{
			ID: "task-card", Code: "task-001", Title: "Card", TaskWorkspacePath: "/repo",
			CollaborationMode: "single", MaxAgents: 1,
		},
		Agent:    domain.TaskAgent{AgentID: "main", Role: "main"},
		Snapshot: domain.RuntimeConfigSnapshot{Backend: "codex"}, UserMessage: "continue",
		Language: domain.LanguageEnglish, CurrentTurnID: "turn-3",
	}
	build := func(input BuildInput) string {
		t.Helper()
		result, err := engine.Build(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		return result.EffectivePrompt
	}

	if prompt := build(base); strings.Contains(prompt, "## Task state card") {
		t.Fatalf("a Task with no earlier Turn was shown an empty card:\n%s", prompt)
	} else if !strings.Contains(prompt, "update the Task state card with `memory_update`") {
		t.Fatal("Main is not told to maintain the state card")
	}

	fresh := base
	fresh.Turns = []domain.Turn{
		{ID: "turn-1", AgentID: "main", Sequence: 1, FinishedAt: finished},
		{ID: "turn-2", AgentID: "main", Sequence: 2, FinishedAt: finished},
		{ID: "turn-sub", AgentID: "sub-001", Sequence: 3, FinishedAt: finished},
		{ID: "turn-3", AgentID: "main", Sequence: 4},
	}
	fresh.Memory = domain.TaskMemory{
		Status: "Fix written; integration tests pending.", StatusTurnID: "turn-2",
		NextSteps: []string{"Run integration tests"}, OpenQuestions: []string{"Fix the log leak too?"},
		Constraints: []domain.TaskMemoryNote{{Text: "Do not change the public API"}},
	}
	prompt := build(fresh)
	for _, expected := range []string{
		"## Task state card", "where things stand: Fix written; integration tests pending.",
		"Run integration tests", "Do not change the public API", "Fix the log leak too?",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("state card is missing %q:\n%s", expected, prompt)
		}
	}
	if strings.Contains(prompt, "ended without updating this card") {
		t.Fatal("a card written by the latest Main Turn was reported stale")
	}
	if strings.Index(prompt, "## Task state card") > strings.Index(prompt, "## Current Inbox Batch") {
		t.Fatal("the state card must be read before the Inbox")
	}

	stale := fresh
	stale.Memory.StatusTurnID = "turn-1"
	if !strings.Contains(build(stale), "ended without updating this card") {
		t.Fatal("a card the previous Main Turn skipped was not flagged as stale")
	}

	// The card belongs to Task Agents: a channel host Task and the pet keep
	// their state elsewhere and must not be asked to maintain one.
	host := fresh
	host.ChannelContext = map[string]any{"endpoint": domain.ChannelEndpointAssistantDM}
	if hostPrompt := build(host); strings.Contains(hostPrompt, "## Task state card") || strings.Contains(hostPrompt, "update the Task state card") {
		t.Fatal("a channel host Task was given the Task state card")
	}
}

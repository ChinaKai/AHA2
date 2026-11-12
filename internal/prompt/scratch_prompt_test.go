package prompt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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

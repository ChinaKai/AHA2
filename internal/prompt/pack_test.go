package prompt

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestBuildIncludesMemoryAndKnowledge(t *testing.T) {
	t.Parallel()
	value := Build(PackInput{
		Project:     domain.Project{Name: "AHA2"},
		Workspace:   domain.Workspace{Name: "local", RootPath: "/repo", Transport: "native"},
		Task:        domain.Task{ID: "task-1", OriginalRequest: "build", CurrentGoal: "test"},
		Memory:      domain.TaskMemory{Facts: []string{"turn is persistent"}},
		UserMessage: "continue",
		ProjectKnowledge: []domain.KnowledgeEntry{{
			Title: "State boundary", Body: "Task and Turn differ.", Status: domain.KnowledgeVerified, Confidence: 0.9,
		}},
	})
	for _, expected := range []string{"turn is persistent", "State boundary", "continue"} {
		if !strings.Contains(value, expected) {
			t.Fatalf("prompt does not contain %q", expected)
		}
	}
}

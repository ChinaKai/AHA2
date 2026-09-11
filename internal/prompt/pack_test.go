package prompt

import (
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMemoryAndKnowledgeFormatting(t *testing.T) {
	t.Parallel()
	value := memoryText(domain.TaskMemory{Facts: []string{"turn is persistent"}}) + "\n" +
		knowledgeText([]domain.KnowledgeEntry{{
			Title: "State boundary", Body: "Task and Turn differ.", Status: domain.KnowledgeVerified, Confidence: 0.9,
		}})
	for _, expected := range []string{"turn is persistent", "State boundary"} {
		if !strings.Contains(value, expected) {
			t.Fatalf("prompt does not contain %q", expected)
		}
	}
}

func TestKnowledgeProtocolRequiresTurnCloseout(t *testing.T) {
	for _, expected := range []string{
		"Before the final response of every Turn, perform a Knowledge closeout",
		"the Main Agent must submit a Knowledge candidate or revision",
		"explicitly record that no Knowledge submission is needed",
	} {
		if !strings.Contains(knowledgeProtocol, expected) {
			t.Fatalf("knowledge protocol does not contain %q", expected)
		}
	}
}

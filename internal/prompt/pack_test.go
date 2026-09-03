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

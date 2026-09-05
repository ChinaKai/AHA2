package prompt

import (
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func memoryText(memory domain.TaskMemory) string {
	return strings.Join([]string{
		list("decisions", memory.Decisions),
		list("facts", memory.Facts),
		list("excluded", memory.Excluded),
		list("progress", memory.Progress),
		list("verification", memory.Verification),
		list("next actions", memory.NextActions),
	}, "\n")
}

func knowledgeText(entries []domain.KnowledgeEntry) string {
	if len(entries) == 0 {
		return "-"
	}
	var result []string
	for _, entry := range entries {
		result = append(result, fmt.Sprintf(
			"### %s [%s, confidence %.2f]\n%s",
			entry.Title,
			entry.Status,
			entry.Confidence,
			entry.Body,
		))
	}
	return strings.Join(result, "\n\n")
}

func list(title string, values []string) string {
	if len(values) == 0 {
		return "- " + title + ": -"
	}
	return "- " + title + ":\n  - " + strings.Join(values, "\n  - ")
}

const agentAPIProtocol = `Use the Task-scoped Agent Control API described in agent-api.md for all structured state changes.

Submit durable Task Memory updates, Knowledge candidates and feedback, Skill package changes, collaboration requests, and intermediate user-facing progress through the API while working. The API token binds the current Task, Agent, and Turn; never print, persist, or include it in a response.
Only the main agent may update Task Memory, publish Knowledge, change Skills, or request collaboration. Sub-agents send their result naturally and may submit progress and Knowledge feedback.
Never use backend-native spawn, fanout, delegation, or multi-agent tools. AHA is the only Agent orchestrator.
The final response must contain only the concise user-facing natural-language conclusion. Do not append JSON, XML, checkpoints, hidden state, or machine-readable protocols.`

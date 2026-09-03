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

const checkpointProtocol = `Before ending this turn, append exactly one machine-readable checkpoint after the user-facing response:

<aha2_checkpoint>
{"decisions":[],"facts":[],"excluded":[],"progress":[],"verification":[],"next_actions":[],"knowledge_candidates":[{"scope":"project","type":"practice","title":"","body":"","confidence":0.8}],"main_followup":"","agent_actions":[{"agent_id":"sub-001","title":"focused assignment","assignment":"complete, self-contained work with disjoint ownership and validation target","required":true,"backend":"","model_id":"","reasoning_effort":"","filesystem":"","approval":""}]}
</aha2_checkpoint>

Only include durable, evidence-backed information. Omit empty knowledge candidates.
Only the main agent may request agent_actions. Set main_followup when main should immediately continue its own disjoint work in parallel after AHA starts the requested sub-agents; otherwise leave it empty. Omitted runtime fields inherit the main Agent's current configuration. AHA enforces the Task's collaboration mode and maximum Agent count. Sub-agents must leave agent_actions empty and main_followup empty.
Never call backend-native spawn-agent, collaboration, fanout, or delegation tools. AHA is the only Agent orchestrator.
Send concise intermediate assistant progress messages as separate messages when work state changes. If the user requests timed or repeated updates, emit every actual update separately at the requested interval. Never claim that updates were sent when no corresponding assistant messages were emitted.
Do not expose credentials or secret environment values in the response, events, artifacts, task memory, or knowledge.`

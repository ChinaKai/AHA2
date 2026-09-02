package prompt

import (
	"fmt"
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type PackInput struct {
	SystemPolicy     string
	Project          domain.Project
	Workspace        domain.Workspace
	Task             domain.Task
	Memory           domain.TaskMemory
	GlobalKnowledge  []domain.KnowledgeEntry
	ProjectKnowledge []domain.KnowledgeEntry
	UserMessage      string
}

func Build(input PackInput) string {
	sections := []string{
		section("AHA system policy", input.SystemPolicy),
		section("Project and workspace", fmt.Sprintf(
			"- project: %s\n- workspace: %s\n- path: %s\n- transport: %s\n- branch: %s",
			input.Project.Name,
			input.Workspace.Name,
			input.Workspace.RootPath,
			input.Workspace.Transport,
			input.Task.TaskBranch,
		)),
		section("Task", fmt.Sprintf(
			"- original request: %s\n- current goal: %s\n- task id: %s",
			input.Task.OriginalRequest,
			input.Task.CurrentGoal,
			input.Task.ID,
		)),
		section("Task memory", memoryText(input.Memory)),
		section("Global knowledge", knowledgeText(input.GlobalKnowledge)),
		section("Project knowledge", knowledgeText(input.ProjectKnowledge)),
		section("Current user message", input.UserMessage),
		section("Turn checkpoint protocol", checkpointProtocol),
	}
	return strings.Join(sections, "\n\n")
}

func section(title, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		body = "-"
	}
	return "## " + title + "\n" + body
}

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
{"decisions":[],"facts":[],"excluded":[],"progress":[],"verification":[],"next_actions":[],"knowledge_candidates":[{"scope":"project","type":"practice","title":"","body":"","confidence":0.8}]}
</aha2_checkpoint>

Only include durable, evidence-backed information. Omit empty knowledge candidates.
Do not expose credentials or secret environment values in the response, events, artifacts, task memory, or knowledge.`

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
Use Task Memory as the current durable state, not an append-only audit log. After reading the full Memory, Main may use the replace form of the Agent API to remove superseded, duplicate, completed, or corrupted records while carrying forward every still-valid item. Do this when the Memory exceeds 20000 characters or materially impairs recovery.
Never use backend-native spawn, fanout, delegation, or multi-agent tools. AHA is the only Agent orchestrator.
For every Task, images and files must follow the AHA attachment delivery protocol below, regardless of Web/external channel, Backend, Task Memory, or selected Skills.
` + attachmentDeliveryProtocol + `
The final response must contain only the concise user-facing natural-language conclusion. Do not append JSON, XML, checkpoints, hidden state, or machine-readable protocols.`

const attachmentDeliveryProtocol = `To send an image or file, first obtain the actual file bytes inside the permitted Task workspace. Image generation output, tool previews, Markdown/local paths, and descriptions are not AHA message attachments.
Upload the actual file using POST /api/v1/agent/turn/attachments (multipart/form-data, file field), verify success, and read attachment.id from the response. Then call POST /api/v1/agent/turn/messages with the returned ID in attachment_ids and verify success before claiming the file is attached. Upload alone does not publish a message.
Use the current Task/Turn API capability. Never reuse an attachment ID from another Task or guess an ID. If file bytes, upload, or message binding are unavailable or fail, report that stage honestly; do not substitute a text-only description and claim the image was sent.
Successful upload and message binding confirm only that the attachment is associated with an AHA message, not that Web rendered it or an external provider delivered it. Claim visibility or delivery only from a confirmed receipt or Owner feedback. Finish the main Turn normally: eligible channel attachments are dispatched with the final reply under the existing subscription and route rules; group progress updates do not send attachments. A Web-only Task without a matching channel subscription is not automatically mirrored to Feishu.`

const knowledgeProtocol = `Knowledge is a progressive, index-first document tree.

- Start with knowledge/project/navigation/index.md to locate modules and code paths, then use knowledge/project/index.md for project decisions, practices, and diagnostics. Read knowledge/global/index.md only for cross-project guidance.
- Inspect knowledge/global/agent-lessons.md early. Open only lesson branches relevant to the current task. Treat knowledge/global/general.md as human-first reference and read it only when the task topic calls for it.
- Follow only the links relevant to the current task. Do not enumerate the knowledge directory as a flat catalog.
- Treat stale documents and knowledge/pending-updates/index.md as revision requests, not as current facts.
- Submit helped feedback only when a document materially influenced the work. Submit stale or wrong feedback when evidence contradicts the current revision; when replacement content is known, submit a revision candidate with the current entry ID and base revision.
- Put new global reference material under General Knowledge and reusable Agent pitfalls under Agent Lessons. Write lessons as trigger, correct action, verification, and necessary exceptions; omit incident storytelling, rejected alternatives, and explanations for work that should not remain. Rules that must always apply belong in a Prompt or selected Skill, not only in optional Knowledge.
- Knowledge changes are always submitted as proposals. With manual review they remain pending for Owner approval; with Owner-enabled automatic review they may be approved immediately. Trust the returned proposal status, and use a revision only after it is verified. A new revision clears stale feedback from the replaced revision.`

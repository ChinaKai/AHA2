package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
	promptpkg "github.com/ChinaKai/AHA2/internal/prompt"
	workspacepkg "github.com/ChinaKai/AHA2/internal/workspace"
)

// AgentInboxClaim is the control-plane half of live message injection for DSH.
// The plugin claims at a tool boundary, steers the returned text into the
// running step, then acknowledges delivery (or rolls the claim back on error).
type AgentInboxClaim struct {
	Messages                []domain.LiveInboxMessage `json:"messages"`
	NextCursor              int64                     `json:"next_cursor"`
	InjectionText           string                    `json:"injection_text"`
	MaterializedAttachments []string                  `json:"materialized_attachments,omitempty"`
}

// ClaimAgentInbox claims pending owner/channel and pet-delegated messages for
// the active turn without blocking. It is deliberately separate from
// WaitAgentInbox: Wait is the model-driven path, claim is the in-process DSH
// checkpoint.
func (s *Service) ClaimAgentInbox(ctx context.Context, claims agentapi.Claims, limit int) (AgentInboxClaim, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return AgentInboxClaim{}, err
	}
	return s.claimLiveInbox(ctx, call.Task, call.Turn, limit)
}

// claimLiveInbox claims pending owner/channel and pet-delegated messages for
// turn and renders them as injection text. Both the DSH plugin endpoint and a
// stream-input Claude Turn go through here, so they share one claim semantics.
func (s *Service) claimLiveInbox(ctx context.Context, task domain.Task, turn domain.Turn, limit int) (AgentInboxClaim, error) {
	items, err := s.store.ClaimOwnerInboxForLiveInjection(
		ctx, task.ID, turn.AgentID, turn.RoundID, turn.ID, limit,
	)
	if err != nil {
		return AgentInboxClaim{}, err
	}
	result := AgentInboxClaim{Messages: []domain.LiveInboxMessage{}}
	if len(items) == 0 {
		return result, nil
	}
	call := AgentCallContext{Task: task, Turn: turn}
	materialized, omittedAttachments := s.materializeLiveInboxAttachments(ctx, call.Task, items)
	result.Messages = make([]domain.LiveInboxMessage, 0, len(items))
	for _, item := range items {
		result.Messages = append(result.Messages, liveInboxMessage(item))
		if item.Sequence > result.NextCursor {
			result.NextCursor = item.Sequence
		}
	}
	result.InjectionText = liveInboxInjectionText(result.Messages, materialized, omittedAttachments)
	for attachmentID := range materialized {
		result.MaterializedAttachments = append(result.MaterializedAttachments, attachmentID)
	}
	sort.Strings(result.MaterializedAttachments)
	s.emit(ctx, call.Task.ID, "inbox", call.Turn.ID, "agent_inbox_live_claimed", map[string]any{
		"round_id": call.Turn.RoundID, "turn_id": call.Turn.ID, "agent_id": call.Turn.AgentID,
		"message_count": len(result.Messages), "next_cursor": result.NextCursor,
		"attachment_count": len(result.MaterializedAttachments),
	})
	return result, nil
}

// AckAgentInbox confirms that the claimed messages reached the live model step,
// or rolls the claim back to Pending when injection failed.
func (s *Service) AckAgentInbox(ctx context.Context, claims agentapi.Claims, ids []string, outcome string) (int, error) {
	call, err := s.AgentCallContext(ctx, claims, false)
	if err != nil {
		return 0, err
	}
	delivered := false
	switch strings.TrimSpace(outcome) {
	case "delivered", "steered":
		delivered = true
	case "failed", "rollback":
		delivered = false
	default:
		return 0, fmt.Errorf("inbox acknowledgement outcome must be delivered or failed")
	}
	cleaned := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		cleaned = append(cleaned, id)
	}
	if len(cleaned) == 0 || len(cleaned) > 50 {
		return 0, fmt.Errorf("inbox acknowledgement needs 1-50 message ids")
	}
	return s.ackLiveInbox(ctx, call.Task, call.Turn, cleaned, delivered)
}

// ackLiveInbox settles a live-injection claim for turn: delivered moves it to
// delivered_in_turn, otherwise it returns to Pending.
func (s *Service) ackLiveInbox(ctx context.Context, task domain.Task, turn domain.Turn, ids []string, delivered bool) (int, error) {
	call := AgentCallContext{Task: task, Turn: turn}
	count, err := s.store.AckLiveInboxForTurn(
		ctx, call.Task.ID, call.Turn.AgentID, call.Turn.ID, ids, delivered, s.now().UTC(),
	)
	if err != nil {
		return 0, err
	}
	event := "agent_inbox_live_steered"
	if !delivered {
		event = "agent_inbox_live_rollback"
	}
	s.emit(ctx, call.Task.ID, "inbox", call.Turn.ID, event, map[string]any{
		"round_id": call.Turn.RoundID, "turn_id": call.Turn.ID, "agent_id": call.Turn.AgentID,
		"message_count": count,
	})
	return count, nil
}

// turnLiveInbox lets a backend that streams input into a running process
// claim this Turn's live messages itself. It uses the same claim and ack as
// the DSH plugin, so a message is claimed by at most one of them, and an
// unacknowledged claim is still requeued when the Turn ends.
type turnLiveInbox struct {
	service *Service
	task    domain.Task
	turn    domain.Turn
}

func (inbox turnLiveInbox) Claim(ctx context.Context) (backend.LiveBatch, error) {
	claim, err := inbox.service.claimLiveInbox(ctx, inbox.task, inbox.turn, 20)
	if err != nil || len(claim.Messages) == 0 {
		return backend.LiveBatch{}, err
	}
	batch := backend.LiveBatch{Text: claim.InjectionText}
	for _, message := range claim.Messages {
		batch.IDs = append(batch.IDs, message.ID)
	}
	return batch, nil
}

func (inbox turnLiveInbox) Ack(ctx context.Context, batch backend.LiveBatch, delivered bool) error {
	if len(batch.IDs) == 0 {
		return nil
	}
	_, err := inbox.service.ackLiveInbox(ctx, inbox.task, inbox.turn, batch.IDs, delivered)
	return err
}

const (
	liveAttachmentOmitTooLarge = "too_large"
	liveAttachmentOmitBudget   = "budget_exhausted"
)

func (s *Service) materializeLiveInboxAttachments(
	ctx context.Context,
	task domain.Task,
	items []domain.AgentInboxItem,
) (map[string]string, map[string]string) {
	paths := map[string]string{}
	omitted := map[string]string{}
	workspace, err := s.store.Workspace(ctx, task.WorkspaceID)
	if err != nil {
		return paths, omitted
	}
	workDir := strings.TrimSpace(task.TaskWorkspacePath)
	if workDir == "" {
		workDir = workspace.RootPath
	}
	attachments := []domain.Attachment{}
	for _, item := range items {
		raw, ok := item.Payload["attachments"]
		if !ok {
			continue
		}
		data, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var references []domain.Attachment
		if json.Unmarshal(data, &references) != nil {
			continue
		}
		for _, reference := range references {
			if reference.ID == "" {
				continue
			}
			attachment, err := s.store.Attachment(ctx, task.ID, reference.ID)
			if err != nil {
				continue
			}
			attachments = append(attachments, attachment)
		}
	}
	// items are oldest-first; the allocator gives the newest message's
	// attachments the budget first, matching the live inbox priority.
	decisions := allocateInlineAttachments(
		attachments,
		promptpkg.MaxInlineAttachmentBytes,
		promptpkg.MaxInlineAttachmentBytesPerTurn,
		func(item domain.Attachment) ([]byte, error) { return s.store.AttachmentContent(item) },
	)
	for index, attachment := range attachments {
		decision := decisions[index]
		if decision.Err != nil {
			continue
		}
		if !decision.Inline {
			switch {
			case attachment.Size > promptpkg.MaxInlineAttachmentBytes:
				omitted[attachment.ID] = liveAttachmentOmitTooLarge
			case decision.BudgetExhausted:
				omitted[attachment.ID] = liveAttachmentOmitBudget
			}
			continue
		}
		relative := pathpkg.Join(".aha2-context", "live-attachments", attachment.ID, liveAttachmentName(attachment.Name))
		if err := writeLiveAttachment(ctx, workspace, workDir, relative, decision.Content); err != nil {
			continue
		}
		paths[attachment.ID] = relative
	}
	return paths, omitted
}

func writeLiveAttachment(ctx context.Context, workspace domain.Workspace, workDir, relative string, content []byte) error {
	if workspace.Transport == "native" {
		target := filepath.Join(workDir, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o600)
	}
	target := workspacepkg.JoinRemotePath(workspace, workDir, filepath.ToSlash(relative))
	return workspacepkg.WriteRemoteTextFile(ctx, workspacepkg.RunnerFor(workspace), target, string(content))
}

func liveAttachmentName(name string) string {
	name = pathpkg.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	switch name {
	case "", ".", "..", "/":
		return "attachment"
	}
	return name
}

func liveInboxInjectionText(
	messages []domain.LiveInboxMessage,
	materialized map[string]string,
	omitted map[string]string,
) string {
	if len(messages) == 0 {
		return ""
	}
	var output strings.Builder
	fmt.Fprintf(&output,
		"[AHA live inbox] %d new message(s) arrived while this Turn is still running. "+
			"They are already delivered to you; do not call inbox/wait for them and do not defer them to a later Turn. "+
			"Apply them now, before continuing your current work.\n", len(messages))
	for index, message := range messages {
		fmt.Fprintf(&output, "\n### Message %d\n", index+1)
		fmt.Fprintf(&output, "Source: %s\n", liveInboxSourceLabel(message))
		if receipt, _ := message.Payload["channel_receipt_id"].(string); strings.TrimSpace(receipt) != "" {
			fmt.Fprintf(&output, "Channel reply receipt: %s (use this with the channel reply API if this message needs a reply)\n", strings.TrimSpace(receipt))
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			content = "(no text)"
		}
		output.WriteString(content)
		output.WriteString("\n")
		output.WriteString(liveInboxAttachmentLines(message.Payload, materialized, omitted))
	}
	return strings.TrimSpace(output.String())
}

func liveInboxSourceLabel(message domain.LiveInboxMessage) string {
	source := message.Source
	switch source.Kind {
	case "channel":
		name := strings.TrimSpace(source.DisplayName)
		if name == "" {
			name = "channel participant"
		}
		role := strings.TrimSpace(source.Role)
		if role == "" {
			role = "participant"
		}
		kind := "channel"
		if source.IsBot {
			kind = "channel bot"
		}
		authority := strings.TrimSpace(source.Authority)
		if authority == "" {
			authority = "channel_context"
		}
		return fmt.Sprintf("%s (%s, role=%s, authority=%s)", name, kind, role, authority)
	case "owner":
		return "owner (authority=owner)"
	case "pet":
		// The pet may relay the owner's intent, but it is never the owner
		// identity itself; keep the authority distinct in the injection text.
		return "pet (authority=owner_delegate)"
	default:
		return fmt.Sprintf("%s (authority=%s)", source.Kind, source.Authority)
	}
}

func liveInboxAttachmentLines(payload map[string]any, materialized, omitted map[string]string) string {
	raw, ok := payload["attachments"]
	if !ok {
		return ""
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	var references []domain.Attachment
	if json.Unmarshal(data, &references) != nil || len(references) == 0 {
		return ""
	}
	var output strings.Builder
	output.WriteString("Attachments:\n")
	for _, reference := range references {
		fmt.Fprintf(&output, "- %s (%s, %d bytes, id %s, sha256 %s)",
			liveAttachmentName(reference.Name), reference.MediaType, reference.Size, reference.ID, reference.SHA256)
		switch {
		case materialized[reference.ID] != "":
			fmt.Fprintf(&output, ": %s", materialized[reference.ID])
		case omitted[reference.ID] == liveAttachmentOmitBudget:
			fmt.Fprintf(&output,
				"; content not inlined: the per-Turn Context inline budget was exhausted by newer attachments. "+
					"Download: GET /api/v1/tasks/%s/attachments/%s (an Owner session is required)",
				reference.TaskID, reference.ID)
		case omitted[reference.ID] == liveAttachmentOmitTooLarge:
			fmt.Fprintf(&output,
				"; content not inlined: it exceeds the %d MiB per-Turn Context inline limit. "+
					"Download: GET /api/v1/tasks/%s/attachments/%s (an Owner session is required)",
				promptpkg.MaxInlineAttachmentBytes/(1<<20), reference.TaskID, reference.ID)
		default:
			output.WriteString(": not materialized")
		}
		output.WriteString("\n")
	}
	return output.String()
}

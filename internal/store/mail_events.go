package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Mail timeline event names from design section 6.2.
const (
	MailEventApprovalRequested = "mail_approval_requested"
	MailEventApproved          = "mail_approved"
	MailEventRejected          = "mail_rejected"
	MailEventQueued            = "mail_queued"
	MailEventSending           = "mail_sending"
	MailEventSent              = "mail_sent"
	MailEventFailed            = "mail_failed"
	MailEventDeadLetter        = "mail_dead_letter"
	MailEventCancelled         = "mail_cancelled"
	MailEventRetried           = "mail_retried"
)

// MailTransition describes one durable Mail Message status change. It is
// written to events and upserted into the single conversation card in the same
// transaction as the state update, then published after commit.
type MailTransition struct {
	TaskID     string
	MessageID  string
	TurnID     string
	AgentID    string
	EventType  string
	Status     domain.MailMessageStatus
	ErrorCode  string
	OccurredAt time.Time
}

// MailCardID is the stable conversation item id for one Mail Message. The
// timeline card is upserted, never appended per status change.
func MailCardID(messageID string) string {
	return "conversation-mail-" + strings.TrimSpace(messageID)
}

// AppendMailTransitionInTx inserts the durable event row and upserts the
// category='mail', kind='mail_message' card. Payload intentionally contains no
// body, credential, recipient list or download token.
func AppendMailTransitionInTx(ctx context.Context, exec mailDBTX, transition MailTransition) (domain.Event, error) {
	transition.TaskID = strings.TrimSpace(transition.TaskID)
	transition.MessageID = strings.TrimSpace(transition.MessageID)
	transition.EventType = strings.TrimSpace(transition.EventType)
	if transition.TaskID == "" || transition.MessageID == "" || transition.EventType == "" {
		return domain.Event{}, fmt.Errorf("mail transition requires task, message and event type")
	}
	if !transition.Status.Valid() {
		return domain.Event{}, fmt.Errorf("invalid mail message status %q", transition.Status)
	}
	occurredAt := transition.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	payload := map[string]any{
		"message_id": transition.MessageID,
		"status":     string(transition.Status),
		"created_at": mailTimeString(occurredAt),
	}
	if transition.ErrorCode != "" {
		payload["error_code"] = transition.ErrorCode
	}
	event := domain.Event{
		ID: domain.NewID("event"), AggregateType: "task", AggregateID: transition.TaskID,
		Type: transition.EventType, Data: payload, OccurredAt: occurredAt,
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO events(id,aggregate_type,aggregate_id,type,data_json,occurred_at)
		VALUES(?,?,?,?,?,?)`,
		event.ID, event.AggregateType, event.AggregateID, event.Type,
		encodeJSON(event.Data), mailTimeString(event.OccurredAt)); err != nil {
		return domain.Event{}, err
	}
	agentID := strings.TrimSpace(transition.AgentID)
	if agentID == "" {
		agentID = "main"
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO conversation_items(
			id,task_id,round_id,turn_id,agent_id,stream_agent_id,from_agent_id,to_agent_id,
			route_kind,category,kind,summary,payload_json,created_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET summary=excluded.summary,payload_json=excluded.payload_json`,
		MailCardID(transition.MessageID), transition.TaskID, "", transition.TurnID,
		agentID, agentID, agentID, agentID, "mail", "mail", "mail_message",
		"邮件状态："+string(transition.Status), encodeJSON(payload), mailTimeString(occurredAt)); err != nil {
		return domain.Event{}, err
	}
	return event, nil
}

// mailMessageRouting loads the fields needed to address a message's timeline
// event and card.
func mailMessageRouting(ctx context.Context, exec mailDBTX, messageID string) (taskID, turnID, agentID string, err error) {
	err = exec.QueryRowContext(ctx, `
		SELECT task_id,turn_id,agent_id FROM mail_messages WHERE id=?`, messageID).
		Scan(&taskID, &turnID, &agentID)
	if err == sql.ErrNoRows {
		return "", "", "", fmt.Errorf("mail message %s not found", messageID)
	}
	return taskID, turnID, agentID, err
}

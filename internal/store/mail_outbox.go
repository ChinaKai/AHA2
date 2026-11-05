package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Mail state-transition errors. Handlers map these to the stable API codes
// from design section 4.4 instead of exposing SQL details.
var (
	ErrMailInvalidState    = errors.New("mail operation is not allowed in the current state")
	ErrMailDigestMismatch  = errors.New("mail approval digest does not match the submitted content")
	ErrMailLeaseLost       = errors.New("mail delivery lease is no longer owned by this worker")
	ErrMailNoActiveBinding = errors.New("mail account is not bound to this scope")
)

// MailDeliveryReclaimResult reports which deliveries an expired-lease sweep
// moved. IDs are kept for audit/log lines; no message content is included.
type MailDeliveryReclaimResult struct {
	DeadLettered []string
	Requeued     []string
}

// MailDeliveryResultPatch is the worker's terminal or retry result. It is
// written under state='sending' AND lease_id=:lease, so a stale worker cannot
// overwrite a newer owner's state.
type MailDeliveryResultPatch struct {
	State            domain.MailDeliveryState
	Outcome          domain.MailAttemptOutcome
	OutcomeCertainty domain.MailOutcomeCertainty
	ErrorCode        domain.MailErrorCode
	ErrorDetail      string
	SMTPCode         int
	SMTPResponse     string
	MessageIDHeader  string
	Phase            domain.MailSMTPPhase
	AvailableAt      time.Time
	Backoff          time.Duration
}

// appendMailAuditDeduped records an operational warning at most once per
// ten-minute window for the same resource. Repeated claim/reclaim collisions
// must not grow the audit table without bound.
func (s *Store) appendMailAuditDeduped(ctx context.Context, actor, action, resourceType, resourceID string, data map[string]any, now time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM audit_events
		WHERE action=? AND resource_type=? AND resource_id=? AND occurred_at>=?)`,
		action, resourceType, resourceID, now.Add(-10*time.Minute).Format(time.RFC3339Nano)).Scan(&exists)
	if err == nil && exists {
		return
	}
	_ = s.AppendAudit(ctx, actor, action, resourceType, resourceID, data, now.Format(time.RFC3339Nano))
}

func appendMailAuditTx(ctx context.Context, exec mailDBTX, actor, action, resourceType, resourceID string, data map[string]any, now time.Time) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO audit_events(id,owner_id,action,resource_type,resource_id,data_json,occurred_at)
		VALUES(?,?,?,?,?,?,?)`,
		domain.NewID("audit"), actor, action, resourceType, resourceID,
		encodeJSON(data), mailTimeString(now))
	return err
}

func scanMailDeliveryRows(rows *sql.Rows) ([]domain.MailDelivery, error) {
	result := make([]domain.MailDelivery, 0)
	for rows.Next() {
		item, err := scanMailDelivery(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ReclaimExpiredMailDeliveries runs the three ordered conditional UPDATEs from
// design section 6.3 in one transaction. The order matters:
//  1. attempts-exhausted rows always become dead_letter;
//  2. expired pre-DATA leases are safely requeued;
//  3. expired DATA/finished leases are ambiguous and become dead_letter.
func (s *Store) ReclaimExpiredMailDeliveries(ctx context.Context, now time.Time) (MailDeliveryReclaimResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := MailDeliveryReclaimResult{DeadLettered: []string{}, Requeued: []string{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	// 1) Retry budget exhausted: expired sending or leftover queued rows with
	// attempts>=8 can never be sent again.
	rows, err := tx.QueryContext(ctx, `
		UPDATE mail_deliveries
		SET state='dead_letter',
		    outcome_certainty=CASE WHEN smtp_phase IN ('data','finished') THEN 'unknown' ELSE '' END,
		    lease_id='', lease_until='',
		    last_error_code=CASE WHEN smtp_phase IN ('data','finished')
		                         THEN 'smtp_ambiguous_after_data' ELSE 'smtp_attempts_exhausted' END,
		    updated_at=?
		WHERE ((state='sending' AND lease_until<=?) OR state='queued')
		  AND attempts>=?
		RETURNING id,message_id`,
		mailTimeString(now), mailTimeString(now), domain.MailDeliveryMaxAttempts)
	if err != nil {
		return result, err
	}
	exhausted, err := scanMailDeliveryIDs(rows)
	if err != nil {
		return result, err
	}
	for _, item := range exhausted {
		if err := updateReclaimMessage(ctx, tx, item.messageID,
			domain.MailMessageDeadLetter, []domain.MailMessageStatus{domain.MailMessageQueued, domain.MailMessageSending}, now); err != nil {
			return s.failMailReclaim(ctx, tx, item, "exhausted", now, err)
		}
		if err := appendMailAuditTx(ctx, tx, "mail_worker", "mail.delivery.reclaim_exhausted",
			"mail_delivery", item.id, map[string]any{
				"message_id": item.messageID, "reason": string(domain.MailErrSMTPAttemptsExhausted),
			}, now); err != nil {
			return result, err
		}
		result.DeadLettered = append(result.DeadLettered, item.id)
	}

	// 2) Lease expired before DATA: safe to retry.
	rows, err = tx.QueryContext(ctx, `
		UPDATE mail_deliveries
		SET state='queued', smtp_phase='', lease_id='', lease_until='',
		    available_at=?, last_error_code=?, updated_at=?
		WHERE state='sending' AND lease_until<=? AND attempts<?
		  AND smtp_phase IN ('','connect','tls','auth','mail_from','rcpt_to')
		RETURNING id,message_id`,
		mailTimeString(now), string(domain.MailErrSMTPLeaseExpiredBeforeData),
		mailTimeString(now), mailTimeString(now), domain.MailDeliveryMaxAttempts)
	if err != nil {
		return result, err
	}
	requeued, err := scanMailDeliveryIDs(rows)
	if err != nil {
		return result, err
	}
	for _, item := range requeued {
		if err := updateReclaimMessage(ctx, tx, item.messageID,
			domain.MailMessageQueued, []domain.MailMessageStatus{domain.MailMessageSending}, now); err != nil {
			return s.failMailReclaim(ctx, tx, item, "requeued", now, err)
		}
		if err := appendMailAuditTx(ctx, tx, "mail_worker", "mail.delivery.reclaim_requeued",
			"mail_delivery", item.id, map[string]any{
				"message_id": item.messageID, "reason": string(domain.MailErrSMTPLeaseExpiredBeforeData),
			}, now); err != nil {
			return result, err
		}
		result.Requeued = append(result.Requeued, item.id)
	}

	// 3) Lease expired in DATA/finished: the server may have accepted the
	// message. Never retry automatically.
	rows, err = tx.QueryContext(ctx, `
		UPDATE mail_deliveries
		SET state='dead_letter', outcome_certainty='unknown',
		    lease_id='', lease_until='', last_error_code=?, updated_at=?
		WHERE state='sending' AND lease_until<=? AND attempts<?
		  AND smtp_phase IN ('data','finished')
		RETURNING id,message_id`,
		string(domain.MailErrSMTPAmbiguousAfterData), mailTimeString(now),
		mailTimeString(now), domain.MailDeliveryMaxAttempts)
	if err != nil {
		return result, err
	}
	ambiguous, err := scanMailDeliveryIDs(rows)
	if err != nil {
		return result, err
	}
	for _, item := range ambiguous {
		if err := updateReclaimMessage(ctx, tx, item.messageID,
			domain.MailMessageDeadLetter, []domain.MailMessageStatus{domain.MailMessageQueued, domain.MailMessageSending}, now); err != nil {
			return s.failMailReclaim(ctx, tx, item, "ambiguous", now, err)
		}
		if err := appendMailAuditTx(ctx, tx, "mail_worker", "mail.delivery.reclaim_ambiguous",
			"mail_delivery", item.id, map[string]any{
				"message_id": item.messageID, "reason": string(domain.MailErrSMTPAmbiguousAfterData),
			}, now); err != nil {
			return result, err
		}
		result.DeadLettered = append(result.DeadLettered, item.id)
	}

	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func updateReclaimMessage(ctx context.Context, tx *sql.Tx, messageID string, to domain.MailMessageStatus, from []domain.MailMessageStatus, now time.Time) error {
	if len(from) == 0 {
		return fmt.Errorf("reclaim message expected states are required")
	}
	placeholders := make([]string, 0, len(from))
	args := []any{to, mailTimeString(now), messageID}
	for _, status := range from {
		if !status.Valid() {
			return fmt.Errorf("invalid mail message status %q", status)
		}
		placeholders = append(placeholders, "?")
		args = append(args, status)
	}
	result, err := tx.ExecContext(ctx, `UPDATE mail_messages SET status=?,updated_at=?
		WHERE id=? AND status IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("reclaim message %s did not move from %v to %s", messageID, from, to)
	}
	return nil
}

// failMailReclaim rolls the whole sweep back when a Message cannot follow its
// Delivery. A partial reclaim would leave Message and Delivery split.
func (s *Store) failMailReclaim(ctx context.Context, tx *sql.Tx, item mailDeliveryIDPair, reason string, now time.Time, cause error) (MailDeliveryReclaimResult, error) {
	_ = tx.Rollback()
	s.appendMailAuditDeduped(ctx, "mail_worker", "mail.delivery.reclaim_failed",
		"mail_delivery", item.id, map[string]any{
			"message_id": item.messageID, "reason": reason, "error": cause.Error(),
		}, now)
	return MailDeliveryReclaimResult{DeadLettered: []string{}, Requeued: []string{}}, fmt.Errorf("reclaim mail delivery %s: %w", item.id, cause)
}

type mailDeliveryIDPair struct {
	id        string
	messageID string
}

func scanMailDeliveryIDs(rows *sql.Rows) ([]mailDeliveryIDPair, error) {
	defer rows.Close()
	result := make([]mailDeliveryIDPair, 0)
	for rows.Next() {
		var item mailDeliveryIDPair
		if err := rows.Scan(&item.id, &item.messageID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ClaimMailDelivery atomically claims one queued delivery and moves its Mail
// Message queued -> sending in the same transaction. accountID optionally
// restricts the claim to one account so the worker can enforce per-account
// concurrency.
func (s *Store) ClaimMailDelivery(ctx context.Context, accountID string, now time.Time, leaseDuration time.Duration) (domain.MailDelivery, bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if leaseDuration <= 0 {
		return domain.MailDelivery{}, false, fmt.Errorf("mail delivery lease duration must be positive")
	}
	leaseID := domain.NewID("mail_lease")
	leaseUntil := now.Add(leaseDuration)
	accountID = strings.TrimSpace(accountID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MailDelivery{}, false, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		UPDATE mail_deliveries
		SET state='sending', lease_id=?, lease_until=?, attempts=attempts+1,
		    smtp_phase='connect', updated_at=?
		WHERE id = (
			SELECT id FROM mail_deliveries
			WHERE state='queued' AND available_at<=? AND attempts<?
			  AND (?='' OR account_id=?)
			ORDER BY available_at, created_at, id
			LIMIT 1
		)
		AND state='queued' AND available_at<=? AND attempts<?
		RETURNING `+mailDeliveryColumns,
		leaseID, mailTimeString(leaseUntil), mailTimeString(now),
		mailTimeString(now), domain.MailDeliveryMaxAttempts, accountID, accountID,
		mailTimeString(now), domain.MailDeliveryMaxAttempts)
	if err != nil {
		return domain.MailDelivery{}, false, err
	}
	claimed, err := scanMailDeliveryRows(rows)
	if err != nil {
		return domain.MailDelivery{}, false, err
	}
	rows.Close()
	if len(claimed) == 0 {
		return domain.MailDelivery{}, false, nil
	}
	item := claimed[0]

	messageResult, err := tx.ExecContext(ctx, `
		UPDATE mail_messages SET status='sending',updated_at=?
		WHERE id=? AND status='queued'`, mailTimeString(now), item.MessageID)
	if err != nil {
		return domain.MailDelivery{}, false, err
	}
	if affected, _ := messageResult.RowsAffected(); affected != 1 {
		// The Message was not queued (for example it was cancelled). Roll the
		// claim back so the delivery does not look in flight, then audit the
		// discarded claim outside the transaction.
		_ = tx.Rollback()
		s.appendMailAuditDeduped(ctx, "mail_worker", "mail.delivery.claim_rejected",
			"mail_delivery", item.ID, map[string]any{
				"message_id": item.MessageID, "reason": "message_not_queued",
			}, now)
		return domain.MailDelivery{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return domain.MailDelivery{}, false, err
	}
	return item, true, nil
}

// DeferMailDelivery returns a claimed delivery to queued without consuming an
// SMTP attempt. It is used when local preflight rejects the send (disabled or
// rotated account, missing credential, misconfigured download base).
func (s *Store) DeferMailDelivery(ctx context.Context, deliveryID, leaseID, messageID string, errorCode domain.MailErrorCode, detail string, availableAt, now time.Time) (bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if availableAt.IsZero() {
		availableAt = now.Add(time.Minute)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	patch := MailDeliveryStatePatch{
		AttemptsDelta:   -1,
		AvailableAt:     &availableAt,
		LastErrorCode:   stringPtr(string(errorCode)),
		LastErrorDetail: stringPtr(detail),
		RequireLeaseID:  leaseID,
	}
	ok, err := s.UpdateMailDeliveryStateInTx(ctx, tx, deliveryID,
		[]domain.MailDeliveryState{domain.MailDeliverySending},
		domain.MailDeliveryQueued, patch, now)
	if err != nil {
		return false, err
	}
	if !ok {
		_ = tx.Rollback()
		s.appendMailAuditDeduped(ctx, "mail_worker", "mail.delivery.defer_discarded",
			"mail_delivery", deliveryID, map[string]any{
				"message_id": messageID, "error_code": string(errorCode),
			}, now)
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE mail_messages SET status='queued',updated_at=?
		WHERE id=? AND status='sending'`, mailTimeString(now), messageID)
	if err != nil {
		return false, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return false, fmt.Errorf("mail message %s did not return to queued", messageID)
	}
	if err := appendMailAuditTx(ctx, tx, "mail_worker", "mail.delivery.deferred",
		"mail_delivery", deliveryID, map[string]any{
			"message_id": messageID, "error_code": string(errorCode),
		}, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// FinishMailDelivery writes one attempt and the resulting Delivery/Message
// state under the worker's lease. A zero-row delivery update means another
// writer owns the row; the result is discarded and never touches the Message.
func (s *Store) FinishMailDelivery(ctx context.Context, delivery domain.MailDelivery, leaseID string, result MailDeliveryResultPatch, now time.Time) (bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	switch result.State {
	case domain.MailDeliverySent, domain.MailDeliveryFailed, domain.MailDeliveryDeadLetter, domain.MailDeliveryQueued:
	default:
		return false, fmt.Errorf("invalid mail delivery result state %q", result.State)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	patch := MailDeliveryStatePatch{
		RequireLeaseID:  leaseID,
		SMTPResponse:    stringPtr(sanitizeSMTPResponse(result.SMTPResponse)),
		LastErrorCode:   stringPtr(string(result.ErrorCode)),
		LastErrorDetail: stringPtr(result.ErrorDetail),
	}
	if result.MessageIDHeader != "" {
		// Retries and failures must keep the Message-ID persisted at first send.
		patch.MessageIDHeader = stringPtr(result.MessageIDHeader)
	}
	if result.OutcomeCertainty != "" {
		patch.OutcomeCertainty = &result.OutcomeCertainty
	}
	if result.State == domain.MailDeliveryQueued {
		availableAt := result.AvailableAt
		if availableAt.IsZero() {
			availableAt = now.Add(time.Second)
		}
		patch.AvailableAt = &availableAt
	}
	if result.Phase != "" && result.State != domain.MailDeliveryQueued {
		patch.SMTPPhase = &result.Phase
	}
	if result.State == domain.MailDeliverySent {
		patch.SentAt = &now
	}
	ok, err := s.UpdateMailDeliveryStateInTx(ctx, tx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliverySending},
		result.State, patch, now)
	if err != nil {
		return false, err
	}
	if !ok {
		// Stale worker: record the discarded result for audit, keep any newer
		// owner's state, and never update the Message.
		_ = tx.Rollback()
		s.appendMailAuditDeduped(ctx, "mail_worker", "mail.delivery.writeback_discarded",
			"mail_delivery", delivery.ID, map[string]any{
				"message_id": delivery.MessageID, "attempt": delivery.Attempts,
				"result_state": string(result.State), "error_code": string(result.ErrorCode),
			}, now)
		return false, nil
	}
	backoffMS := result.Backoff.Milliseconds()
	startedAt := delivery.UpdatedAt
	if startedAt.IsZero() || startedAt.After(now) {
		startedAt = now
	}
	if _, err := s.CreateMailDeliveryAttemptInTx(ctx, tx, domain.MailDeliveryAttempt{
		DeliveryID: delivery.ID, AttemptNo: delivery.Attempts, LeaseID: leaseID,
		Outcome: result.Outcome, ErrorCode: string(result.ErrorCode),
		SMTPCode: result.SMTPCode, SMTPPhase: result.Phase, BackoffMS: backoffMS,
		StartedAt: startedAt, FinishedAt: now,
	}); err != nil {
		return false, err
	}
	messageStatus := domain.MailMessageStatus(string(result.State))
	messageResult, err := tx.ExecContext(ctx, `
		UPDATE mail_messages SET status=?,updated_at=?
		WHERE id=? AND status='sending'`,
		messageStatus, mailTimeString(now), delivery.MessageID)
	if err != nil {
		return false, err
	}
	if affected, _ := messageResult.RowsAffected(); affected != 1 {
		return false, fmt.Errorf("mail message %s did not move to %s", delivery.MessageID, messageStatus)
	}
	if err := appendMailAuditTx(ctx, tx, "mail_worker", "mail.delivery."+string(result.State),
		"mail_delivery", delivery.ID, map[string]any{
			"message_id": delivery.MessageID, "attempt": delivery.Attempts,
			"error_code": string(result.ErrorCode), "outcome_certainty": string(result.OutcomeCertainty),
		}, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// SetMailDeliveryMessageIDHeader persists the RFC 5322 Message-ID before the
// first SMTP attempt so every retry of the same delivery reuses it.
func (s *Store) SetMailDeliveryMessageIDHeader(ctx context.Context, id, leaseID, header string, now time.Time) (bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_deliveries SET message_id_header=?,updated_at=?
		WHERE id=? AND state='sending' AND lease_id=?`,
		strings.TrimSpace(header), mailTimeString(now), id, leaseID)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

func sanitizeSMTPResponse(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}

func stringPtr(value string) *string { return &value }

// SubmitMailMessage moves a draft to pending_approval and freezes its digest.
func (s *Store) SubmitMailMessage(ctx context.Context, id, digest string, now time.Time) (domain.MailMessage, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MailMessage{}, err
	}
	defer tx.Rollback()
	var status domain.MailMessageStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM mail_messages WHERE id=?`, id).Scan(&status); err != nil {
		return domain.MailMessage{}, err
	}
	if status != domain.MailMessageDraft {
		return domain.MailMessage{}, ErrMailInvalidState
	}
	if strings.TrimSpace(digest) == "" {
		return domain.MailMessage{}, ErrMailDigestMismatch
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE mail_messages SET status='pending_approval',approval_digest=?,submitted_at=?,updated_at=?
		WHERE id=? AND status='draft'`,
		digest, mailTimeString(now), mailTimeString(now), id)
	if err != nil {
		return domain.MailMessage{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.MailMessage{}, ErrMailInvalidState
	}
	if err := tx.Commit(); err != nil {
		return domain.MailMessage{}, err
	}
	return s.MailMessage(ctx, id)
}

// ApproveMailMessage verifies the digest, freezes approval and creates the
// first queued delivery. A repeated approval with the same digest and owner is
// idempotent and returns the existing active delivery.
func (s *Store) ApproveMailMessage(ctx context.Context, id, digest, ownerID string, now time.Time) (domain.MailMessage, domain.MailDelivery, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	digest = strings.TrimSpace(digest)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MailMessage{}, domain.MailDelivery{}, err
	}
	defer tx.Rollback()

	message, err := scanMailMessage(tx.QueryRowContext(ctx,
		`SELECT `+mailMessageColumns+` FROM mail_messages WHERE id=?`, id))
	if err != nil {
		return domain.MailMessage{}, domain.MailDelivery{}, err
	}
	if digest == "" || message.ApprovalDigest != digest {
		return domain.MailMessage{}, domain.MailDelivery{}, ErrMailDigestMismatch
	}

	switch message.Status {
	case domain.MailMessagePendingApproval:
		result, err := tx.ExecContext(ctx, `
			UPDATE mail_messages SET status='queued',approved_by=?,approved_at=?,updated_at=?
			WHERE id=? AND status='pending_approval'`,
			ownerID, mailTimeString(now), mailTimeString(now), id)
		if err != nil {
			return domain.MailMessage{}, domain.MailDelivery{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.MailMessage{}, domain.MailDelivery{}, ErrMailInvalidState
		}
		delivery, err := s.CreateMailDeliveryInTx(ctx, tx, domain.MailDelivery{
			MessageID: message.ID, Generation: 0, AccountID: message.AccountID,
			State: domain.MailDeliveryQueued, AvailableAt: now, ApprovedDigest: digest,
		})
		if err != nil {
			return domain.MailMessage{}, domain.MailDelivery{}, err
		}
		if err := appendMailAuditTx(ctx, tx, ownerID, "mail.message.approve",
			"mail_message", message.ID, map[string]any{
				"delivery_id": delivery.ID, "generation": delivery.Generation,
				"digest": digest,
			}, now); err != nil {
			return domain.MailMessage{}, domain.MailDelivery{}, err
		}
		if err := tx.Commit(); err != nil {
			return domain.MailMessage{}, domain.MailDelivery{}, err
		}
		updated, err := s.MailMessage(ctx, id)
		return updated, delivery, err
	case domain.MailMessageQueued:
		delivery, err := scanMailDelivery(tx.QueryRowContext(ctx,
			`SELECT `+mailDeliveryColumns+` FROM mail_deliveries
			 WHERE message_id=? ORDER BY generation DESC LIMIT 1`, id))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.MailMessage{}, domain.MailDelivery{}, ErrMailInvalidState
			}
			return domain.MailMessage{}, domain.MailDelivery{}, err
		}
		// Idempotent only while the delivery is still active. Approving an
		// already sent/failed/dead-letter message is an invalid operation.
		if delivery.ApprovedDigest == digest && message.ApprovedBy == ownerID &&
			(delivery.State == domain.MailDeliveryQueued || delivery.State == domain.MailDeliverySending) {
			if err := tx.Commit(); err != nil {
				return domain.MailMessage{}, domain.MailDelivery{}, err
			}
			return message, delivery, nil
		}
		return domain.MailMessage{}, domain.MailDelivery{}, ErrMailInvalidState
	default:
		return domain.MailMessage{}, domain.MailDelivery{}, ErrMailInvalidState
	}
}

// RejectMailMessage moves pending_approval to rejected.
func (s *Store) RejectMailMessage(ctx context.Context, id, reason string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	// A rejection is a decision, not an approval: approved_by/approved_at
	// remain empty so a rejected message can never look approved.
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_messages SET status='rejected',rejected_reason=?,updated_at=?
		WHERE id=? AND status='pending_approval'`,
		strings.TrimSpace(reason), mailTimeString(now), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrMailInvalidState
	}
	return nil
}

// CancelMailMessage cancels pending, queued (including backoff), failed and
// dead-letter messages. A queued delivery must lose its conditional
// queued -> cancelled update if a worker already claimed it.
func (s *Store) CancelMailMessage(ctx context.Context, id, ownerID string, now time.Time) (domain.MailMessage, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MailMessage{}, err
	}
	defer tx.Rollback()
	var status domain.MailMessageStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM mail_messages WHERE id=?`, id).Scan(&status); err != nil {
		return domain.MailMessage{}, err
	}
	switch status {
	case domain.MailMessageDraft, domain.MailMessagePendingApproval,
		domain.MailMessageFailed, domain.MailMessageDeadLetter:
		// A failed/dead-letter generation is terminal, but the state machine
		// still records an explicit Owner cancellation on the delivery.
		if status == domain.MailMessageFailed || status == domain.MailMessageDeadLetter {
			if _, err := tx.ExecContext(ctx, `
				UPDATE mail_deliveries SET state='cancelled',lease_id='',lease_until='',updated_at=?
				WHERE message_id=? AND state IN ('failed','dead_letter')`,
				mailTimeString(now), id); err != nil {
				return domain.MailMessage{}, err
			}
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE mail_messages SET status='cancelled',updated_at=?
			WHERE id=? AND status=?`, mailTimeString(now), id, status)
		if err != nil {
			return domain.MailMessage{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.MailMessage{}, ErrMailInvalidState
		}
	case domain.MailMessageQueued:
		deliveryResult, err := tx.ExecContext(ctx, `
			UPDATE mail_deliveries SET state='cancelled',lease_id='',lease_until='',updated_at=?
			WHERE message_id=? AND state='queued'`, mailTimeString(now), id)
		if err != nil {
			return domain.MailMessage{}, err
		}
		if affected, _ := deliveryResult.RowsAffected(); affected != 1 {
			// A worker claimed it; sending can no longer be cancelled safely.
			return domain.MailMessage{}, ErrMailInvalidState
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE mail_messages SET status='cancelled',updated_at=?
			WHERE id=? AND status='queued'`, mailTimeString(now), id)
		if err != nil {
			return domain.MailMessage{}, err
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return domain.MailMessage{}, ErrMailInvalidState
		}
	case domain.MailMessageSending:
		return domain.MailMessage{}, ErrMailInvalidState
	default:
		return domain.MailMessage{}, ErrMailInvalidState
	}
	if err := appendMailAuditTx(ctx, tx, ownerID, "mail.message.cancel",
		"mail_message", id, map[string]any{"from_status": string(status)}, now); err != nil {
		return domain.MailMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MailMessage{}, err
	}
	return s.MailMessage(ctx, id)
}

// RetryMailMessage creates a new delivery generation for the same frozen
// snapshot. Only failed/dead_letter messages are retryable, and the digest must
// still match the submitted snapshot.
func (s *Store) RetryMailMessage(ctx context.Context, id, digest, ownerID string, now time.Time) (domain.MailDelivery, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MailDelivery{}, err
	}
	defer tx.Rollback()
	message, err := scanMailMessage(tx.QueryRowContext(ctx,
		`SELECT `+mailMessageColumns+` FROM mail_messages WHERE id=?`, id))
	if err != nil {
		return domain.MailDelivery{}, err
	}
	if message.Status != domain.MailMessageFailed && message.Status != domain.MailMessageDeadLetter {
		return domain.MailDelivery{}, ErrMailInvalidState
	}
	if digest == "" || message.ApprovalDigest != digest {
		return domain.MailDelivery{}, ErrMailDigestMismatch
	}
	generation := 0
	replayOf := ""
	latest, err := scanMailDelivery(tx.QueryRowContext(ctx,
		`SELECT `+mailDeliveryColumns+` FROM mail_deliveries
		 WHERE message_id=? ORDER BY generation DESC LIMIT 1`, id))
	if err == nil {
		generation = latest.Generation + 1
		replayOf = latest.ID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.MailDelivery{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE mail_messages SET status='queued',updated_at=?
		WHERE id=? AND status IN ('failed','dead_letter')`,
		mailTimeString(now), id)
	if err != nil {
		return domain.MailDelivery{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.MailDelivery{}, ErrMailInvalidState
	}
	delivery, err := s.CreateMailDeliveryInTx(ctx, tx, domain.MailDelivery{
		MessageID: message.ID, Generation: generation, ReplayOfID: replayOf,
		AccountID: message.AccountID, State: domain.MailDeliveryQueued,
		AvailableAt: now, ApprovedDigest: digest,
	})
	if err != nil {
		return domain.MailDelivery{}, err
	}
	if err := appendMailAuditTx(ctx, tx, ownerID, "mail.message.retry",
		"mail_message", id, map[string]any{
			"delivery_id": delivery.ID, "generation": delivery.Generation,
			"replay_of_id": replayOf,
		}, now); err != nil {
		return domain.MailDelivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MailDelivery{}, err
	}
	return delivery, nil
}

// MailMessageFilter bounds the Owner list query.
type MailMessageFilter struct {
	TaskID    string
	AccountID string
	Status    domain.MailMessageStatus
	Limit     int
}

func (s *Store) ListMailMessages(ctx context.Context, filter MailMessageFilter) ([]domain.MailMessage, error) {
	query := `SELECT ` + mailMessageColumns + ` FROM mail_messages WHERE 1=1`
	args := make([]any, 0, 4)
	if value := strings.TrimSpace(filter.TaskID); value != "" {
		query += ` AND task_id=?`
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.AccountID); value != "" {
		query += ` AND account_id=?`
		args = append(args, value)
	}
	if filter.Status != "" {
		if !filter.Status.Valid() {
			return nil, fmt.Errorf("invalid mail message status %q", filter.Status)
		}
		query += ` AND status=?`
		args = append(args, filter.Status)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, mailListLimit(filter.Limit))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailMessage, 0)
	for rows.Next() {
		item, err := scanMailMessage(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// MailMessageByFingerprintSince implements the duplicate-content window.
func (s *Store) MailMessageByFingerprintSince(ctx context.Context, taskID, fingerprint string, since time.Time) (domain.MailMessage, error) {
	return scanMailMessage(s.db.QueryRowContext(ctx, `SELECT `+mailMessageColumns+`
		FROM mail_messages
		WHERE task_id=? AND content_fingerprint=? AND created_at>=?
		  AND status NOT IN ('rejected','cancelled')
		ORDER BY created_at DESC LIMIT 1`,
		strings.TrimSpace(taskID), strings.TrimSpace(fingerprint), mailTimeString(since)))
}

// MailDeliveryQueuedAccountIDs returns accounts with at least one claimable
// delivery, so the worker can enforce per-account limits before claiming.
func (s *Store) MailDeliveryQueuedAccountIDs(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT account_id FROM mail_deliveries
		WHERE state='queued' AND available_at<=? AND attempts<?
		ORDER BY account_id LIMIT ?`,
		mailTimeString(now), domain.MailDeliveryMaxAttempts, mailListLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			return nil, err
		}
		result = append(result, accountID)
	}
	return result, rows.Err()
}

// MailAccountSendingCount counts in-flight deliveries for one account.
func (s *Store) MailAccountSendingCount(ctx context.Context, accountID string, now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM mail_deliveries
		WHERE account_id=? AND state='sending' AND lease_until>?`,
		strings.TrimSpace(accountID), mailTimeString(now)).Scan(&count)
	return count, err
}

// MailAccountAttemptCountSince counts SMTP attempts started since a cutoff.
func (s *Store) MailAccountAttemptCountSince(ctx context.Context, accountID string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM mail_delivery_attempts a
		JOIN mail_deliveries d ON d.id=a.delivery_id
		WHERE d.account_id=? AND a.started_at>=?`,
		strings.TrimSpace(accountID), mailTimeString(since)).Scan(&count)
	return count, err
}

// MailDeliveryHasActiveForMessage reports whether a queued/sending delivery
// already exists. Owner retry must create a new generation only afterwards.
func (s *Store) MailDeliveryHasActiveForMessage(ctx context.Context, messageID string) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM mail_deliveries
		WHERE message_id=? AND state IN ('queued','sending'))`,
		messageID).Scan(&active)
	return active, err
}

// MailAttachmentByLinkTokenHash resolves a controlled-download token digest.
func (s *Store) MailAttachmentByLinkTokenHash(ctx context.Context, hash string) (domain.MailAttachment, error) {
	return scanMailAttachment(s.db.QueryRowContext(ctx,
		`SELECT `+mailAttachmentColumns+` FROM mail_attachments WHERE link_token_hash=?`,
		strings.TrimSpace(hash)))
}

// IncrementMailAttachmentLinkDownload atomically consumes one download slot.
// It returns false for expired, revoked, exhausted or unknown links without
// revealing which condition failed.
func (s *Store) IncrementMailAttachmentLinkDownload(ctx context.Context, id string, now time.Time) (bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_attachments SET link_download_count=link_download_count+1
		WHERE id=? AND link_token_hash<>'' AND link_revoked_at=''
		  AND link_expires_at>? AND link_download_count<link_max_downloads`,
		id, mailTimeString(now))
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

// ExplainMailDeliveryReclaim returns the query plan for the pre-DATA reclaim
// statement. The acceptance test asserts it uses idx_mail_deliveries_reclaim.
func (s *Store) ExplainMailDeliveryReclaim(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		EXPLAIN QUERY PLAN
		UPDATE mail_deliveries
		SET state='queued', smtp_phase='', lease_id='', lease_until='',
		    available_at=?, last_error_code=?, updated_at=?
		WHERE state='sending' AND lease_until<=? AND attempts<?
		  AND smtp_phase IN ('','connect','tls','auth','mail_from','rcpt_to')`,
		mailTimeString(time.Now().UTC()), string(domain.MailErrSMTPLeaseExpiredBeforeData),
		mailTimeString(time.Now().UTC()), mailTimeString(time.Now().UTC()),
		domain.MailDeliveryMaxAttempts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			return nil, err
		}
		result = append(result, detail)
	}
	return result, rows.Err()
}

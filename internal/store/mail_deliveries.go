package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const mailDeliveryColumns = `id,message_id,generation,replay_of_id,account_id,state,
	attempts,available_at,lease_id,lease_until,smtp_phase,message_id_header,
	smtp_response,last_error_code,last_error_detail,outcome_certainty,
	approved_digest,sent_at,created_at,updated_at`

func scanMailDelivery(scanner interface{ Scan(...any) error }) (domain.MailDelivery, error) {
	var item domain.MailDelivery
	var replayOfID sql.NullString
	var availableAt, leaseUntil, sentAt, createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.MessageID, &item.Generation, &replayOfID, &item.AccountID,
		&item.State, &item.Attempts, &availableAt, &item.LeaseID, &leaseUntil,
		&item.SMTPPhase, &item.MessageIDHeader, &item.SMTPResponse,
		&item.LastErrorCode, &item.LastErrorDetail, &item.OutcomeCertainty,
		&item.ApprovedDigest, &sentAt, &createdAt, &updatedAt,
	)
	item.ReplayOfID = replayOfID.String
	item.AvailableAt = parseMailTime(availableAt)
	item.LeaseUntil = parseMailTime(leaseUntil)
	item.SentAt = parseMailTime(sentAt)
	item.CreatedAt = parseMailTime(createdAt)
	item.UpdatedAt = parseMailTime(updatedAt)
	return item, err
}

// MailDeliveryStatePatch describes optional columns to set alongside a
// conditional state change. Nil pointers leave a column untouched; empty
// strings clear text columns. RequireLeaseID adds the worker's lease guard, so
// a stale worker cannot overwrite a newer owner's result.
type MailDeliveryStatePatch struct {
	AttemptsDelta    int
	AvailableAt      *time.Time
	LeaseID          *string
	LeaseUntil       *time.Time
	SMTPPhase        *domain.MailSMTPPhase
	MessageIDHeader  *string
	SMTPResponse     *string
	LastErrorCode    *string
	LastErrorDetail  *string
	OutcomeCertainty *domain.MailOutcomeCertainty
	ApprovedDigest   *string
	SentAt           *time.Time
	RequireLeaseID   string
}

func normalizeMailDelivery(item domain.MailDelivery, create bool) (domain.MailDelivery, error) {
	item.MessageID = strings.TrimSpace(item.MessageID)
	item.AccountID = strings.TrimSpace(item.AccountID)
	item.ReplayOfID = strings.TrimSpace(item.ReplayOfID)
	if item.MessageID == "" {
		return item, fmt.Errorf("mail delivery message_id is required")
	}
	if item.AccountID == "" {
		return item, fmt.Errorf("mail delivery account_id is required")
	}
	if create && item.ID == "" {
		item.ID = domain.NewID("mail_delivery")
	}
	if !create && item.ID == "" {
		return item, fmt.Errorf("mail delivery id is required")
	}
	if item.State == "" {
		item.State = domain.MailDeliveryQueued
	}
	if !item.State.Valid() {
		return item, fmt.Errorf("invalid mail delivery state %q", item.State)
	}
	if item.Generation < 0 {
		item.Generation = 0
	}
	if item.Attempts < 0 {
		item.Attempts = 0
	}
	if item.SMTPPhase != "" && !item.SMTPPhase.Valid() {
		return item, fmt.Errorf("invalid mail smtp phase %q", item.SMTPPhase)
	}
	if item.State == domain.MailDeliverySending {
		if strings.TrimSpace(item.LeaseID) == "" || item.LeaseUntil.IsZero() {
			return item, fmt.Errorf("sending mail delivery requires a lease")
		}
	} else if strings.TrimSpace(item.LeaseID) != "" || !item.LeaseUntil.IsZero() {
		return item, fmt.Errorf("only sending mail deliveries may carry a lease")
	}
	now := time.Now().UTC()
	if item.AvailableAt.IsZero() {
		item.AvailableAt = now
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	return item, nil
}

func (s *Store) CreateMailDelivery(ctx context.Context, item domain.MailDelivery) (domain.MailDelivery, error) {
	return s.CreateMailDeliveryInTx(ctx, s.db, item)
}

func (s *Store) CreateMailDeliveryInTx(ctx context.Context, exec mailDBTX, item domain.MailDelivery) (domain.MailDelivery, error) {
	item, err := normalizeMailDelivery(item, true)
	if err != nil {
		return domain.MailDelivery{}, err
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO mail_deliveries(
			id,message_id,generation,replay_of_id,account_id,state,attempts,
			available_at,lease_id,lease_until,smtp_phase,message_id_header,
			smtp_response,last_error_code,last_error_detail,outcome_certainty,
			approved_digest,sent_at,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.MessageID, item.Generation, nullIfEmpty(item.ReplayOfID),
		item.AccountID, item.State, item.Attempts, mailTimeString(item.AvailableAt),
		item.LeaseID, mailTimeString(item.LeaseUntil), item.SMTPPhase,
		item.MessageIDHeader, item.SMTPResponse, item.LastErrorCode,
		item.LastErrorDetail, item.OutcomeCertainty, item.ApprovedDigest,
		mailTimeString(item.SentAt), mailTimeString(item.CreatedAt),
		mailTimeString(item.UpdatedAt),
	); err != nil {
		return domain.MailDelivery{}, err
	}
	return item, nil
}

func (s *Store) MailDelivery(ctx context.Context, id string) (domain.MailDelivery, error) {
	return scanMailDelivery(s.db.QueryRowContext(ctx,
		`SELECT `+mailDeliveryColumns+` FROM mail_deliveries WHERE id=?`, id))
}

func (s *Store) MailDeliveriesByMessage(ctx context.Context, messageID string, limit int) ([]domain.MailDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mailDeliveryColumns+`
		FROM mail_deliveries WHERE message_id=?
		ORDER BY generation DESC,created_at DESC,id DESC LIMIT ?`,
		messageID, mailListLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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

// UpdateMailDeliveryState is the conditional write used by claim, result and
// recovery paths. from must list the states the writer expects; an empty write
// count means another writer already moved the row. Writes from sending require
// a lease guard, entering sending requires a new lease, every other target
// state clears the lease, and a queued target also clears smtp_phase so a
// temporary DATA-phase failure can be retried.
func (s *Store) UpdateMailDeliveryState(ctx context.Context, id string, from []domain.MailDeliveryState, to domain.MailDeliveryState, patch MailDeliveryStatePatch, now time.Time) (bool, error) {
	return s.UpdateMailDeliveryStateInTx(ctx, s.db, id, from, to, patch, now)
}

func (s *Store) UpdateMailDeliveryStateInTx(ctx context.Context, exec mailDBTX, id string, from []domain.MailDeliveryState, to domain.MailDeliveryState, patch MailDeliveryStatePatch, now time.Time) (bool, error) {
	if !to.Valid() {
		return false, fmt.Errorf("invalid mail delivery state %q", to)
	}
	if strings.TrimSpace(id) == "" {
		return false, fmt.Errorf("mail delivery id is required")
	}
	if len(from) == 0 {
		return false, fmt.Errorf("mail delivery expected states are required")
	}
	fromSending := false
	placeholders := make([]string, 0, len(from))
	for _, state := range from {
		if !state.Valid() {
			return false, fmt.Errorf("invalid mail delivery state %q", state)
		}
		if state == domain.MailDeliverySending {
			fromSending = true
		}
		placeholders = append(placeholders, "?")
	}
	if fromSending && strings.TrimSpace(patch.RequireLeaseID) == "" {
		return false, fmt.Errorf("mail delivery lease guard is required for sending")
	}
	if to == domain.MailDeliverySending {
		if patch.LeaseID == nil || strings.TrimSpace(*patch.LeaseID) == "" ||
			patch.LeaseUntil == nil || patch.LeaseUntil.IsZero() {
			return false, fmt.Errorf("mail delivery entering sending requires a lease")
		}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sets := []string{`state=?`, `updated_at=?`}
	args := []any{to, mailTimeString(now)}
	if patch.AttemptsDelta != 0 {
		sets = append(sets, `attempts=attempts+?`)
		args = append(args, patch.AttemptsDelta)
	}
	if patch.AvailableAt != nil {
		sets = append(sets, `available_at=?`)
		args = append(args, mailTimeString(*patch.AvailableAt))
	}
	if to == domain.MailDeliverySending {
		sets = append(sets, `lease_id=?`, `lease_until=?`)
		args = append(args, *patch.LeaseID, mailTimeString(*patch.LeaseUntil))
	} else {
		// Leaving sending (or writing any other state) always clears the lease.
		sets = append(sets, `lease_id=''`, `lease_until=''`)
	}
	phaseGuard := ""
	if to == domain.MailDeliveryQueued {
		// A retry returns to queued with no in-flight phase. The Store owns this
		// invariant and overrides any phase the caller observed, so a stale
		// `data` value can never leave a queued row that the next claim cannot
		// pick up.
		if patch.SMTPPhase != nil && *patch.SMTPPhase != "" && !patch.SMTPPhase.Valid() {
			return false, fmt.Errorf("invalid mail smtp phase %q", *patch.SMTPPhase)
		}
		sets = append(sets, `smtp_phase=''`)
	} else if patch.SMTPPhase != nil {
		if *patch.SMTPPhase != "" && !patch.SMTPPhase.Valid() {
			return false, fmt.Errorf("invalid mail smtp phase %q", *patch.SMTPPhase)
		}
		sets = append(sets, `smtp_phase=?`)
		args = append(args, string(*patch.SMTPPhase))
		if to == domain.MailDeliverySending {
			// Only in-place sending progress is monotonic. Terminal and retry
			// writes must not be rejected because the attempt already reached
			// DATA or finished.
			phaseGuard = mailSMTPPhaseRegressionGuard(*patch.SMTPPhase)
		}
	}
	if patch.MessageIDHeader != nil {
		sets = append(sets, `message_id_header=?`)
		args = append(args, *patch.MessageIDHeader)
	}
	if patch.SMTPResponse != nil {
		sets = append(sets, `smtp_response=?`)
		args = append(args, *patch.SMTPResponse)
	}
	if patch.LastErrorCode != nil {
		sets = append(sets, `last_error_code=?`)
		args = append(args, *patch.LastErrorCode)
	}
	if patch.LastErrorDetail != nil {
		sets = append(sets, `last_error_detail=?`)
		args = append(args, *patch.LastErrorDetail)
	}
	if patch.OutcomeCertainty != nil {
		sets = append(sets, `outcome_certainty=?`)
		args = append(args, string(*patch.OutcomeCertainty))
	}
	if patch.ApprovedDigest != nil {
		sets = append(sets, `approved_digest=?`)
		args = append(args, *patch.ApprovedDigest)
	}
	if patch.SentAt != nil {
		sets = append(sets, `sent_at=?`)
		args = append(args, mailTimeString(*patch.SentAt))
	}
	query := `UPDATE mail_deliveries SET ` + strings.Join(sets, `,`) + ` WHERE id=?`
	args = append(args, id)
	query += ` AND state IN (` + strings.Join(placeholders, ",") + `)`
	for _, state := range from {
		args = append(args, state)
	}
	if patch.RequireLeaseID != "" {
		query += ` AND lease_id=?`
		args = append(args, patch.RequireLeaseID)
	}
	query += phaseGuard
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// mailSMTPPhaseRegressionGuard prevents a failed or stale worker from moving
// smtp_phase back before DATA. Once DATA or finished is recorded, a blind
// resend would be unsafe, so only forward transitions are permitted.
func mailSMTPPhaseRegressionGuard(phase domain.MailSMTPPhase) string {
	switch phase {
	case domain.MailSMTPPhaseFinished:
		return ""
	case domain.MailSMTPPhaseData:
		return ` AND smtp_phase<>'finished'`
	default:
		return ` AND smtp_phase NOT IN ('data','finished')`
	}
}

// SetMailDeliveryPhaseInTx records a phase before the worker proceeds. In
// particular, entering DATA must succeed under a valid lease or the worker must
// abort; this is the single mutual-exclusion point before sending.
func (s *Store) SetMailDeliveryPhaseInTx(ctx context.Context, exec mailDBTX, id, leaseID string, phase domain.MailSMTPPhase, now time.Time) (bool, error) {
	if phase != "" && !phase.Valid() {
		return false, fmt.Errorf("invalid mail smtp phase %q", phase)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query := `
		UPDATE mail_deliveries SET smtp_phase=?,updated_at=?
		WHERE id=? AND state='sending' AND lease_id=? AND lease_until>?` +
		mailSMTPPhaseRegressionGuard(phase)
	result, err := exec.ExecContext(ctx, query,
		string(phase), mailTimeString(now), id, leaseID, mailTimeString(now))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// SetMailDeliveryPhase is the standalone form used by the delivery worker; the
// InTx variant remains available for transactions composed inside this package.
func (s *Store) SetMailDeliveryPhase(ctx context.Context, id, leaseID string, phase domain.MailSMTPPhase, now time.Time) (bool, error) {
	return s.SetMailDeliveryPhaseInTx(ctx, s.db, id, leaseID, phase, now)
}

// RenewMailDeliveryLease extends a live worker's lease in place.
func (s *Store) RenewMailDeliveryLease(ctx context.Context, id, leaseID string, until, now time.Time) (bool, error) {
	return s.RenewMailDeliveryLeaseInTx(ctx, s.db, id, leaseID, until, now)
}

func (s *Store) RenewMailDeliveryLeaseInTx(ctx context.Context, exec mailDBTX, id, leaseID string, until, now time.Time) (bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := exec.ExecContext(ctx, `
		UPDATE mail_deliveries SET lease_until=?,updated_at=?
		WHERE id=? AND state='sending' AND lease_id=?`,
		mailTimeString(until), mailTimeString(now), id, leaseID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

const mailDeliveryAttemptColumns = `id,delivery_id,attempt_no,lease_id,outcome,
	error_code,smtp_code,smtp_phase,backoff_ms,started_at,finished_at`

func scanMailDeliveryAttempt(scanner interface{ Scan(...any) error }) (domain.MailDeliveryAttempt, error) {
	var item domain.MailDeliveryAttempt
	var startedAt, finishedAt string
	err := scanner.Scan(&item.ID, &item.DeliveryID, &item.AttemptNo,
		&item.LeaseID, &item.Outcome, &item.ErrorCode, &item.SMTPCode,
		&item.SMTPPhase, &item.BackoffMS, &startedAt, &finishedAt)
	item.StartedAt = parseMailTime(startedAt)
	item.FinishedAt = parseMailTime(finishedAt)
	return item, err
}

func normalizeMailDeliveryAttempt(item domain.MailDeliveryAttempt) (domain.MailDeliveryAttempt, error) {
	item.DeliveryID = strings.TrimSpace(item.DeliveryID)
	item.LeaseID = strings.TrimSpace(item.LeaseID)
	if item.DeliveryID == "" {
		return item, fmt.Errorf("mail delivery attempt delivery_id is required")
	}
	if item.AttemptNo < 1 {
		return item, fmt.Errorf("mail delivery attempt number must be at least 1")
	}
	switch item.Outcome {
	case domain.MailAttemptSent, domain.MailAttemptTransientFailure,
		domain.MailAttemptPermanentFailure, domain.MailAttemptUnknown,
		domain.MailAttemptCancelled:
	default:
		return item, fmt.Errorf("invalid mail delivery attempt outcome %q", item.Outcome)
	}
	if item.ID == "" {
		item.ID = domain.NewID("mail_delivery_attempt")
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = time.Now().UTC()
	}
	return item, nil
}

func (s *Store) CreateMailDeliveryAttempt(ctx context.Context, item domain.MailDeliveryAttempt) (domain.MailDeliveryAttempt, error) {
	return s.CreateMailDeliveryAttemptInTx(ctx, s.db, item)
}

func (s *Store) CreateMailDeliveryAttemptInTx(ctx context.Context, exec mailDBTX, item domain.MailDeliveryAttempt) (domain.MailDeliveryAttempt, error) {
	item, err := normalizeMailDeliveryAttempt(item)
	if err != nil {
		return domain.MailDeliveryAttempt{}, err
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO mail_delivery_attempts(
			id,delivery_id,attempt_no,lease_id,outcome,error_code,smtp_code,
			smtp_phase,backoff_ms,started_at,finished_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.DeliveryID, item.AttemptNo, item.LeaseID, item.Outcome,
		item.ErrorCode, item.SMTPCode, item.SMTPPhase, item.BackoffMS,
		mailTimeString(item.StartedAt), mailTimeString(item.FinishedAt)); err != nil {
		return domain.MailDeliveryAttempt{}, err
	}
	return item, nil
}

func (s *Store) MailDeliveryAttempts(ctx context.Context, deliveryID string, limit int) ([]domain.MailDeliveryAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mailDeliveryAttemptColumns+`
		FROM mail_delivery_attempts WHERE delivery_id=?
		ORDER BY attempt_no DESC LIMIT ?`, deliveryID, mailListLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailDeliveryAttempt, 0)
	for rows.Next() {
		item, err := scanMailDeliveryAttempt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

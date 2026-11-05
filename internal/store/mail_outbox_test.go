package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
)

func newMailOutboxFixture(t *testing.T) (*Store, domain.MailAccount, domain.MailMessage, []domain.MailAttachment, string) {
	t.Helper()
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	account := createMailAccountFixture(t, database, "Outbox", "outbox@example.com")
	message := createMailMessageFixture(t, database, taskID, account.ID, "outbox-1")
	attachment, err := database.CreateMailAttachment(ctx, domain.MailAttachment{
		MessageID: message.ID, Position: 0,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: "attachment_x",
		FileName: "report.txt", MediaType: "text/plain", SizeBytes: 5,
		SHA256: strings.Repeat("a", 64), DeliveryMode: domain.MailAttachmentDirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	attachments := []domain.MailAttachment{attachment}
	message, err = database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest := mailpkg.ApprovalDigest(message, attachments)
	message, err = database.SubmitMailMessage(ctx, message.ID, digest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return database, account, message, attachments, digest
}

func TestMailApprovalLifecycle(t *testing.T) {
	ctx := context.Background()
	database, account, message, _, digest := newMailOutboxFixture(t)
	now := time.Now().UTC()

	if message.Status != domain.MailMessagePendingApproval || message.ApprovalDigest != digest {
		t.Fatalf("submitted message = %+v", message)
	}
	approved, delivery, err := database.ApproveMailMessage(ctx, message.ID, digest, "owner-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != domain.MailMessageQueued || approved.ApprovedBy != "owner-1" ||
		delivery.State != domain.MailDeliveryQueued || delivery.ApprovedDigest != digest ||
		delivery.AccountID != account.ID {
		t.Fatalf("approved = %+v delivery = %+v", approved, delivery)
	}
	// Repeated approval with the same digest and owner is idempotent.
	_, repeated, err := database.ApproveMailMessage(ctx, message.ID, digest, "owner-1", now)
	if err != nil {
		t.Fatalf("idempotent approval: %v", err)
	}
	if repeated.ID != delivery.ID {
		t.Fatalf("repeated approval created a new delivery: %s vs %s", repeated.ID, delivery.ID)
	}
	// A different digest must not be accepted.
	if _, _, err := database.ApproveMailMessage(ctx, message.ID, "sha256:wrong", "owner-1", now); !errors.Is(err, ErrMailDigestMismatch) {
		t.Fatalf("wrong digest error = %v", err)
	}
	// Cancel while queued cancels both Message and Delivery.
	cancelled, err := database.CancelMailMessage(ctx, message.ID, "owner-1", now)
	if err != nil {
		t.Fatal(err)
	}
	storedDelivery, err := database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != domain.MailMessageCancelled || storedDelivery.State != domain.MailDeliveryCancelled {
		t.Fatalf("cancel = %+v delivery = %+v", cancelled, storedDelivery)
	}
}

func TestMailRejectAndRetry(t *testing.T) {
	ctx := context.Background()
	database, _, message, _, digest := newMailOutboxFixture(t)
	now := time.Now().UTC()
	if err := database.RejectMailMessage(ctx, message.ID, "recipient list wrong", now); err != nil {
		t.Fatal(err)
	}
	stored, err := database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MailMessageRejected || stored.RejectedReason != "recipient list wrong" {
		t.Fatalf("rejected = %+v", stored)
	}
	if stored.ApprovedBy != "" || !stored.ApprovedAt.IsZero() {
		t.Fatalf("rejection was recorded as an approval: %+v", stored)
	}

	// Build a failed generation, then retry it with the same snapshot.
	message2, err := database.CreateMailMessage(ctx, domain.MailMessage{
		TaskID: stored.TaskID, AccountID: stored.AccountID, Source: domain.MailMessageSourceOwner,
		IdempotencyKey: "retry-1", FromAddress: "noreply@example.com",
		To: []string{"qa@example.com"}, Subject: "retry", BodyText: "retry",
		Status: domain.MailMessageDraft,
	})
	if err != nil {
		t.Fatal(err)
	}
	message2, err = database.SubmitMailMessage(ctx, message2.ID, mailpkg.ApprovalDigest(message2, nil), now)
	if err != nil {
		t.Fatal(err)
	}
	digest2 := mailpkg.ApprovalDigest(message2, nil)
	message2, err = database.MailMessage(ctx, message2.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Recompute after Submit, because Submit stored the digest.
	digest2 = message2.ApprovalDigest
	failed, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message2.ID, Generation: 0, AccountID: message2.AccountID,
		State: domain.MailDeliveryFailed, AvailableAt: now, ApprovedDigest: digest2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := database.UpdateMailMessageStatus(ctx, message2.ID,
		[]domain.MailMessageStatus{domain.MailMessagePendingApproval},
		domain.MailMessageFailed, now); err != nil || !ok {
		t.Fatalf("mark failed: ok=%v err=%v", ok, err)
	}
	if _, err := database.RetryMailMessage(ctx, message2.ID, "sha256:wrong", "owner-1", now); !errors.Is(err, ErrMailDigestMismatch) {
		t.Fatalf("retry digest error = %v", err)
	}
	retried, err := database.RetryMailMessage(ctx, message2.ID, digest2, "owner-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Generation != failed.Generation+1 || retried.ReplayOfID != failed.ID ||
		retried.State != domain.MailDeliveryQueued || retried.ApprovedDigest != digest2 ||
		retried.AccountID != message2.AccountID || retried.MessageID != message2.ID {
		t.Fatalf("retried delivery = %+v", retried)
	}
	retriedMessage, err := database.MailMessage(ctx, message2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retriedMessage.AccountID != message2.AccountID || retriedMessage.FromAddress != message2.FromAddress ||
		strings.Join(retriedMessage.To, ",") != strings.Join(message2.To, ",") ||
		retriedMessage.ApprovalDigest != digest2 {
		t.Fatalf("retry changed the frozen snapshot: %+v", retriedMessage)
	}
	_ = digest
}

func TestMailClaimReclaimPreData(t *testing.T) {
	ctx := context.Background()
	database, _, message, _, digest := newMailOutboxFixture(t)
	now := time.Now().UTC()
	_, _, err := database.ApproveMailMessage(ctx, message.ID, digest, "owner-1", now)
	if err != nil {
		t.Fatal(err)
	}
	delivery, ok, err := database.ClaimMailDelivery(ctx, "", now, 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if delivery.State != domain.MailDeliverySending || delivery.Attempts != 1 ||
		delivery.LeaseID == "" || delivery.SMTPPhase != domain.MailSMTPPhaseConnect {
		t.Fatalf("claimed delivery = %+v", delivery)
	}
	storedMessage, err := database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedMessage.Status != domain.MailMessageSending {
		t.Fatalf("message status = %s", storedMessage.Status)
	}
	// Expire the lease while still before DATA.
	expired := now.Add(-time.Second)
	if _, err := database.db.ExecContext(ctx,
		`UPDATE mail_deliveries SET lease_until=? WHERE id=?`, mailTimeString(expired), delivery.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := database.ReclaimExpiredMailDeliveries(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed.Requeued) != 1 || reclaimed.Requeued[0] != delivery.ID {
		t.Fatalf("reclaim = %+v", reclaimed)
	}
	storedDelivery, err := database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedMessage, err = database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedDelivery.State != domain.MailDeliveryQueued || storedDelivery.LeaseID != "" ||
		storedDelivery.SMTPPhase != "" || storedDelivery.LastErrorCode != string(domain.MailErrSMTPLeaseExpiredBeforeData) {
		t.Fatalf("requeued delivery = %+v", storedDelivery)
	}
	if storedMessage.Status != domain.MailMessageQueued {
		t.Fatalf("message did not return to queued: %s", storedMessage.Status)
	}
}

func TestMailReclaimDataPhaseGoesDeadLetter(t *testing.T) {
	ctx := context.Background()
	database, _, message, _, digest := newMailOutboxFixture(t)
	now := time.Now().UTC()
	if _, _, err := database.ApproveMailMessage(ctx, message.ID, digest, "owner-1", now); err != nil {
		t.Fatal(err)
	}
	delivery, ok, err := database.ClaimMailDelivery(ctx, "", now, 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if ok, err := database.SetMailDeliveryPhase(ctx, delivery.ID, delivery.LeaseID, domain.MailSMTPPhaseData, now); err != nil || !ok {
		t.Fatalf("enter data: ok=%v err=%v", ok, err)
	}
	expired := now.Add(-time.Second)
	if _, err := database.db.ExecContext(ctx,
		`UPDATE mail_deliveries SET lease_until=? WHERE id=?`, mailTimeString(expired), delivery.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := database.ReclaimExpiredMailDeliveries(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed.DeadLettered) != 1 {
		t.Fatalf("reclaim = %+v", reclaimed)
	}
	storedDelivery, err := database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedMessage, err := database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedDelivery.State != domain.MailDeliveryDeadLetter ||
		storedDelivery.OutcomeCertainty != domain.MailOutcomeUnknown ||
		storedDelivery.LastErrorCode != string(domain.MailErrSMTPAmbiguousAfterData) {
		t.Fatalf("dead delivery = %+v", storedDelivery)
	}
	if storedMessage.Status != domain.MailMessageDeadLetter {
		t.Fatalf("message status = %s", storedMessage.Status)
	}
}

func TestMailSubmittedMessageCannotReturnToPending(t *testing.T) {
	ctx := context.Background()
	database, _, message, _, digest := newMailOutboxFixture(t)
	if _, err := database.SubmitMailMessage(ctx, message.ID, digest, time.Now().UTC()); !errors.Is(err, ErrMailInvalidState) {
		t.Fatalf("resubmit pending message error = %v", err)
	}
}

func TestMailCancelQueuedBackoff(t *testing.T) {
	ctx := context.Background()
	database, _, message, _, digest := newMailOutboxFixture(t)
	now := time.Now().UTC()
	_, delivery, err := database.ApproveMailMessage(ctx, message.ID, digest, "owner-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx,
		`UPDATE mail_deliveries SET available_at=? WHERE id=?`,
		mailTimeString(now.Add(time.Hour)), delivery.ID); err != nil {
		t.Fatal(err)
	}
	cancelled, err := database.CancelMailMessage(ctx, message.ID, "owner-1", now)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != domain.MailMessageCancelled || stored.State != domain.MailDeliveryCancelled {
		t.Fatalf("cancel backoff = %+v delivery = %+v", cancelled, stored)
	}
}

func TestMailReclaimRollsBackWhenMessageCannotFollow(t *testing.T) {
	ctx := context.Background()
	database, account, message, _, _ := newMailOutboxFixture(t)
	now := time.Now().UTC()
	if _, err := database.db.ExecContext(ctx,
		`UPDATE mail_messages SET status='sent' WHERE id=?`, message.ID); err != nil {
		t.Fatal(err)
	}
	delivery, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, State: domain.MailDeliverySending,
		LeaseID: "expired-lease", LeaseUntil: now.Add(-time.Second), AvailableAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReclaimExpiredMailDeliveries(ctx, now); err == nil {
		t.Fatal("reclaim accepted a Message that could not follow its Delivery")
	}
	stored, err := database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.MailDeliverySending {
		t.Fatalf("delivery changed despite rollback: %+v", stored)
	}
	storedMessage, err := database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedMessage.Status != domain.MailMessageSent {
		t.Fatalf("message changed despite rollback: %s", storedMessage.Status)
	}
	countAudit := func() int {
		var count int
		if err := database.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM audit_events WHERE action='mail.delivery.reclaim_failed' AND resource_id=?`,
			delivery.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if countAudit() != 1 {
		t.Fatalf("reclaim failure audit count = %d, want 1", countAudit())
	}
	// A repeated failed round must not grow the audit table.
	_, _ = database.ReclaimExpiredMailDeliveries(ctx, now)
	if countAudit() != 1 {
		t.Fatalf("reclaim failure audit repeated: %d", countAudit())
	}
}

func TestMailReclaimExhaustedAttempts(t *testing.T) {
	ctx := context.Background()
	database, account, message, _, _ := newMailOutboxFixture(t)
	now := time.Now().UTC()
	if _, err := database.db.ExecContext(ctx,
		`UPDATE mail_messages SET status='queued' WHERE id=?`, message.ID); err != nil {
		t.Fatal(err)
	}
	delivery, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, State: domain.MailDeliveryQueued,
		Attempts: domain.MailDeliveryMaxAttempts, AvailableAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	reclaimed, err := database.ReclaimExpiredMailDeliveries(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed.DeadLettered) != 1 || reclaimed.DeadLettered[0] != delivery.ID {
		t.Fatalf("reclaim = %+v", reclaimed)
	}
	stored, err := database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.MailDeliveryDeadLetter ||
		stored.LastErrorCode != string(domain.MailErrSMTPAttemptsExhausted) {
		t.Fatalf("delivery = %+v", stored)
	}
}

func TestMailReclaimUsesReclaimIndex(t *testing.T) {
	database := openMailTestStore(t)
	plan, err := database.ExplainMailDeliveryReclaim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_mail_deliveries_reclaim") {
		t.Fatalf("reclaim query plan does not use the reclaim index:\n%s", joined)
	}
}

func TestMailFinishRejectsStaleLease(t *testing.T) {
	ctx := context.Background()
	database, _, message, _, digest := newMailOutboxFixture(t)
	now := time.Now().UTC()
	if _, _, err := database.ApproveMailMessage(ctx, message.ID, digest, "owner-1", now); err != nil {
		t.Fatal(err)
	}
	delivery, ok, err := database.ClaimMailDelivery(ctx, "", now, 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	// A new worker takes the row.
	if _, err := database.db.ExecContext(ctx,
		`UPDATE mail_deliveries SET lease_id='new-lease' WHERE id=?`, delivery.ID); err != nil {
		t.Fatal(err)
	}
	ok, err = database.FinishMailDelivery(ctx, delivery, delivery.LeaseID, MailDeliveryResultPatch{
		State: domain.MailDeliverySent, Outcome: domain.MailAttemptSent,
		OutcomeCertainty: domain.MailOutcomeProviderAccepted,
		Phase:            domain.MailSMTPPhaseFinished,
	}, now)
	if err != nil || ok {
		t.Fatalf("stale finish: ok=%v err=%v", ok, err)
	}
	stored, err := database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedMessage, err := database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.MailDeliverySending || stored.LeaseID != "new-lease" {
		t.Fatalf("stale worker overwrote delivery: %+v", stored)
	}
	if storedMessage.Status != domain.MailMessageSending {
		t.Fatalf("stale worker overwrote message: %s", storedMessage.Status)
	}
}

func TestMailCancelSendingIsRejected(t *testing.T) {
	ctx := context.Background()
	database, _, message, _, digest := newMailOutboxFixture(t)
	now := time.Now().UTC()
	if _, _, err := database.ApproveMailMessage(ctx, message.ID, digest, "owner-1", now); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := database.ClaimMailDelivery(ctx, "", now, 5*time.Minute); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if _, err := database.CancelMailMessage(ctx, message.ID, "owner-1", now); !errors.Is(err, ErrMailInvalidState) {
		t.Fatalf("cancel sending error = %v", err)
	}
}

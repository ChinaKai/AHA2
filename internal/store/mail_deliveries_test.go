package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestMailDeliveryConditionalStateAndLeasePrimitives(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	account := createMailAccountFixture(t, database, "Delivery", "delivery@example.com")
	message := createMailMessageFixture(t, database, taskID, account.ID, "delivery-1")
	now := time.Now().UTC()

	delivery, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		ID: domain.NewID("mail_delivery"), MessageID: message.ID,
		AccountID: account.ID, State: domain.MailDeliveryQueued, AvailableAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	leaseID := "lease-1"
	leaseUntil := now.Add(5 * time.Minute)
	connect := domain.MailSMTPPhaseConnect
	claimed, err := database.UpdateMailDeliveryState(ctx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliveryQueued},
		domain.MailDeliverySending,
		MailDeliveryStatePatch{
			AttemptsDelta: 1, LeaseID: &leaseID, LeaseUntil: &leaseUntil, SMTPPhase: &connect,
		}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("queued -> sending claim was not applied")
	}
	stored, err := database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.MailDeliverySending || stored.Attempts != 1 ||
		stored.LeaseID != leaseID || stored.LeaseUntil.IsZero() ||
		stored.SMTPPhase != domain.MailSMTPPhaseConnect {
		t.Fatalf("claimed delivery = %+v", stored)
	}

	// A second claimer that still expects queued must lose. It supplies its own
	// lease so the failure is the conditional UPDATE, not input validation.
	staleLease := "lease-2"
	staleUntil := now.Add(5 * time.Minute)
	claimed, err = database.UpdateMailDeliveryState(ctx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliveryQueued},
		domain.MailDeliverySending,
		MailDeliveryStatePatch{AttemptsDelta: 1, LeaseID: &staleLease, LeaseUntil: &staleUntil}, now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("stale queued claim overwrote a sending delivery")
	}

	// Degenerate writes must be rejected instead of turning into an
	// unconditional UPDATE or a lease-less sending write.
	if _, err := database.UpdateMailDeliveryState(ctx, delivery.ID, nil,
		domain.MailDeliverySending, MailDeliveryStatePatch{LeaseID: &staleLease, LeaseUntil: &staleUntil}, now); err == nil {
		t.Fatal("empty expected-state list was accepted")
	}
	if _, err := database.UpdateMailDeliveryState(ctx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliveryQueued},
		domain.MailDeliverySending, MailDeliveryStatePatch{AttemptsDelta: 1}, now); err == nil {
		t.Fatal("entering sending without a lease was accepted")
	}
	if _, err := database.UpdateMailDeliveryState(ctx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliverySending},
		domain.MailDeliveryQueued, MailDeliveryStatePatch{}, now); err == nil {
		t.Fatal("writing from sending without a lease guard was accepted")
	}

	dataPhase := domain.MailSMTPPhaseData
	if ok, err := database.SetMailDeliveryPhaseInTx(ctx, database.db, delivery.ID,
		"wrong-lease", dataPhase, now); err != nil || ok {
		t.Fatalf("phase with wrong lease: ok=%v err=%v", ok, err)
	}
	if ok, err := database.SetMailDeliveryPhaseInTx(ctx, database.db, delivery.ID,
		leaseID, dataPhase, now); err != nil || !ok {
		t.Fatalf("phase with correct lease: ok=%v err=%v", ok, err)
	}
	stored, err = database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SMTPPhase != domain.MailSMTPPhaseData {
		t.Fatalf("smtp phase = %q, want data", stored.SMTPPhase)
	}

	// Once DATA is recorded the phase is monotonic: a stale worker cannot move
	// it back to connect or empty, either through the phase helper or a state
	// patch.
	connectPhase := domain.MailSMTPPhaseConnect
	emptyPhase := domain.MailSMTPPhaseNone
	if ok, err := database.SetMailDeliveryPhaseInTx(ctx, database.db, delivery.ID,
		leaseID, connectPhase, now); err != nil || ok {
		t.Fatalf("phase regression to connect: ok=%v err=%v", ok, err)
	}
	if ok, err := database.SetMailDeliveryPhaseInTx(ctx, database.db, delivery.ID,
		leaseID, emptyPhase, now); err != nil || ok {
		t.Fatalf("phase regression to empty: ok=%v err=%v", ok, err)
	}
	if ok, err := database.UpdateMailDeliveryState(ctx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliverySending},
		domain.MailDeliverySending,
		MailDeliveryStatePatch{
			SMTPPhase: &connectPhase, LeaseID: &leaseID, LeaseUntil: &leaseUntil,
			RequireLeaseID: leaseID,
		}, now); err != nil || ok {
		t.Fatalf("phase patch regression to connect: ok=%v err=%v", ok, err)
	}
	stored, err = database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SMTPPhase != domain.MailSMTPPhaseData {
		t.Fatalf("smtp phase after regression attempts = %q, want data", stored.SMTPPhase)
	}

	extended := now.Add(10 * time.Minute)
	if ok, err := database.RenewMailDeliveryLeaseInTx(ctx, database.db, delivery.ID,
		leaseID, extended, now); err != nil || !ok {
		t.Fatalf("renew lease: ok=%v err=%v", ok, err)
	}
	if ok, err := database.RenewMailDeliveryLeaseInTx(ctx, database.db, delivery.ID,
		"stale-lease", extended, now); err != nil || ok {
		t.Fatalf("renew with stale lease: ok=%v err=%v", ok, err)
	}

	accepted := domain.MailOutcomeProviderAccepted
	finished := domain.MailSMTPPhaseFinished
	header := "<mail-message@example.com>"
	response := "250 accepted"
	sentAt := now.Add(time.Second)
	final := MailDeliveryStatePatch{
		SMTPPhase: &finished, OutcomeCertainty: &accepted,
		MessageIDHeader: &header, SMTPResponse: &response, SentAt: &sentAt,
		RequireLeaseID: leaseID,
	}
	if ok, err := database.UpdateMailDeliveryState(ctx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliverySending},
		domain.MailDeliverySent, final, sentAt); err != nil || !ok {
		t.Fatalf("final write with lease: ok=%v err=%v", ok, err)
	}
	stored, err = database.MailDelivery(ctx, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.MailDeliverySent ||
		stored.OutcomeCertainty != domain.MailOutcomeProviderAccepted ||
		stored.MessageIDHeader != header || stored.SMTPResponse != response ||
		stored.SentAt.IsZero() || stored.LeaseID != "" || !stored.LeaseUntil.IsZero() {
		t.Fatalf("final delivery = %+v", stored)
	}
	if ok, err := database.UpdateMailDeliveryState(ctx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliverySent},
		domain.MailDeliveryFailed, MailDeliveryStatePatch{RequireLeaseID: leaseID}, now); err != nil || ok {
		t.Fatalf("final write with stale lease: ok=%v err=%v", ok, err)
	}

	attempt, err := database.CreateMailDeliveryAttempt(ctx, domain.MailDeliveryAttempt{
		DeliveryID: delivery.ID, AttemptNo: 1, LeaseID: leaseID,
		Outcome: domain.MailAttemptTransientFailure, ErrorCode: string(domain.MailErrSMTPTemporaryFailure),
		SMTPCode: 451, BackoffMS: 5000, StartedAt: now, FinishedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateMailDeliveryAttempt(ctx, domain.MailDeliveryAttempt{
		DeliveryID: delivery.ID, AttemptNo: 1, LeaseID: leaseID,
		Outcome: domain.MailAttemptSent, StartedAt: now,
	}); err == nil {
		t.Fatal("duplicate delivery/attempt_no passed the unique constraint")
	}
	if _, err := database.CreateMailDeliveryAttempt(ctx, domain.MailDeliveryAttempt{
		ID: domain.NewID("mail_delivery_attempt"), DeliveryID: delivery.ID,
		AttemptNo: 2, LeaseID: leaseID, Outcome: domain.MailAttemptSent,
		StartedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	attempts, err := database.MailDeliveryAttempts(ctx, delivery.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].AttemptNo != 2 || attempts[1].AttemptNo != 1 {
		t.Fatalf("attempts = %+v", attempts)
	}
	if attempts[1].ID != attempt.ID || attempts[1].SMTPCode != 451 ||
		attempts[1].BackoffMS != 5000 || attempts[1].Outcome != domain.MailAttemptTransientFailure {
		t.Fatalf("attempt snapshot = %+v", attempts[1])
	}

	if _, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		ID: domain.NewID("mail_delivery"), MessageID: message.ID,
		AccountID: account.ID, Generation: 0,
	}); err == nil {
		t.Fatal("duplicate message/generation passed the unique constraint")
	}
	replay, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		ID: domain.NewID("mail_delivery"), MessageID: message.ID,
		AccountID: account.ID, Generation: 1, ReplayOfID: delivery.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	storedReplay, err := database.MailDelivery(ctx, replay.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedReplay.ReplayOfID != delivery.ID || storedReplay.Generation != 1 {
		t.Fatalf("replay delivery = %+v", storedReplay)
	}
}

func TestMailDeliveryPhaseRejectsExpiredLease(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	account := createMailAccountFixture(t, database, "Expired", "expired@example.com")
	message := createMailMessageFixture(t, database, taskID, account.ID, "expired-1")
	now := time.Now().UTC()

	delivery, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, State: domain.MailDeliveryQueued,
		AvailableAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	leaseID := "lease-expired"
	expired := now.Add(-time.Minute)
	if ok, err := database.UpdateMailDeliveryState(ctx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliveryQueued},
		domain.MailDeliverySending,
		MailDeliveryStatePatch{AttemptsDelta: 1, LeaseID: &leaseID, LeaseUntil: &expired}, now); err != nil || !ok {
		t.Fatalf("prepare sending delivery: ok=%v err=%v", ok, err)
	}
	dataPhase := domain.MailSMTPPhaseData
	if ok, err := database.SetMailDeliveryPhaseInTx(ctx, database.db, delivery.ID,
		leaseID, dataPhase, now); err != nil || ok {
		t.Fatalf("expired lease allowed entering DATA: ok=%v err=%v", ok, err)
	}
}

func TestMailDeliveryActiveMessageUniqueness(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	account := createMailAccountFixture(t, database, "Active", "active@example.com")
	message := createMailMessageFixture(t, database, taskID, account.ID, "active-1")
	now := time.Now().UTC()

	if _, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, State: domain.MailDeliverySending,
	}); err == nil {
		t.Fatal("created a sending delivery without a lease")
	}
	if _, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, State: domain.MailDeliveryQueued,
		LeaseID: "queued-lease",
	}); err == nil {
		t.Fatal("created a queued delivery with a lease")
	}
	first, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, Generation: 0,
		State: domain.MailDeliveryQueued, AvailableAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, Generation: 1,
		State: domain.MailDeliveryQueued, AvailableAt: now,
	}); err == nil {
		t.Fatal("second active generation passed idx_mail_deliveries_active_message")
	}

	ok, err := database.UpdateMailDeliveryState(ctx, first.ID,
		[]domain.MailDeliveryState{domain.MailDeliveryQueued},
		domain.MailDeliveryCancelled, MailDeliveryStatePatch{}, now)
	if err != nil || !ok {
		t.Fatalf("cancel first generation: ok=%v err=%v", ok, err)
	}
	second, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, Generation: 1,
		State: domain.MailDeliveryQueued, AvailableAt: now,
	})
	if err != nil {
		t.Fatalf("new generation after terminal state: %v", err)
	}
	if _, err := database.CreateMailDelivery(ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: account.ID, Generation: 2,
		State: domain.MailDeliveryQueued, AvailableAt: now,
	}); err == nil {
		t.Fatal("third active generation passed idx_mail_deliveries_active_message")
	}
	storedFirst, err := database.MailDelivery(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedFirst.State != domain.MailDeliveryCancelled || storedFirst.LeaseID != "" || !storedFirst.LeaseUntil.IsZero() {
		t.Fatalf("cancelled delivery = %+v", storedFirst)
	}
	if storedSecond, err := database.MailDelivery(ctx, second.ID); err != nil || storedSecond.State != domain.MailDeliveryQueued {
		t.Fatalf("second generation = %+v, err=%v", storedSecond, err)
	}
}

func TestMailDeliveryTransactionCompositionAndRollback(t *testing.T) {
	ctx := context.Background()
	database := openMailTestStore(t)
	taskID, _, _ := createMailTaskFixture(t, database)
	account := createMailAccountFixture(t, database, "Tx", "tx@example.com")
	message, err := database.CreateMailMessage(ctx, domain.MailMessage{
		TaskID: taskID, AccountID: account.ID, Source: domain.MailMessageSourceAgent,
		IdempotencyKey: "tx-1", FromAddress: "noreply@example.com",
		To: []string{"qa@example.com"}, Subject: "Tx", Status: domain.MailMessageQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	deliveryID := domain.NewID("mail_delivery")
	err = database.WithMailTx(ctx, func(tx *sql.Tx) error {
		if _, err := database.CreateMailDeliveryInTx(ctx, tx, domain.MailDelivery{
			ID: deliveryID, MessageID: message.ID, AccountID: account.ID,
			State: domain.MailDeliveryQueued, AvailableAt: now,
		}); err != nil {
			return err
		}
		ok, err := database.UpdateMailMessageStatusInTx(ctx, tx, message.ID,
			[]domain.MailMessageStatus{domain.MailMessageQueued},
			domain.MailMessageSending, now)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("message was not queued")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.MailDelivery(ctx, deliveryID); err != nil {
		t.Fatal(err)
	}
	storedMessage, err := database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedMessage.Status != domain.MailMessageSending {
		t.Fatalf("message status = %q, want sending", storedMessage.Status)
	}

	// The rollback case needs its own message: the unique active-delivery index
	// permits only one queued/sending generation per message.
	rollbackMessage, err := database.CreateMailMessage(ctx, domain.MailMessage{
		TaskID: taskID, AccountID: account.ID, Source: domain.MailMessageSourceAgent,
		IdempotencyKey: "tx-2", FromAddress: "noreply@example.com",
		To: []string{"qa@example.com"}, Subject: "Tx rollback", Status: domain.MailMessageQueued,
	})
	if err != nil {
		t.Fatal(err)
	}
	rollbackID := domain.NewID("mail_delivery")
	abort := errors.New("abort transaction")
	err = database.WithMailTx(ctx, func(tx *sql.Tx) error {
		if _, err := database.CreateMailDeliveryInTx(ctx, tx, domain.MailDelivery{
			ID: rollbackID, MessageID: rollbackMessage.ID, AccountID: account.ID,
			State: domain.MailDeliveryQueued, AvailableAt: now,
		}); err != nil {
			return err
		}
		if _, err := database.UpdateMailMessageStatusInTx(ctx, tx, rollbackMessage.ID,
			[]domain.MailMessageStatus{domain.MailMessageQueued},
			domain.MailMessageSending, now); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("transaction error = %v, want abort", err)
	}
	if _, err := database.MailDelivery(ctx, rollbackID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rolled back delivery is still visible: %v", err)
	}
	storedRollback, err := database.MailMessage(ctx, rollbackMessage.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRollback.Status != domain.MailMessageQueued {
		t.Fatalf("message status after rollback = %q, want queued", storedRollback.Status)
	}
}

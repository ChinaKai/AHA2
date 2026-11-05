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

	// A second claimer that still expects queued must lose.
	claimed, err = database.UpdateMailDeliveryState(ctx, delivery.ID,
		[]domain.MailDeliveryState{domain.MailDeliveryQueued},
		domain.MailDeliverySending, MailDeliveryStatePatch{AttemptsDelta: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("stale queued claim overwrote a sending delivery")
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
	clear := ""
	header := "<mail-message@example.com>"
	response := "250 accepted"
	sentAt := now.Add(time.Second)
	final := MailDeliveryStatePatch{
		LeaseID: &clear, SMTPPhase: &finished, OutcomeCertainty: &accepted,
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
		stored.SentAt.IsZero() || stored.LeaseID != "" {
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

	rollbackID := domain.NewID("mail_delivery")
	abort := errors.New("abort transaction")
	err = database.WithMailTx(ctx, func(tx *sql.Tx) error {
		if _, err := database.CreateMailDeliveryInTx(ctx, tx, domain.MailDelivery{
			ID: rollbackID, MessageID: message.ID, AccountID: account.ID,
			Generation: 1, State: domain.MailDeliveryQueued, AvailableAt: now,
		}); err != nil {
			return err
		}
		if _, err := database.UpdateMailMessageStatusInTx(ctx, tx, message.ID,
			[]domain.MailMessageStatus{domain.MailMessageSending},
			domain.MailMessageQueued, now); err != nil {
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
	storedMessage, err = database.MailMessage(ctx, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedMessage.Status != domain.MailMessageSending {
		t.Fatalf("message status after rollback = %q, want sending", storedMessage.Status)
	}
}

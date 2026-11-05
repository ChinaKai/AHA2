package mailworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	mailpkg "github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/mail/mailtest"
	"github.com/ChinaKai/AHA2/internal/store"
)

type workerFixture struct {
	t       *testing.T
	ctx     context.Context
	dbPath  string
	db      *store.Store
	raw     *sql.DB
	secrets map[string]string
	task    domain.Task
	account domain.MailAccount
	logs    *bytes.Buffer
	logger  *slog.Logger
}

func newWorkerFixture(t *testing.T) *workerFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	task := createWorkerTask(t, database)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	accountID := domain.NewID("mail_account")
	account, err := database.CreateMailAccount(ctx, domain.MailAccount{
		ID: accountID, Name: "Worker", Address: "noreply@example.com", DisplayName: "AHA",
		Provider: domain.MailProviderNeteaseQiye, SMTPHost: domain.MailDefaultSMTPHost,
		SMTPPort: domain.MailDefaultSMTPPort, SMTPSecurity: domain.MailSMTPSecuritySSL,
		SMTPUsername:  "noreply@example.com",
		CredentialRef: domain.MailAccountCredentialRef(accountID), CredentialConfigured: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &workerFixture{
		t: t, ctx: ctx, dbPath: path, db: database, raw: raw, logger: logger, logs: logs,
		secrets: map[string]string{domain.MailAccountCredentialRef(accountID): "AUTH-CANARY"},
		task:    task, account: account,
	}
}

func createWorkerTask(t *testing.T, database *store.Store) domain.Task {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	suffix := domain.NewID("worker")
	project := domain.Project{ID: "project_" + suffix, Name: "Mail project", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		ID: "workspace_" + suffix, ProjectID: project.ID, Name: "Mail workspace",
		Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	provider := domain.Provider{ID: "provider_" + suffix, Name: "Mail provider", BaseURL: "https://example.test/v1", CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	group := domain.EnvGroup{
		ID: "env_" + suffix, Name: "Mail env", ProviderID: provider.ID, Backend: "codex",
		Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertEnvGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	model := domain.Model{
		ID: "model_" + suffix, DisplayName: "Mail model", ProviderID: provider.ID, Backend: "codex",
		WireModel: "model", DefaultEnvGroupID: group.ID, Capabilities: map[string]any{},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeConfigSnapshot{
		ID: "snapshot_" + suffix, WorkspaceID: workspace.ID, Backend: "codex",
		ModelID: model.ID, WireModel: model.WireModel, EnvGroupID: group.ID,
		EnvGroupRevision: 1, PermissionsJSON: "{}", CreatedAt: now,
	}
	if err := database.CreateRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{
		ID: "task_" + suffix, ProjectID: project.ID, WorkspaceID: workspace.ID,
		Title: "Mail task", OriginalRequest: "test", CurrentGoal: "test",
		Status: domain.TaskActive, RuntimeConfigSnapshotID: snapshot.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	return task
}

func workerTestSender(server *mailtest.Server) *mailpkg.SMTPSender {
	sender := mailpkg.NewSMTPSender()
	sender.ConnectTimeout = 2 * time.Second
	sender.TLSTimeout = 2 * time.Second
	sender.AuthTimeout = 2 * time.Second
	sender.CommandTimeout = 2 * time.Second
	sender.DataTimeout = 2 * time.Second
	sender.TotalTimeout = 8 * time.Second
	sender.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Addr)
	}
	sender.TLSConfig = func(host string) *tls.Config {
		return &tls.Config{ServerName: host, RootCAs: server.RootCAs, MinVersion: tls.VersionTLS12}
	}
	return sender
}

func (f *workerFixture) newWorker(server *mailtest.Server, baseURL string) *Worker {
	return f.newWorkerWithSender(workerTestSender(server), baseURL)
}

func (f *workerFixture) newWorkerWithSender(sender Sender, baseURL string) *Worker {
	return New(Config{
		Store: f.db, Secrets: fakeSecrets(f.secrets), Sender: sender,
		Logger: f.logger, Interval: time.Hour, LeaseDuration: 5 * time.Minute,
		RenewInterval: time.Minute, DownloadBaseURL: baseURL,
		Jitter: func(d time.Duration) time.Duration { return d },
	})
}

type fakeSecrets map[string]string

func (values fakeSecrets) Get(key string) (string, bool) {
	value, ok := values[key]
	return value, ok
}

type queuedMessage struct {
	message    domain.MailMessage
	attachment domain.MailAttachment
	delivery   domain.MailDelivery
	digest     string
}

func (f *workerFixture) createQueuedMessage(subject, body string, link bool, corruptHash bool) queuedMessage {
	f.t.Helper()
	ctx := f.ctx
	now := time.Now().UTC()
	source, err := f.db.CreateAttachment(ctx, f.task.ID, "report.txt", "text/plain", []byte("attachment-bytes"), now)
	if err != nil {
		f.t.Fatal(err)
	}
	attachmentID := domain.NewID("mail_attachment")
	if link {
		body = strings.ReplaceAll(body, "{{link}}", "%%MAIL_LINK:"+attachmentID+"%%")
	}
	message, err := f.db.CreateMailMessage(ctx, domain.MailMessage{
		ID: domain.NewID("mail_message"), TaskID: f.task.ID, ProjectID: f.task.ProjectID,
		WorkspaceID: f.task.WorkspaceID, AccountID: f.account.ID,
		Source: domain.MailMessageSourceOwner, IdempotencyKey: domain.NewID("key"),
		FromAddress: f.account.Address, FromName: "AHA", To: []string{"qa-team@example.com"},
		Subject: subject, BodyText: body, Status: domain.MailMessageDraft,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	attachment := domain.MailAttachment{
		ID: attachmentID, MessageID: message.ID, Position: 0,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: source.ID,
		FileName: source.Name, MediaType: source.MediaType,
		SizeBytes: source.Size, SHA256: source.SHA256,
		DeliveryMode: domain.MailAttachmentDirect,
	}
	if corruptHash {
		attachment.SHA256 = strings.Repeat("0", 64)
	}
	if link {
		attachment.DeliveryMode = domain.MailAttachmentLink
		attachment.LinkTokenRef = domain.MailAttachmentLinkTokenRef(attachment.ID)
		attachment.LinkTokenHash = tokenHash("TOKEN-CANARY")
		attachment.LinkExpiresAt = now.Add(7 * 24 * time.Hour)
		attachment.LinkMaxDownloads = 20
		f.secrets[attachment.LinkTokenRef] = "TOKEN-CANARY"
	}
	created, err := f.db.CreateMailAttachment(ctx, attachment)
	if err != nil {
		f.t.Fatal(err)
	}
	message, err = f.db.MailMessage(ctx, message.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	digest := mailpkg.ApprovalDigest(message, []domain.MailAttachment{created})
	message, err = f.db.SubmitMailMessage(ctx, message.ID, digest, now)
	if err != nil {
		f.t.Fatal(err)
	}
	message, delivery, err := f.db.ApproveMailMessage(ctx, message.ID, digest, "owner-1", now)
	if err != nil {
		f.t.Fatal(err)
	}
	return queuedMessage{message: message, attachment: created, delivery: delivery, digest: digest}
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// messageText decodes the first text/plain part of a captured SMTP message.
func messageText(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	readPart := func(reader io.Reader, header map[string][]string) string {
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if strings.EqualFold(strings.TrimSpace(joined(header["Content-Transfer-Encoding"])), "base64") {
			decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(data), "\r\n", ""))
			if err != nil {
				t.Fatal(err)
			}
			return string(decoded)
		}
		return string(data)
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(parsed.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(part.Header.Get("Content-Type"), "text/plain") {
				return readPart(part, part.Header)
			}
		}
	}
	return readPart(parsed.Body, parsed.Header)
}

func joined(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func findDelivery(t *testing.T, database *store.Store, id string) domain.MailDelivery {
	t.Helper()
	delivery, err := database.MailDelivery(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return delivery
}

func findMessage(t *testing.T, database *store.Store, id string) domain.MailMessage {
	t.Helper()
	message, err := database.MailMessage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func findAccount(t *testing.T, database *store.Store, id string) domain.MailAccount {
	t.Helper()
	account, err := database.MailAccount(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func (f *workerFixture) exec(query string, args ...any) error {
	_, err := f.raw.ExecContext(f.ctx, query, args...)
	return err
}

func TestBackoffScheduleIsLocal(t *testing.T) {
	identity := func(d time.Duration) time.Duration { return d }
	want := []time.Duration{
		time.Second, 5 * time.Second, 30 * time.Second,
		2 * time.Minute, 10 * time.Minute, time.Hour, time.Hour,
	}
	for index, duration := range want {
		if got := BackoffForFailedAttempt(index+1, identity); got != duration {
			t.Fatalf("attempt %d backoff = %s, want %s", index+1, got, duration)
		}
	}
	if got := BackoffForFailedAttempt(8, identity); got != 0 {
		t.Fatalf("attempt 8 backoff = %s, want 0", got)
	}
}

func TestWorkerSendsQueuedDeliveryForTerminalTask(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("terminal", "body", false, false)
	if err := f.exec(`UPDATE tasks SET status='completed' WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	worker := f.newWorker(server, "")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	if findDelivery(t, f.db, queued.delivery.ID).State != domain.MailDeliverySent {
		t.Fatalf("queued delivery did not continue after the Task became terminal")
	}
}

func TestWorkerSendsQueuedDelivery(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("Build ready", "The build is ready", false, false)
	worker := f.newWorker(server, "")
	processed, err := worker.ProcessOnce(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 || server.DataSeen() != 1 {
		t.Fatalf("processed=%d data=%d", processed, server.DataSeen())
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	message := findMessage(t, f.db, queued.message.ID)
	if delivery.State != domain.MailDeliverySent || delivery.OutcomeCertainty != domain.MailOutcomeProviderAccepted ||
		delivery.MessageIDHeader == "" || delivery.SentAt.IsZero() {
		t.Fatalf("delivery = %+v", delivery)
	}
	if message.Status != domain.MailMessageSent {
		t.Fatalf("message = %+v", message)
	}
	attempts, err := f.db.MailDeliveryAttempts(f.ctx, delivery.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != domain.MailAttemptSent || attempts[0].AttemptNo != 1 ||
		attempts[0].SMTPCode != 250 {
		t.Fatalf("attempts = %+v", attempts)
	}
	messages := server.Messages()
	if len(messages) != 1 || !strings.Contains(messageText(t, messages[0]), "The build is ready") {
		t.Fatalf("smtp message = %#v", messages)
	}
}

func TestWorkerDoesNotSendWithoutApproval(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	message, err := f.db.CreateMailMessage(f.ctx, domain.MailMessage{
		TaskID: f.task.ID, AccountID: f.account.ID, Source: domain.MailMessageSourceAgent,
		IdempotencyKey: "pending-1", FromAddress: f.account.Address,
		To: []string{"qa@example.com"}, Subject: "pending", BodyText: "pending",
		Status: domain.MailMessagePendingApproval, ContentRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker := f.newWorker(server, "")
	processed, err := worker.ProcessOnce(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 || server.Connections() != 0 || server.DataSeen() != 0 {
		t.Fatalf("unapproved message reached SMTP: processed=%d connections=%d", processed, server.Connections())
	}
	deliveries, err := f.db.MailDeliveriesByMessage(f.ctx, message.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 0 {
		t.Fatalf("pending message has deliveries: %+v", deliveries)
	}
}

func TestWorkerRejectsDeliveryForUnapprovedMessage(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	message, err := f.db.CreateMailMessage(f.ctx, domain.MailMessage{
		TaskID: f.task.ID, AccountID: f.account.ID, Source: domain.MailMessageSourceAgent,
		IdempotencyKey: "pending-delivery", FromAddress: f.account.Address,
		To: []string{"qa@example.com"}, Subject: "pending", BodyText: "pending",
		Status: domain.MailMessagePendingApproval, ContentRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a rogue/stale outbox row that was not created by approval.
	delivery, err := f.db.CreateMailDelivery(f.ctx, domain.MailDelivery{
		MessageID: message.ID, AccountID: f.account.ID, State: domain.MailDeliveryQueued,
		AvailableAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	worker := f.newWorker(server, "")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	if server.Connections() != 0 || server.DataSeen() != 0 {
		t.Fatal("unapproved message reached SMTP")
	}
	stored := findDelivery(t, f.db, delivery.ID)
	if stored.State != domain.MailDeliveryQueued || stored.Attempts != 0 {
		t.Fatalf("rogue delivery was not rolled back: %+v", stored)
	}
	if findMessage(t, f.db, message.ID).Status != domain.MailMessagePendingApproval {
		t.Fatalf("message status = %s", findMessage(t, f.db, message.ID).Status)
	}
	countClaimAudit := func() int {
		var count int
		if err := f.raw.QueryRowContext(f.ctx,
			`SELECT COUNT(*) FROM audit_events WHERE action='mail.delivery.claim_rejected' AND resource_id=?`,
			delivery.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if countClaimAudit() != 1 {
		t.Fatalf("claim-rejected audit count = %d, want 1", countClaimAudit())
	}
	// A second identical round must be deduped, not append another audit row.
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	if countClaimAudit() != 1 || server.Connections() != 0 {
		t.Fatalf("repeated claim audit grew: count=%d connections=%d", countClaimAudit(), server.Connections())
	}
}

func TestWorkerDigestMismatchFailsBeforeSMTP(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("Build ready", "body", false, false)
	if err := f.exec(`UPDATE mail_messages SET subject='tampered' WHERE id=?`, queued.message.ID); err != nil {
		t.Fatal(err)
	}
	worker := f.newWorker(server, "")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	if server.Connections() != 0 || server.DataSeen() != 0 {
		t.Fatal("digest mismatch reached SMTP")
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	if delivery.State != domain.MailDeliveryFailed || delivery.LastErrorCode != string(domain.MailErrApprovalDigestMismatch) {
		t.Fatalf("delivery = %+v", delivery)
	}
	if findMessage(t, f.db, queued.message.ID).Status != domain.MailMessageFailed {
		t.Fatalf("message status = %s", findMessage(t, f.db, queued.message.ID).Status)
	}
}

func TestWorkerDisabledAccountDoesNotClaim(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("disabled", "body", false, false)
	if err := f.db.DisableMailAccount(f.ctx, f.account.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	worker := f.newWorker(server, "")
	processed, err := worker.ProcessOnce(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 || server.Connections() != 0 {
		t.Fatalf("disabled account sent mail: processed=%d connections=%d", processed, server.Connections())
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	if delivery.State != domain.MailDeliveryQueued || delivery.Attempts != 0 {
		t.Fatalf("delivery = %+v", delivery)
	}
}

func TestWorkerAccountRotationBetweenClaimAndSendDefers(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("rotate", "body", false, false)
	worker := f.newWorker(server, "")
	now := time.Now().UTC()
	baselineAccount := findAccount(t, f.db, f.account.ID)
	baseline := accountSnapshot{
		Revision: baselineAccount.Revision, CredentialRef: domain.MailAccountCredentialRef(f.account.ID),
		SMTPHost: baselineAccount.SMTPHost, SMTPUsername: baselineAccount.SMTPUsername,
	}
	delivery, ok, err := f.db.ClaimMailDelivery(f.ctx, f.account.ID, now, 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if delivery.ID != queued.delivery.ID {
		t.Fatalf("claimed %s", delivery.ID)
	}
	if err := f.db.SetMailAccountCredential(f.ctx, f.account.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	worker.sendDelivery(f.ctx, delivery, baseline)
	if server.Connections() != 0 || server.DataSeen() != 0 {
		t.Fatal("rotated account reached SMTP")
	}
	stored := findDelivery(t, f.db, queued.delivery.ID)
	if stored.State != domain.MailDeliveryQueued || stored.Attempts != 0 {
		t.Fatalf("delivery after rotation = %+v", stored)
	}
	if stored.LastErrorCode != string(domain.MailErrAccountChanged) {
		t.Fatalf("last error = %q", stored.LastErrorCode)
	}
}

func TestWorkerLeaseLostBeforeDataDoesNotConnect(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("lease", "body", false, false)
	worker := f.newWorker(server, "")
	baselineAccount := findAccount(t, f.db, f.account.ID)
	baseline := accountSnapshot{
		Revision: baselineAccount.Revision, CredentialRef: domain.MailAccountCredentialRef(f.account.ID),
		SMTPHost: baselineAccount.SMTPHost, SMTPUsername: baselineAccount.SMTPUsername,
	}
	delivery, ok, err := f.db.ClaimMailDelivery(f.ctx, f.account.ID, time.Now().UTC(), 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	// Another owner takes the lease before the worker begins SMTP.
	if err := f.exec(`UPDATE mail_deliveries SET lease_id='new-lease' WHERE id=?`, delivery.ID); err != nil {
		t.Fatal(err)
	}
	worker.sendDelivery(f.ctx, delivery, baseline)
	if server.Connections() != 0 || server.DataSeen() != 0 {
		t.Fatal("worker connected after losing the delivery lease")
	}
	stored := findDelivery(t, f.db, queued.delivery.ID)
	if stored.LeaseID != "new-lease" || stored.State != domain.MailDeliverySending {
		t.Fatalf("stale worker changed delivery: %+v", stored)
	}
}

func TestWorkerTransientFailureSchedulesBackoff(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{RcptReplies: func(string) string { return "450 mailbox busy" }})
	queued := f.createQueuedMessage("retry", "body", false, false)
	worker := f.newWorker(server, "")
	started := time.Now()
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	if server.DataSeen() != 0 {
		t.Fatal("DATA was sent after RCPT 450")
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	if delivery.State != domain.MailDeliveryQueued || delivery.Attempts != 1 {
		t.Fatalf("delivery = %+v", delivery)
	}
	if delivery.AvailableAt.Before(started) || delivery.AvailableAt.Sub(started) < time.Second {
		t.Fatalf("available_at = %s, started = %s", delivery.AvailableAt, started)
	}
	if findMessage(t, f.db, queued.message.ID).Status != domain.MailMessageQueued {
		t.Fatalf("message status = %s", findMessage(t, f.db, queued.message.ID).Status)
	}
	attempts, err := f.db.MailDeliveryAttempts(f.ctx, delivery.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != domain.MailAttemptTransientFailure ||
		attempts[0].BackoffMS < 1000 || attempts[0].SMTPCode != 450 {
		t.Fatalf("attempts = %+v", attempts)
	}
}

func TestWorkerExhaustedAttemptsDeadLetter(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{RcptReplies: func(string) string { return "450 busy" }})
	queued := f.createQueuedMessage("exhausted", "body", false, false)
	if err := f.exec(`UPDATE mail_deliveries SET attempts=? WHERE id=?`, domain.MailDeliveryMaxAttempts-1, queued.delivery.ID); err != nil {
		t.Fatal(err)
	}
	worker := f.newWorker(server, "")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	if delivery.State != domain.MailDeliveryDeadLetter || delivery.Attempts != domain.MailDeliveryMaxAttempts ||
		delivery.LastErrorCode != string(domain.MailErrSMTPAttemptsExhausted) {
		t.Fatalf("delivery = %+v", delivery)
	}
	if findMessage(t, f.db, queued.message.ID).Status != domain.MailMessageDeadLetter {
		t.Fatalf("message status = %s", findMessage(t, f.db, queued.message.ID).Status)
	}
}

func TestWorkerDataDisconnectDeadLetterUnknown(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{CloseAfterData: true})
	queued := f.createQueuedMessage("ambiguous", "body", false, false)
	worker := f.newWorker(server, "")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	if delivery.State != domain.MailDeliveryDeadLetter ||
		delivery.OutcomeCertainty != domain.MailOutcomeUnknown ||
		delivery.LastErrorCode != string(domain.MailErrSMTPAmbiguousAfterData) {
		t.Fatalf("delivery = %+v", delivery)
	}
	if findMessage(t, f.db, queued.message.ID).Status != domain.MailMessageDeadLetter {
		t.Fatalf("message status = %s", findMessage(t, f.db, queued.message.ID).Status)
	}
}

func TestWorkerAuthFailureMarksAccountError(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{AuthMode: "fail"})
	queued := f.createQueuedMessage("auth", "body", false, false)
	worker := f.newWorker(server, "")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	if delivery.State != domain.MailDeliveryFailed || delivery.LastErrorCode != string(domain.MailErrSMTPAuthFailed) {
		t.Fatalf("delivery = %+v", delivery)
	}
	if findAccount(t, f.db, f.account.ID).Status != domain.MailAccountError {
		t.Fatalf("account status = %s", findAccount(t, f.db, f.account.ID).Status)
	}
	if server.DataSeen() != 0 {
		t.Fatal("DATA was sent after auth failure")
	}
	second := f.createQueuedMessage("auth-2", "body", false, false)
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	if stored := findDelivery(t, f.db, second.delivery.ID); stored.Attempts != 0 || stored.State != domain.MailDeliveryQueued {
		t.Fatalf("worker claimed for an errored account: %+v", stored)
	}
}

func TestWorkerTLSFailureMarksAccountError(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("tls", "body", false, false)
	sender := workerTestSender(server)
	// Trust nothing: the fake server certificate must fail the real TLS path.
	sender.TLSConfig = func(host string) *tls.Config {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	worker := f.newWorkerWithSender(sender, "")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	if delivery.State != domain.MailDeliveryQueued || delivery.LastErrorCode != string(domain.MailErrSMTPTLSFailed) {
		t.Fatalf("delivery = %+v", delivery)
	}
	if findAccount(t, f.db, f.account.ID).Status != domain.MailAccountError {
		t.Fatalf("account status = %s", findAccount(t, f.db, f.account.ID).Status)
	}
	if server.DataSeen() != 0 {
		t.Fatal("DATA was sent over a failed TLS session")
	}
}

func TestWorkerDNSFailureMarksAccountError(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("dns", "body", false, false)
	sender := workerTestSender(server)
	sender.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.DNSError{Err: "no such host", Name: domain.MailDefaultSMTPHost, IsNotFound: true}
	}
	worker := f.newWorkerWithSender(sender, "")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	if delivery.State != domain.MailDeliveryQueued || delivery.LastErrorCode != string(domain.MailErrSMTPDNSFailed) {
		t.Fatalf("delivery = %+v", delivery)
	}
	if findAccount(t, f.db, f.account.ID).Status != domain.MailAccountError {
		t.Fatalf("account status = %s", findAccount(t, f.db, f.account.ID).Status)
	}
	if server.Connections() != 0 {
		t.Fatalf("dial reached the fake server: %d", server.Connections())
	}
}

type cancelingSentSender struct {
	cancel context.CancelFunc
	result mailpkg.DeliveryResult
}

func (sender *cancelingSentSender) Send(context.Context, domain.MailAccount, string, mailpkg.Outgoing, mailpkg.DeliveryHooks) mailpkg.DeliveryResult {
	sender.cancel()
	return sender.result
}

func TestWorkerPersistsKnown250AfterShutdownSignal(t *testing.T) {
	f := newWorkerFixture(t)
	queued := f.createQueuedMessage("shutdown", "body", false, false)
	now := time.Now().UTC()
	delivery, ok, err := f.db.ClaimMailDelivery(f.ctx, f.account.ID, now, 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	baselineAccount := findAccount(t, f.db, f.account.ID)
	baseline := accountSnapshot{
		Revision: baselineAccount.Revision, CredentialRef: domain.MailAccountCredentialRef(f.account.ID),
		SMTPHost: baselineAccount.SMTPHost, SMTPUsername: baselineAccount.SMTPUsername,
	}
	sendCtx, cancel := context.WithCancel(f.ctx)
	sender := &cancelingSentSender{
		cancel: cancel,
		result: mailpkg.DeliveryResult{
			Outcome: domain.MailAttemptSent, SMTPCode: 250,
			Phase: domain.MailSMTPPhaseFinished, Response: "250 queued",
		},
	}
	worker := f.newWorkerWithSender(sender, "")
	worker.sendDelivery(sendCtx, delivery, baseline)
	stored := findDelivery(t, f.db, queued.delivery.ID)
	if stored.State != domain.MailDeliverySent || stored.OutcomeCertainty != domain.MailOutcomeProviderAccepted {
		t.Fatalf("250 was lost on shutdown: %+v", stored)
	}
	if findMessage(t, f.db, queued.message.ID).Status != domain.MailMessageSent {
		t.Fatalf("message status = %s", findMessage(t, f.db, queued.message.ID).Status)
	}
}

func TestWorkerAttachmentHashMismatchFailsBeforeSMTP(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("hash", "body", false, true)
	worker := f.newWorker(server, "")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	if server.Connections() != 0 {
		t.Fatal("attachment hash mismatch reached SMTP")
	}
	delivery := findDelivery(t, f.db, queued.delivery.ID)
	if delivery.State != domain.MailDeliveryFailed || delivery.LastErrorCode != string(domain.MailErrAttachmentUnavailable) {
		t.Fatalf("delivery = %+v", delivery)
	}
}

func TestWorkerRateLimitSkipsAccount(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	queued := f.createQueuedMessage("rate", "body", false, false)
	now := time.Now().UTC()
	for index := 0; index < 20; index++ {
		delivery, err := f.db.CreateMailDelivery(f.ctx, domain.MailDelivery{
			MessageID: queued.message.ID, Generation: index + 1, AccountID: f.account.ID,
			State: domain.MailDeliverySent, AvailableAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.CreateMailDeliveryAttempt(f.ctx, domain.MailDeliveryAttempt{
			DeliveryID: delivery.ID, AttemptNo: 1, Outcome: domain.MailAttemptSent, StartedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	worker := f.newWorker(server, "")
	processed, err := worker.ProcessOnce(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 || server.Connections() != 0 {
		t.Fatalf("rate-limited account sent mail: processed=%d connections=%d", processed, server.Connections())
	}
}

func TestWorkerLogsDoNotLeakSensitiveData(t *testing.T) {
	f := newWorkerFixture(t)
	server := mailtest.Start(t, mailtest.Script{})
	f.createQueuedMessage("log", "BODY-CANARY with {{link}}", true, false)
	worker := f.newWorker(server, "https://mail-downloads.example.com")
	if _, err := worker.ProcessOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	logs := f.logs.String()
	for _, secret := range []string{"AUTH-CANARY", "TOKEN-CANARY", "BODY-CANARY", "AUTH PLAIN", "AUTH LOGIN"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log leaked %q:\n%s", secret, logs)
		}
	}
	if server.DataSeen() != 1 {
		t.Fatalf("DATA count = %d", server.DataSeen())
	}
	messages := server.Messages()
	if len(messages) != 1 || !strings.Contains(messageText(t, messages[0]), "TOKEN-CANARY") {
		t.Fatalf("link token was not placed in the outgoing message: %#v", messages)
	}
}

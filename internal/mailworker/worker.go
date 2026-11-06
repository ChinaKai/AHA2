// Package mailworker runs the durable mail delivery outbox. It claims queued
// deliveries under a lease, sends them over implicit-TLS SMTP and writes every
// result back under the same lease. See docs/email-resource-v1-design.md
// sections 6.2-6.4.
package mailworker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/mail"
	"github.com/ChinaKai/AHA2/internal/store"
)

type SecretStore interface {
	Get(string) (string, bool)
}

// Sender is the SMTP seam. Production uses mail.SMTPSender; tests use the same
// implementation against a fake TLS SMTP server.
type Sender interface {
	Send(ctx context.Context, account domain.MailAccount, credential string, outgoing mail.Outgoing, hooks mail.DeliveryHooks) mail.DeliveryResult
}

// Publisher receives committed mail timeline events. Production injects the
// app EventHub; tests use a recording fake.
type Publisher interface {
	Publish(taskID string, event domain.Event)
}

type Config struct {
	Store   *store.Store
	Secrets SecretStore
	Sender  Sender
	Logger  *slog.Logger

	Interval      time.Duration
	LeaseDuration time.Duration
	RenewInterval time.Duration
	MaxPerMinute  int
	MaxPerHour    int
	// MaxConcurrent is the per-account cap for one polling round. Rounds are
	// serialized by Worker.mu and deliveries within an account are sent one at
	// a time; the durable lease, not this value, is the cross-process mutex.
	MaxConcurrent   int
	DownloadBaseURL string
	Publisher       Publisher
	Jitter          func(time.Duration) time.Duration
	Now             func() time.Time
}

// Worker processes the outbox sequentially. Different accounts may be
// processed in later rounds; the database lease is the real concurrency guard.
type Worker struct {
	config Config
	mu     sync.Mutex
}

func New(config Config) *Worker {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Interval <= 0 {
		config.Interval = time.Second
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 300 * time.Second
	}
	if config.RenewInterval <= 0 {
		config.RenewInterval = 60 * time.Second
	}
	if config.MaxPerMinute <= 0 {
		config.MaxPerMinute = 20
	}
	if config.MaxPerHour <= 0 {
		config.MaxPerHour = 200
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 2
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Jitter == nil {
		config.Jitter = fullJitter
	}
	return &Worker{config: config}
}

func fullJitter(duration time.Duration) time.Duration {
	if duration <= 0 {
		return 0
	}
	// Full jitter: a uniform delay in [0, duration]. crypto/rand is used to
	// avoid sharing math/rand state with other workers.
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return duration
	}
	n := uint64(0)
	for _, b := range value {
		n = n<<8 | uint64(b)
	}
	return time.Duration(n % uint64(duration+1))
}

// BackoffForFailedAttempt returns the full-jittered delay after the given
// 1-based failed attempt. The design's base schedule is 1s, 5s, 30s, 2m, 10m,
// 1h for failures 1-6 and 1h after failures 7.
func BackoffForFailedAttempt(attempt int, jitter func(time.Duration) time.Duration) time.Duration {
	schedule := []time.Duration{
		time.Second, 5 * time.Second, 30 * time.Second,
		2 * time.Minute, 10 * time.Minute, time.Hour, time.Hour,
	}
	if attempt < 1 || attempt > len(schedule) {
		return 0
	}
	base := schedule[attempt-1]
	if jitter == nil {
		return base
	}
	return jitter(base)
}

func (w *Worker) now() time.Time {
	return w.config.Now()
}

func (w *Worker) publishMailEvents(events []domain.Event) {
	if w.config.Publisher == nil {
		return
	}
	for _, event := range events {
		w.config.Publisher.Publish(event.AggregateID, event)
	}
}

// Run polls the outbox until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()
	for {
		if _, err := w.ProcessOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.config.Logger.Warn("mail worker round failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ProcessOnce reclaims expired leases, then claims and sends claimable
// deliveries per account while honoring rate and concurrency limits. Rounds are
// deliberately serialized: SQLite has a single writer, and a single in-process
// round keeps lease renewal and result writeback ordering easy to reason about.
// MaxConcurrent bounds how many deliveries one account may process per round.
func (w *Worker) ProcessOnce(ctx context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	reclaimed, events, err := w.config.Store.ReclaimExpiredMailDeliveries(ctx, now)
	if err != nil {
		return 0, err
	}
	w.publishMailEvents(events)
	processed := 0
	for _, deliveryID := range reclaimed.Requeued {
		w.config.Logger.Info("mail delivery requeued after lease expiry", "delivery_id", deliveryID)
	}
	for _, deliveryID := range reclaimed.DeadLettered {
		w.config.Logger.Warn("mail delivery dead-lettered after lease expiry", "delivery_id", deliveryID)
	}
	accountIDs, err := w.config.Store.MailDeliveryQueuedAccountIDs(ctx, now, 50)
	if err != nil {
		return 0, err
	}
	for _, accountID := range accountIDs {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		processed += w.processAccount(ctx, accountID)
	}
	return processed, nil
}

func (w *Worker) processAccount(ctx context.Context, accountID string) int {
	now := w.now()
	account, err := w.config.Store.MailAccount(ctx, accountID)
	if err != nil || !accountSendable(account) {
		w.config.Logger.Info("mail worker account blocked", "account_id", accountID, "reason", "account_unavailable")
		return 0
	}
	ref, err := w.config.Store.MailAccountCredentialRef(ctx, accountID)
	if err != nil || strings.TrimSpace(ref) == "" {
		w.config.Logger.Info("mail worker account blocked", "account_id", accountID, "reason", "credential_missing")
		return 0
	}
	snapshot := accountSnapshot{
		Revision: account.Revision, CredentialRef: ref,
		SMTPHost: account.SMTPHost, SMTPUsername: account.SMTPUsername,
	}
	if count, err := w.config.Store.MailAccountSendingCount(ctx, accountID, now); err == nil && count >= w.config.MaxConcurrent {
		w.config.Logger.Info("mail worker account blocked", "account_id", accountID, "reason", "concurrency_limit", "sending", count)
		return 0
	}
	if count, err := w.config.Store.MailAccountAttemptCountSince(ctx, accountID, now.Add(-time.Minute)); err == nil && count >= w.config.MaxPerMinute {
		w.config.Logger.Info("mail worker account blocked", "account_id", accountID, "reason", "rate_limit_minute", "attempts", count)
		return 0
	}
	if count, err := w.config.Store.MailAccountAttemptCountSince(ctx, accountID, now.Add(-time.Hour)); err == nil && count >= w.config.MaxPerHour {
		w.config.Logger.Info("mail worker account blocked", "account_id", accountID, "reason", "rate_limit_hour", "attempts", count)
		return 0
	}
	processed := 0
	for processed < w.config.MaxConcurrent {
		delivery, ok, events, err := w.config.Store.ClaimMailDelivery(ctx, accountID, w.now(), w.config.LeaseDuration)
		if err != nil {
			w.config.Logger.Warn("mail delivery claim failed", "account_id", accountID, "error", err)
			return processed
		}
		if !ok {
			return processed
		}
		w.publishMailEvents(events)
		w.sendDelivery(ctx, delivery, snapshot)
		processed++
	}
	return processed
}

type accountSnapshot struct {
	Revision      int64
	CredentialRef string
	SMTPHost      string
	SMTPUsername  string
}

func accountSendable(account domain.MailAccount) bool {
	return account.Status == domain.MailAccountOK || account.Status == domain.MailAccountUnverified
}

// writebackTimeout bounds the final state write after a send. It is detached
// from the worker's run context: once SMTP has accepted a message, a shutdown
// signal must not turn a known 250 into an unknown/dead-letter result.
const writebackTimeout = 5 * time.Second

func (w *Worker) writebackContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), writebackTimeout)
}

func (w *Worker) sendDelivery(ctx context.Context, delivery domain.MailDelivery, baseline accountSnapshot) {
	started := w.now()
	logger := w.config.Logger.With(
		"delivery_id", delivery.ID, "message_id", delivery.MessageID,
		"account_id", delivery.AccountID, "attempt", delivery.Attempts,
	)
	finish := func(result store.MailDeliveryResultPatch) bool {
		writeCtx, cancel := w.writebackContext(ctx)
		defer cancel()
		ok, events, err := w.config.Store.FinishMailDelivery(writeCtx, delivery, delivery.LeaseID, result, w.now())
		if err != nil {
			logger.Warn("mail delivery result write failed", "error", err, "state", result.State, "error_code", string(result.ErrorCode))
			return false
		}
		if !ok {
			logger.Warn("mail delivery result discarded: lease no longer owned", "state", result.State, "error_code", string(result.ErrorCode))
			return false
		}
		w.publishMailEvents(events)
		logger.Info("mail delivery attempt finished",
			"state", string(result.State), "error_code", string(result.ErrorCode),
			"certainty", string(result.OutcomeCertainty), "duration_ms", w.now().Sub(started).Milliseconds())
		return true
	}
	deferMessage := func(code domain.MailErrorCode, detail string) {
		writeCtx, cancel := w.writebackContext(ctx)
		defer cancel()
		ok, events, err := w.config.Store.DeferMailDelivery(writeCtx, delivery.ID, delivery.LeaseID,
			delivery.MessageID, code, detail, w.now().Add(time.Minute), w.now())
		if err != nil {
			logger.Warn("mail delivery defer failed", "error", err, "error_code", string(code))
		} else if ok {
			w.publishMailEvents(events)
		}
		logger.Info("mail delivery deferred", "error_code", string(code))
	}

	// Preflight 1: account still exists, enabled and unchanged since claim.
	account, err := w.config.Store.MailAccount(ctx, delivery.AccountID)
	if err != nil {
		deferMessage(domain.MailErrAccountNotFound, "account no longer exists")
		return
	}
	if !accountSendable(account) {
		deferMessage(domain.MailErrAccountDisabled, "account is not enabled for sending")
		return
	}
	if !account.CredentialConfigured {
		deferMessage(domain.MailErrCredentialMissing, "account has no configured authorization code")
		return
	}
	ref, err := w.config.Store.MailAccountCredentialRef(ctx, delivery.AccountID)
	if err != nil || strings.TrimSpace(ref) == "" {
		deferMessage(domain.MailErrCredentialMissing, "credential reference is missing")
		return
	}
	if account.Revision != baseline.Revision || !strings.EqualFold(ref, baseline.CredentialRef) ||
		!strings.EqualFold(account.SMTPHost, baseline.SMTPHost) ||
		!strings.EqualFold(account.SMTPUsername, baseline.SMTPUsername) {
		deferMessage(domain.MailErrAccountChanged, "account changed while the delivery was claimed")
		return
	}
	credential := ""
	if w.config.Secrets != nil {
		credential, _ = w.config.Secrets.Get(ref)
	}
	if strings.TrimSpace(credential) == "" {
		deferMessage(domain.MailErrCredentialMissing, "authorization code is missing")
		return
	}

	message, err := w.config.Store.MailMessage(ctx, delivery.MessageID)
	if err != nil || message.Status != domain.MailMessageSending {
		finish(store.MailDeliveryResultPatch{
			State: domain.MailDeliveryFailed, Outcome: domain.MailAttemptPermanentFailure,
			OutcomeCertainty: domain.MailOutcomePermanentFailure,
			ErrorCode:        domain.MailErrInvalidState, ErrorDetail: "message is unavailable or not sending",
		})
		return
	}
	attachments, err := w.config.Store.MailAttachments(ctx, message.ID, 100)
	if err != nil {
		finish(store.MailDeliveryResultPatch{
			State: domain.MailDeliveryFailed, Outcome: domain.MailAttemptPermanentFailure,
			OutcomeCertainty: domain.MailOutcomePermanentFailure,
			ErrorCode:        domain.MailErrInvalidState, ErrorDetail: "attachments could not be read",
		})
		return
	}

	// Preflight 2: approval digest must match the frozen snapshot the Owner
	// approved. Any mismatch means the message was replaced after approval.
	currentDigest := mail.ApprovalDigest(message, attachments)
	if strings.TrimSpace(delivery.ApprovedDigest) == "" || currentDigest != delivery.ApprovedDigest ||
		message.ApprovalDigest != delivery.ApprovedDigest {
		finish(store.MailDeliveryResultPatch{
			State: domain.MailDeliveryFailed, Outcome: domain.MailAttemptPermanentFailure,
			OutcomeCertainty: domain.MailOutcomePermanentFailure,
			ErrorCode:        domain.MailErrApprovalDigestMismatch,
		})
		return
	}
	if !account.AllowsFrom(message.FromAddress) {
		finish(store.MailDeliveryResultPatch{
			State: domain.MailDeliveryFailed, Outcome: domain.MailAttemptPermanentFailure,
			OutcomeCertainty: domain.MailOutcomePermanentFailure,
			ErrorCode:        domain.MailErrFromNotAllowed,
		})
		return
	}
	to, err := mail.NormalizeRecipients(message.To)
	if err != nil {
		finish(localFailure(domain.MailErrRecipientInvalid))
		return
	}
	cc, err := mail.NormalizeRecipients(message.CC)
	if err != nil {
		finish(localFailure(domain.MailErrRecipientInvalid))
		return
	}
	bcc, err := mail.NormalizeRecipients(message.BCC)
	if err != nil {
		finish(localFailure(domain.MailErrRecipientInvalid))
		return
	}
	if len(to)+len(cc)+len(bcc) == 0 || len(to)+len(cc)+len(bcc) > mail.MaxRecipients {
		finish(localFailure(domain.MailErrRecipientInvalid))
		return
	}
	if len(attachments) > mail.MaxAttachments {
		finish(localFailure(domain.MailErrAttachmentTooLarge))
		return
	}

	bodyText := message.BodyText
	bodyHTML := message.BodyHTML
	direct := make([]mail.OutgoingAttachment, 0, len(attachments))
	directTotal := int64(0)
	for _, attachment := range attachments {
		if attachment.SizeBytes > mail.MaxAttachmentBytes {
			finish(localFailure(domain.MailErrAttachmentTooLarge))
			return
		}
		source, err := w.resolveAttachmentSource(ctx, message.TaskID, attachment)
		if err != nil {
			finish(localFailure(domain.MailErrAttachmentUnavailable))
			return
		}
		switch attachment.DeliveryMode {
		case domain.MailAttachmentDirect:
			content, err := w.config.Store.AttachmentContent(source)
			if err != nil || int64(len(content)) != attachment.SizeBytes || !hashMatches(content, attachment.SHA256) {
				finish(localFailure(domain.MailErrAttachmentUnavailable))
				return
			}
			directTotal += attachment.SizeBytes
			if directTotal > mail.MaxTotalAttachmentBytes {
				finish(localFailure(domain.MailErrTotalTooLarge))
				return
			}
			direct = append(direct, mail.OutgoingAttachment{
				FileName: attachment.FileName, MediaType: attachment.MediaType,
				ContentID: attachment.SourceID, Content: content,
			})
		case domain.MailAttachmentLink:
			if !attachment.LinkRevokedAt.IsZero() || !attachment.LinkExpiresAt.IsZero() && !attachment.LinkExpiresAt.After(w.now()) {
				finish(localFailure(domain.MailErrAttachmentUnavailable))
				return
			}
			if strings.Contains(bodyHTML, "cid:"+attachment.SourceID) || strings.Contains(bodyHTML, "cid:"+attachment.ID) {
				finish(localFailure(domain.MailErrTemplateInvalid))
				return
			}
			linkURL, err := w.linkURL(ctx, attachment)
			if err != nil {
				finish(localFailure(domain.MailErrAttachmentUnavailable))
				return
			}
			bodyText, bodyHTML = applyLink(bodyText, bodyHTML, attachment, linkURL)
		default:
			finish(localFailure(domain.MailErrAttachmentUnavailable))
			return
		}
	}

	messageID := strings.TrimSpace(delivery.MessageIDHeader)
	if messageID == "" {
		messageID, err = generateMessageID(message.FromAddress, w.now())
		if err != nil {
			finish(localFailure(domain.MailErrInvalidState))
			return
		}
		if ok, err := w.config.Store.SetMailDeliveryMessageIDHeader(ctx, delivery.ID, delivery.LeaseID, messageID, w.now()); err != nil || !ok {
			// The lease was lost; do not send.
			return
		}
	}

	// Re-check the account immediately before SMTP. Attachment reads and MIME
	// assembly can take a few seconds, and a disable or credential rotation in
	// that window must still abort the send.
	latestAccount, err := w.config.Store.MailAccount(ctx, delivery.AccountID)
	if err != nil {
		deferMessage(domain.MailErrAccountNotFound, "account disappeared before sending")
		return
	}
	latestRef, err := w.config.Store.MailAccountCredentialRef(ctx, delivery.AccountID)
	if err != nil || !strings.EqualFold(latestRef, baseline.CredentialRef) {
		deferMessage(domain.MailErrCredentialMissing, "credential reference changed before sending")
		return
	}
	if !accountSendable(latestAccount) ||
		latestAccount.Revision != baseline.Revision ||
		!strings.EqualFold(latestAccount.SMTPHost, baseline.SMTPHost) ||
		!strings.EqualFold(latestAccount.SMTPUsername, baseline.SMTPUsername) {
		deferMessage(domain.MailErrAccountChanged, "account changed before sending")
		return
	}

	outgoing := mail.Outgoing{
		FromAddress: message.FromAddress, FromName: message.FromName,
		To: to, CC: cc, BCC: bcc, Subject: message.Subject,
		BodyText: bodyText, BodyHTML: bodyHTML, MessageID: messageID,
		Attachments: direct, Now: w.now(),
	}

	renewCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var leaseLost atomic.Bool
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(w.config.RenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				now := w.now()
				ok, err := w.config.Store.RenewMailDeliveryLease(renewCtx, delivery.ID, delivery.LeaseID, now.Add(w.config.LeaseDuration), now)
				if err != nil || !ok {
					leaseLost.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	hooks := &deliveryHooks{worker: w, deliveryID: delivery.ID, leaseID: delivery.LeaseID}
	result := w.config.Sender.Send(renewCtx, account, credential, outgoing, hooks)
	cancel()
	<-renewDone

	if result.Aborted {
		// The hook refused the transition, so no DATA was sent. Returning to
		// queued is safe only while the lease is still ours; a lost lease makes
		// this a no-op. Any non-aborted result below is written under the lease
		// even if a renewal failed: if that writeback loses the race, the
		// reclaim path decides from smtp_phase and never resends after DATA.
		deferMessage(domain.MailErrSMTPLeaseExpiredBeforeData, "worker lease was lost")
		return
	}
	if leaseLost.Load() {
		logger.Warn("mail delivery lease renewal failed; writing result under original lease")
	}
	applied := false
	switch result.Outcome {
	case domain.MailAttemptSent:
		applied = finish(store.MailDeliveryResultPatch{
			State: domain.MailDeliverySent, Outcome: domain.MailAttemptSent,
			OutcomeCertainty: domain.MailOutcomeProviderAccepted,
			SMTPCode:         result.SMTPCode, SMTPResponse: result.Response,
			MessageIDHeader: messageID, Phase: domain.MailSMTPPhaseFinished,
		})
	case domain.MailAttemptUnknown:
		applied = finish(store.MailDeliveryResultPatch{
			State: domain.MailDeliveryDeadLetter, Outcome: domain.MailAttemptUnknown,
			OutcomeCertainty: domain.MailOutcomeUnknown,
			ErrorCode:        domain.MailErrSMTPAmbiguousAfterData,
			SMTPCode:         result.SMTPCode, Phase: result.Phase,
		})
	case domain.MailAttemptPermanentFailure:
		applied = finish(store.MailDeliveryResultPatch{
			State: domain.MailDeliveryFailed, Outcome: domain.MailAttemptPermanentFailure,
			OutcomeCertainty: domain.MailOutcomePermanentFailure, ErrorCode: result.ErrorCode,
			SMTPCode: result.SMTPCode, SMTPResponse: result.Response, Phase: result.Phase,
		})
	default:
		if delivery.Attempts >= domain.MailDeliveryMaxAttempts {
			applied = finish(store.MailDeliveryResultPatch{
				State: domain.MailDeliveryDeadLetter, Outcome: domain.MailAttemptTransientFailure,
				ErrorCode: domain.MailErrSMTPAttemptsExhausted,
				SMTPCode:  result.SMTPCode, Phase: result.Phase,
			})
			break
		}
		backoff := BackoffForFailedAttempt(delivery.Attempts, w.config.Jitter)
		applied = finish(store.MailDeliveryResultPatch{
			State: domain.MailDeliveryQueued, Outcome: domain.MailAttemptTransientFailure,
			ErrorCode: result.ErrorCode, Backoff: backoff,
			AvailableAt: w.now().Add(backoff), SMTPCode: result.SMTPCode, Phase: result.Phase,
		})
	}
	// A TLS/DNS/auth failure is a property of the account, not only of this
	// attempt. It applies to transient outcomes too (for example a TLS failure
	// is retried later, but the account must stop being claimed until Owner
	// action). Only an applied result may update the account; a stale worker
	// must not clobber a newer revision.
	if applied && accountFailureCode(result.ErrorCode) {
		writeCtx, cancel := w.writebackContext(ctx)
		defer cancel()
		if _, err := w.config.Store.MarkMailAccountError(writeCtx, delivery.AccountID, baseline.Revision, w.now()); err != nil {
			logger.Warn("mail account error status write failed", "error", err)
		}
	}
}

// accountFailureCode reports transport/authentication failures that must move
// the account to error and stop further claims until the Owner retests or
// re-enables it.
func accountFailureCode(code domain.MailErrorCode) bool {
	switch code {
	case domain.MailErrSMTPAuthFailed, domain.MailErrSMTPTLSFailed, domain.MailErrSMTPDNSFailed:
		return true
	default:
		return false
	}
}

func localFailure(code domain.MailErrorCode) store.MailDeliveryResultPatch {
	return store.MailDeliveryResultPatch{
		State: domain.MailDeliveryFailed, Outcome: domain.MailAttemptPermanentFailure,
		OutcomeCertainty: domain.MailOutcomePermanentFailure, ErrorCode: code,
	}
}

func (w *Worker) resolveAttachmentSource(ctx context.Context, taskID string, attachment domain.MailAttachment) (domain.Attachment, error) {
	var source domain.Attachment
	var err error
	switch attachment.SourceType {
	case domain.MailAttachmentSourceTaskAttachment:
		source, err = w.config.Store.Attachment(ctx, taskID, attachment.SourceID)
	case domain.MailAttachmentSourceBuildArtifact:
		artifact, artifactErr := w.config.Store.BuildArtifact(ctx, attachment.SourceID)
		if artifactErr != nil || artifact.TaskID != taskID {
			return domain.Attachment{}, fmt.Errorf("build artifact unavailable")
		}
		if artifact.SizeBytes != attachment.SizeBytes || !strings.EqualFold(artifact.SHA256, attachment.SHA256) {
			return domain.Attachment{}, fmt.Errorf("build artifact metadata changed")
		}
		source, err = w.config.Store.Attachment(ctx, taskID, artifact.AttachmentID)
	default:
		return domain.Attachment{}, fmt.Errorf("unsupported attachment source")
	}
	if err != nil {
		return domain.Attachment{}, err
	}
	if source.Size != attachment.SizeBytes || !strings.EqualFold(source.SHA256, attachment.SHA256) {
		return domain.Attachment{}, fmt.Errorf("attachment metadata changed")
	}
	return source, nil
}

func hashMatches(content []byte, expected string) bool {
	sum := sha256.Sum256(content)
	return strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(expected))
}

func (w *Worker) linkURL(ctx context.Context, attachment domain.MailAttachment) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(w.config.DownloadBaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("download base URL is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return "", fmt.Errorf("download base URL must be absolute https")
	}
	ref, err := w.config.Store.MailAttachmentLinkTokenRef(ctx, attachment.ID)
	if err != nil || strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("link token reference is missing")
	}
	if w.config.Secrets == nil {
		return "", fmt.Errorf("secret store is unavailable")
	}
	token, ok := w.config.Secrets.Get(ref)
	if !ok || strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("link token is missing")
	}
	sum := sha256.Sum256([]byte(token))
	if !strings.EqualFold(hex.EncodeToString(sum[:]), attachment.LinkTokenHash) {
		return "", fmt.Errorf("link token does not match its digest")
	}
	return base + "/d/" + url.PathEscape(token), nil
}

func applyLink(bodyText, bodyHTML string, attachment domain.MailAttachment, linkURL string) (string, string) {
	placeholder := "%%MAIL_LINK:" + attachment.ID + "%%"
	replaced := strings.Contains(bodyText, placeholder) || strings.Contains(bodyHTML, placeholder)
	bodyText = strings.ReplaceAll(bodyText, placeholder, linkURL)
	bodyHTML = strings.ReplaceAll(bodyHTML, placeholder, html.EscapeString(linkURL))
	if replaced {
		return bodyText, bodyHTML
	}
	policy := "受控下载链接"
	if !attachment.LinkExpiresAt.IsZero() && attachment.LinkMaxDownloads > 0 {
		policy = fmt.Sprintf("受控下载链接（有效至 %s，最多 %d 次）",
			attachment.LinkExpiresAt.UTC().Format(time.RFC3339), attachment.LinkMaxDownloads)
	}
	summary := fmt.Sprintf("\n\n附件：%s（%d 字节，SHA256 %s）\n%s：%s\n",
		attachment.FileName, attachment.SizeBytes, attachment.SHA256, policy, linkURL)
	bodyText += summary
	if bodyHTML != "" {
		bodyHTML += fmt.Sprintf("\n<p>附件：%s（%d 字节，SHA256 %s）<br><a href=\"%s\">%s</a></p>",
			html.EscapeString(attachment.FileName), attachment.SizeBytes,
			html.EscapeString(attachment.SHA256), html.EscapeString(linkURL), html.EscapeString(policy))
	}
	return bodyText, bodyHTML
}

func generateMessageID(from string, now time.Time) (string, error) {
	domainPart := "local"
	if _, value, found := strings.Cut(strings.TrimSpace(from), "@"); found && value != "" {
		domainPart = value
	}
	safe := true
	for _, r := range domainPart {
		if r <= 0x20 || r > 0x7e || strings.ContainsRune(`<>()@,;:\"[]`, r) {
			safe = false
			break
		}
	}
	if !safe {
		domainPart = "local"
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("<%s.%d@%s>", hex.EncodeToString(random[:]), now.UTC().UnixNano(), domainPart), nil
}

type deliveryHooks struct {
	worker     *Worker
	deliveryID string
	leaseID    string
}

func (hooks *deliveryHooks) EnterPhase(ctx context.Context, phase domain.MailSMTPPhase) error {
	now := hooks.worker.now()
	ok, err := hooks.worker.config.Store.SetMailDeliveryPhase(ctx,
		hooks.deliveryID, hooks.leaseID, phase, now)
	if err != nil {
		return err
	}
	if !ok {
		return store.ErrMailLeaseLost
	}
	return nil
}

func (hooks *deliveryHooks) BeforeData(ctx context.Context) error {
	return hooks.EnterPhase(ctx, domain.MailSMTPPhaseData)
}

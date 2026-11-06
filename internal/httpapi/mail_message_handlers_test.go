package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func (f *mailAPIFixture) createMessage(payload map[string]any) map[string]any {
	f.t.Helper()
	body := f.request(http.MethodPost, "/api/v1/mail-messages", payload, f.csrf)
	return body
}

func (f *mailAPIFixture) approve(id, digest string) map[string]any {
	f.t.Helper()
	return f.request(http.MethodPost, "/api/v1/mail-messages/"+id+"/approve", map[string]any{
		"expected_digest": digest,
	}, f.csrf)
}

func TestMailContentFingerprintIncludesFromAndSortedBCC(t *testing.T) {
	base := mailContentFingerprint("task-1", "account-1", "noreply@example.com", "AHA",
		[]string{"qa@example.com"}, nil, []string{"b@example.com", "a@example.com"},
		"subject", "text", "", nil)
	same := mailContentFingerprint("task-1", "account-1", "noreply@example.com", "AHA",
		[]string{"qa@example.com"}, nil, []string{"a@example.com", "b@example.com"},
		"subject", "text", "", nil)
	if base != same {
		t.Fatal("BCC order changed the content fingerprint")
	}
	changes := map[string]string{
		"from":      mailContentFingerprint("task-1", "account-1", "other@example.com", "AHA", []string{"qa@example.com"}, nil, []string{"a@example.com", "b@example.com"}, "subject", "text", "", nil),
		"from name": mailContentFingerprint("task-1", "account-1", "noreply@example.com", "Other", []string{"qa@example.com"}, nil, []string{"a@example.com", "b@example.com"}, "subject", "text", "", nil),
		"bcc":       mailContentFingerprint("task-1", "account-1", "noreply@example.com", "AHA", []string{"qa@example.com"}, nil, []string{"a@example.com"}, "subject", "text", "", nil),
	}
	for name, value := range changes {
		if value == base {
			t.Fatalf("%s change did not alter the fingerprint", name)
		}
	}
}

func TestMailCreatePublishesApprovalRequestedEvent(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("event-code")
	events, unsubscribe := f.api.app.Hub().Subscribe(f.task.ID)
	defer unsubscribe()
	created := f.createMessage(map[string]any{
		"task_id": f.task.ID, "account_id": account["id"], "to": []string{"qa@example.com"},
		"subject": "event", "body_text": "body-canary", "submit": true,
		"idempotency_key": "event-1",
	})
	if created["_status"].(float64) != http.StatusCreated {
		t.Fatalf("create = %#v", created)
	}
	select {
	case event := <-events:
		if event.Type != store.MailEventApprovalRequested {
			t.Fatalf("event type = %s", event.Type)
		}
		encoded, _ := json.Marshal(event.Data)
		if strings.Contains(string(encoded), "body-canary") ||
			strings.Contains(strings.ToLower(string(encoded)), "token") {
			t.Fatalf("event payload leaked content: %s", encoded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval-requested event was not published")
	}
	var cards int
	if err := f.rawDB().QueryRowContext(f.ctx,
		`SELECT COUNT(*) FROM conversation_items
		 WHERE task_id=? AND category='mail' AND kind='mail_message'`,
		f.task.ID).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if cards != 1 {
		t.Fatalf("mail card count = %d, want 1", cards)
	}
}

func TestMailApprovalIsOwnerOnlyAndRejectsChangedDigest(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("approval-code")
	template := f.createTemplate(map[string]any{
		"scope": "global", "name": "审批", "subject_template": "{{task.title}}",
		"body_text_template": "正文 {{task.title}}",
	})
	basePayload := map[string]any{
		"task_id": f.task.ID, "account_id": account["id"], "template_id": template["id"],
		"to": []string{"qa-team@example.com"}, "variables": map[string]string{},
		"submit": true,
	}
	payload := map[string]any{}
	for key, value := range basePayload {
		payload[key] = value
	}
	payload["idempotency_key"] = "approval-1"
	created := f.createMessage(payload)
	if created["_status"].(float64) != http.StatusCreated {
		t.Fatalf("create = %#v", created)
	}
	message := created["message"].(map[string]any)
	id := message["id"].(string)
	digest := message["approval_digest"].(string)
	if message["status"] != string(domain.MailMessagePendingApproval) || digest == "" {
		t.Fatalf("message = %#v", message)
	}

	// Anonymous and Agent capabilities cannot approve.
	anonymous := &http.Client{}
	response := requestJSON(t, anonymous, http.MethodPost,
		f.server.URL+"/api/v1/mail-messages/"+id+"/approve",
		map[string]any{"expected_digest": digest}, "")
	if response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous approve status = %d", response.StatusCode)
	}
	response.Body.Close()
	request, _ := http.NewRequest(http.MethodPost,
		f.server.URL+"/api/v1/mail-messages/"+id+"/approve",
		strings.NewReader(`{"expected_digest":"`+digest+`"}`))
	request.Header.Set("Authorization", "Bearer agent-capability-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("agent approve status = %d", response.StatusCode)
	}
	stored, err := f.database.MailMessage(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.MailMessagePendingApproval {
		t.Fatalf("agent approve changed state: %s", stored.Status)
	}

	missingDigest := f.request(http.MethodPost, "/api/v1/mail-messages/"+id+"/approve",
		map[string]any{"expected_digest": ""}, f.csrf)
	if missingDigest["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, missingDigest) != string(domain.MailErrApprovalDigestMismatch) {
		t.Fatalf("missing digest = %#v", missingDigest)
	}
	wrong := f.approve(id, "sha256:wrong")
	if wrong["_status"].(float64) != http.StatusConflict ||
		mailErrorCode(t, wrong) != string(domain.MailErrApprovalDigestMismatch) {
		t.Fatalf("wrong digest = %#v", wrong)
	}

	// Change the frozen content after submission; approval must refuse.
	f.execRaw(`UPDATE mail_messages SET subject='tampered' WHERE id=?`, id)
	tampered := f.approve(id, digest)
	if tampered["_status"].(float64) != http.StatusConflict ||
		mailErrorCode(t, tampered) != string(domain.MailErrApprovalDigestMismatch) {
		t.Fatalf("tampered approve = %#v", tampered)
	}

	// A second, untouched message approves normally and queues a delivery.
	payload2 := map[string]any{}
	for key, value := range basePayload {
		payload2[key] = value
	}
	payload2["idempotency_key"] = "approval-2"
	payload2["duplicate_override"] = true
	payload2["override_reason"] = "test fixture"
	created2 := f.createMessage(payload2)
	message2 := created2["message"].(map[string]any)
	id2 := message2["id"].(string)
	digest2 := message2["approval_digest"].(string)
	approved := f.approve(id2, digest2)
	if approved["_status"].(float64) != http.StatusOK {
		t.Fatalf("approve = %#v", approved)
	}
	delivery := approved["delivery"].(map[string]any)
	if delivery["state"] != string(domain.MailDeliveryQueued) || delivery["approved_digest"] != digest2 {
		t.Fatalf("delivery = %#v", delivery)
	}
	if f.auditCount("mail.message.approve", id2) != 1 {
		t.Fatalf("approval audit count = %d, want 1", f.auditCount("mail.message.approve", id2))
	}
	// Repeated approval with the same digest is idempotent and must not add a
	// second audit row.
	repeated := f.approve(id2, digest2)
	if repeated["_status"].(float64) != http.StatusOK ||
		repeated["delivery"].(map[string]any)["id"] != delivery["id"] {
		t.Fatalf("repeated approve = %#v", repeated)
	}
	if f.auditCount("mail.message.approve", id2) != 1 {
		t.Fatalf("idempotent approval duplicated audit: %d", f.auditCount("mail.message.approve", id2))
	}

	// Terminal Task: an existing pending_approval may still be approved, but a
	// new message may not be created.
	payload3 := map[string]any{}
	for key, value := range basePayload {
		payload3[key] = value
	}
	payload3["idempotency_key"] = "approval-3"
	payload3["duplicate_override"] = true
	payload3["override_reason"] = "test fixture"
	created3 := f.createMessage(payload3)
	message3 := created3["message"].(map[string]any)
	id3 := message3["id"].(string)
	digest3 := message3["approval_digest"].(string)
	f.execRaw(`UPDATE tasks SET status='completed' WHERE id=?`, f.task.ID)
	createdAfter := f.createMessage(map[string]any{
		"task_id": f.task.ID, "account_id": account["id"], "template_id": template["id"],
		"to": []string{"qa-team@example.com"}, "submit": true, "idempotency_key": "approval-4",
	})
	if createdAfter["_status"].(float64) != http.StatusConflict ||
		mailErrorCode(t, createdAfter) != string(domain.MailErrInvalidState) {
		t.Fatalf("create after terminal = %#v", createdAfter)
	}
	terminalApprove := f.approve(id3, digest3)
	if terminalApprove["_status"].(float64) != http.StatusOK {
		t.Fatalf("terminal-task approve = %#v", terminalApprove)
	}

	// Once a worker owns the queued delivery, cancellation is refused.
	claimed, ok, _, err := f.database.ClaimMailDelivery(f.ctx, "", time.Now().UTC(), 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	cancelled := f.request(http.MethodPost, "/api/v1/mail-messages/"+claimed.MessageID+"/cancel", map[string]any{}, f.csrf)
	if cancelled["_status"].(float64) != http.StatusConflict ||
		mailErrorCode(t, cancelled) != string(domain.MailErrInvalidState) {
		t.Fatalf("cancel sending = %#v", cancelled)
	}
}

func TestMailMessageDuplicateDetectionAndOwnerOverride(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("duplicate-code")
	payload := map[string]any{
		"task_id": f.task.ID, "account_id": account["id"],
		"to":      []string{"qa-team@example.com"},
		"subject": "同一封", "body_text": "同一正文", "submit": false,
		"idempotency_key": "dup-1",
	}
	first := f.createMessage(payload)
	if first["_status"].(float64) != http.StatusCreated {
		t.Fatalf("first = %#v", first)
	}
	idempotent := f.createMessage(payload)
	if idempotent["_status"].(float64) != http.StatusOK || idempotent["deduplicated"] != true {
		t.Fatalf("idempotent retry = %#v", idempotent)
	}
	second := map[string]any{}
	for key, value := range payload {
		second[key] = value
	}
	second["idempotency_key"] = "dup-2"
	duplicate := f.createMessage(second)
	if duplicate["_status"].(float64) != http.StatusConflict ||
		mailErrorCode(t, duplicate) != string(domain.MailErrDuplicateMessage) {
		t.Fatalf("duplicate = %#v", duplicate)
	}
	override := map[string]any{}
	for key, value := range second {
		override[key] = value
	}
	override["idempotency_key"] = "dup-3"
	override["duplicate_override"] = true
	override["override_reason"] = "Owner 确认需要重复发送"
	allowed := f.createMessage(override)
	if allowed["_status"].(float64) != http.StatusCreated {
		t.Fatalf("override = %#v", allowed)
	}
	if f.auditCount("mail.message.duplicate_override", allowed["message"].(map[string]any)["id"].(string)) != 1 {
		t.Fatal("duplicate override was not audited")
	}
	// Same key still returns the original, even with override removed.
	again := f.createMessage(payload)
	if again["_status"].(float64) != http.StatusOK || again["message"].(map[string]any)["id"] != first["message"].(map[string]any)["id"] {
		t.Fatalf("idempotent after override = %#v", again)
	}
}

func TestMailAttachmentThresholdsAndControlledLinks(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("attachment-code")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tooLargeID := domain.NewID("attachment")
	f.execRaw(`INSERT INTO attachments(id,task_id,message_id,name,media_type,size_bytes,sha256,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, tooLargeID, f.task.ID, "", "big.zip", "application/zip",
		26<<20, strings.Repeat("1", 64), now)
	tooLarge := f.createMessage(map[string]any{
		"task_id": f.task.ID, "account_id": account["id"], "to": []string{"qa@example.com"},
		"subject": "too large", "body_text": "x", "attachment_ids": []string{tooLargeID},
		"submit": true, "idempotency_key": "attach-1",
	})
	if tooLarge["_status"].(float64) != http.StatusUnprocessableEntity ||
		mailErrorCode(t, tooLarge) != string(domain.MailErrAttachmentTooLarge) {
		t.Fatalf("too large = %#v", tooLarge)
	}

	linkID := domain.NewID("attachment")
	f.execRaw(`INSERT INTO attachments(id,task_id,message_id,name,media_type,size_bytes,sha256,created_at)
		VALUES(?,?,?,?,?,?,?,?)`, linkID, f.task.ID, "", "big.zip", "application/zip",
		21<<20, strings.Repeat("2", 64), now)
	link := f.createMessage(map[string]any{
		"task_id": f.task.ID, "account_id": account["id"], "to": []string{"qa@example.com"},
		"subject": "link mode", "body_text": "下载见附件", "attachment_ids": []string{linkID},
		"submit": true, "idempotency_key": "attach-2",
	})
	if link["_status"].(float64) != http.StatusCreated {
		t.Fatalf("link mode create = %#v", link)
	}
	attachments := link["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v", attachments)
	}
	mailAttachment := attachments[0].(map[string]any)
	if mailAttachment["delivery_mode"] != string(domain.MailAttachmentLink) {
		t.Fatalf("delivery mode = %#v", mailAttachment["delivery_mode"])
	}
	encoded, _ := json.Marshal(link)
	if strings.Contains(string(encoded), "link_token_ref") {
		t.Fatalf("link token reference leaked: %s", encoded)
	}
	attachmentsRows, err := f.database.MailAttachments(f.ctx, link["message"].(map[string]any)["id"].(string), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachmentsRows) != 1 || attachmentsRows[0].LinkTokenHash == "" ||
		attachmentsRows[0].LinkMaxDownloads != domain.MailLinkDefaultMaxDownloads ||
		attachmentsRows[0].LinkExpiresAt.IsZero() {
		t.Fatalf("link attachment = %+v", attachmentsRows[0])
	}
	ref, err := f.database.MailAttachmentLinkTokenRef(f.ctx, attachmentsRows[0].ID)
	if err != nil || f.secrets.values[ref] == "" {
		t.Fatalf("download token not stored: ref=%q err=%v", ref, err)
	}
	if f.auditCount("mail.link.generate", attachmentsRows[0].ID) != 1 {
		t.Fatal("link generation was not audited")
	}
	token := f.secrets.values[ref]
	response := f.downloadGET("/d/" + url.PathEscape(token))
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("confirm page status = %d", response.StatusCode)
	}
	response = f.downloadHEAD("/d/" + url.PathEscape(token))
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status = %d", response.StatusCode)
	}
	stored, err := f.database.MailAttachment(f.ctx, attachmentsRows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LinkDownloadCount != 0 {
		t.Fatalf("GET/HEAD consumed downloads: %d", stored.LinkDownloadCount)
	}
}

var noncePattern = regexp.MustCompile(`name="nonce" value="([^"]+)"`)
var expiresPattern = regexp.MustCompile(`name="expires" value="([^"]+)"`)

const testMailDownloadHost = "mail-downloads.example.com"

func (f *mailAPIFixture) downloadDo(request *http.Request) *http.Response {
	f.t.Helper()
	request.Host = testMailDownloadHost
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		f.t.Fatal(err)
	}
	return response
}

func (f *mailAPIFixture) downloadGET(path string) *http.Response {
	f.t.Helper()
	request, err := http.NewRequest(http.MethodGet, f.download.URL+path, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return f.downloadDo(request)
}

func (f *mailAPIFixture) downloadHEAD(path string) *http.Response {
	f.t.Helper()
	request, err := http.NewRequest(http.MethodHead, f.download.URL+path, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return f.downloadDo(request)
}

func (f *mailAPIFixture) downloadNonce(token string) (string, string) {
	f.t.Helper()
	response := f.downloadGET("/d/" + url.PathEscape(token))
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		f.t.Fatalf("confirm page status = %d", response.StatusCode)
	}
	var body strings.Builder
	buffer := make([]byte, 4096)
	for {
		n, err := response.Body.Read(buffer)
		if n > 0 {
			body.Write(buffer[:n])
		}
		if err != nil {
			break
		}
	}
	nonce := noncePattern.FindStringSubmatch(body.String())
	expires := expiresPattern.FindStringSubmatch(body.String())
	if len(nonce) != 2 || len(expires) != 2 {
		f.t.Fatalf("confirmation page missing nonce: %s", body.String())
	}
	return nonce[1], expires[1]
}

func (f *mailAPIFixture) confirmDownloadWithOrigin(token, nonce, expires, origin string) *http.Response {
	f.t.Helper()
	form := url.Values{"nonce": {nonce}, "expires": {expires}}
	request, err := http.NewRequest(http.MethodPost,
		f.download.URL+"/d/"+url.PathEscape(token)+"/confirm",
		strings.NewReader(form.Encode()))
	if err != nil {
		f.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	return f.downloadDo(request)
}

func (f *mailAPIFixture) confirmDownload(token, nonce, expires string) *http.Response {
	return f.confirmDownloadWithOrigin(token, nonce, expires, "")
}

func TestMailDownloadConfirmCountsOnlyPost(t *testing.T) {
	f := newMailAPIFixture(t)
	now := time.Now().UTC()
	source, err := f.database.CreateAttachment(f.ctx, f.task.ID, "report.txt", "text/plain", []byte("download-bytes"), now)
	if err != nil {
		t.Fatal(err)
	}
	account := f.createAccount("download-code")
	message, err := f.database.CreateMailMessage(f.ctx, domain.MailMessage{
		TaskID: f.task.ID, AccountID: account["id"].(string), Source: domain.MailMessageSourceOwner,
		IdempotencyKey: "download-1", FromAddress: "noreply@example.com",
		To: []string{"qa@example.com"}, Subject: "download", BodyText: "x",
		Status: domain.MailMessagePendingApproval, ContentRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "link-token-canary"
	mailAttachmentID := domain.NewID("mail_attachment")
	ref := domain.MailAttachmentLinkTokenRef(mailAttachmentID)
	f.secrets.values[ref] = token
	attachment, err := f.database.CreateMailAttachment(f.ctx, domain.MailAttachment{
		ID: mailAttachmentID, MessageID: message.ID, Position: 0,
		SourceType: domain.MailAttachmentSourceTaskAttachment, SourceID: source.ID,
		FileName: source.Name, MediaType: source.MediaType, SizeBytes: source.Size,
		SHA256: source.SHA256, DeliveryMode: domain.MailAttachmentLink,
		LinkTokenRef: ref, LinkTokenHash: mailLinkTokenHashForTest(token),
		LinkExpiresAt: now.Add(7 * 24 * time.Hour), LinkMaxDownloads: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A Host outside the configured download domain is rejected before the
	// token is even considered.
	hostRequest, err := http.NewRequest(http.MethodGet, f.download.URL+"/d/"+url.PathEscape(token), nil)
	if err != nil {
		t.Fatal(err)
	}
	hostRequest.Host = "evil.example.com"
	hostResponse, err := http.DefaultClient.Do(hostRequest)
	if err != nil {
		t.Fatal(err)
	}
	hostResponse.Body.Close()
	if hostResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("bad Host status = %d", hostResponse.StatusCode)
	}

	nonce, expires := f.downloadNonce(token)
	nonce2, expires2 := f.downloadNonce(token)
	nonce3, expires3 := f.downloadNonce(token)
	nonce4, expires4 := f.downloadNonce(token)
	nonce5, expires5 := f.downloadNonce(token)
	nonce6, expires6 := f.downloadNonce(token)
	response := f.confirmDownload(token, nonce, expires)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("confirm status = %d", response.StatusCode)
	}
	downloaded, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(downloaded) != "download-bytes" {
		t.Fatalf("download body = %q", downloaded)
	}
	if response.Header.Get("Content-Disposition") == "" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing download headers: %#v", response.Header)
	}
	stored, err := f.database.MailAttachment(f.ctx, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LinkDownloadCount != 1 {
		t.Fatalf("download count = %d", stored.LinkDownloadCount)
	}

	// The nonce is one-time: replay is rejected without consuming another slot.
	replay := f.confirmDownload(token, nonce, expires)
	replay.Body.Close()
	if replay.StatusCode != http.StatusForbidden {
		t.Fatalf("nonce replay status = %d", replay.StatusCode)
	}
	stored, err = f.database.MailAttachment(f.ctx, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LinkDownloadCount != 1 {
		t.Fatalf("replay consumed a download: %d", stored.LinkDownloadCount)
	}

	// Origin: null is accepted for a same-origin confirmation.
	nullOrigin := f.confirmDownloadWithOrigin(token, nonce2, expires2, "null")
	nullOrigin.Body.Close()
	if nullOrigin.StatusCode != http.StatusOK {
		t.Fatalf("Origin null status = %d", nullOrigin.StatusCode)
	}
	// A concrete same-origin Origin is accepted.
	sameOrigin := f.confirmDownloadWithOrigin(token, nonce3, expires3, "https://"+testMailDownloadHost)
	sameOrigin.Body.Close()
	if sameOrigin.StatusCode != http.StatusOK {
		t.Fatalf("same-origin status = %d", sameOrigin.StatusCode)
	}
	// A cross-site Origin is always rejected and does not consume the nonce.
	crossSite := f.confirmDownloadWithOrigin(token, nonce4, expires4, "https://evil.example.com")
	crossSite.Body.Close()
	if crossSite.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site status = %d", crossSite.StatusCode)
	}
	fourth := f.confirmDownload(token, nonce4, expires4)
	fourth.Body.Close()
	if fourth.StatusCode != http.StatusOK {
		t.Fatalf("fourth download status = %d", fourth.StatusCode)
	}
	fifth := f.confirmDownload(token, nonce5, expires5)
	fifth.Body.Close()
	if fifth.StatusCode != http.StatusOK {
		t.Fatalf("fifth download status = %d", fifth.StatusCode)
	}
	sixth := f.confirmDownload(token, nonce6, expires6)
	sixth.Body.Close()
	if sixth.StatusCode != http.StatusGone && sixth.StatusCode != http.StatusNotFound {
		t.Fatalf("exhausted link status = %d", sixth.StatusCode)
	}
	unknown := f.downloadGET("/d/not-a-real-token")
	unknown.Body.Close()
	if unknown.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown token status = %d", unknown.StatusCode)
	}
	// Logs and audit must not contain the raw token.
	var auditCount int
	if err := f.rawDB().QueryRowContext(f.ctx,
		`SELECT COUNT(*) FROM audit_events WHERE data_json LIKE ?`, "%"+token+"%").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("raw download token appeared in audit data %d times", auditCount)
	}
}

func mailLinkTokenHashForTest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func TestMailDraftSubmitAndCancel(t *testing.T) {
	f := newMailAPIFixture(t)
	account := f.createAccount("draft-code")
	created := f.createMessage(map[string]any{
		"task_id": f.task.ID, "account_id": account["id"], "to": []string{"qa@example.com"},
		"subject": "draft", "body_text": "draft body", "submit": false,
		"idempotency_key": "draft-1",
	})
	if created["_status"].(float64) != http.StatusCreated {
		t.Fatalf("draft = %#v", created)
	}
	message := created["message"].(map[string]any)
	id := message["id"].(string)
	if message["status"] != string(domain.MailMessageDraft) {
		t.Fatalf("status = %#v", message["status"])
	}
	submitted := f.request(http.MethodPost, "/api/v1/mail-messages/"+id+"/submit", map[string]any{}, f.csrf)
	if submitted["_status"].(float64) != http.StatusOK ||
		submitted["message"].(map[string]any)["status"] != string(domain.MailMessagePendingApproval) {
		t.Fatalf("submit = %#v", submitted)
	}
	cancelled := f.request(http.MethodPost, "/api/v1/mail-messages/"+id+"/cancel", map[string]any{}, f.csrf)
	if cancelled["_status"].(float64) != http.StatusOK ||
		cancelled["message"].(map[string]any)["status"] != string(domain.MailMessageCancelled) {
		t.Fatalf("cancel = %#v", cancelled)
	}
}

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
	// Repeated approval with the same digest is idempotent.
	repeated := f.approve(id2, digest2)
	if repeated["_status"].(float64) != http.StatusOK ||
		repeated["delivery"].(map[string]any)["id"] != delivery["id"] {
		t.Fatalf("repeated approve = %#v", repeated)
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
	claimed, ok, err := f.database.ClaimMailDelivery(f.ctx, "", time.Now().UTC(), 5*time.Minute)
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
	response, err := http.Get(f.download.URL + "/d/" + url.PathEscape(token))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("confirm page status = %d", response.StatusCode)
	}
	response, err = http.Head(f.download.URL + "/d/" + url.PathEscape(token))
	if err != nil {
		t.Fatal(err)
	}
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

func (f *mailAPIFixture) downloadNonce(token string) (string, string) {
	f.t.Helper()
	response, err := http.Get(f.download.URL + "/d/" + url.PathEscape(token))
	if err != nil {
		f.t.Fatal(err)
	}
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

func (f *mailAPIFixture) confirmDownload(token, nonce, expires string) *http.Response {
	f.t.Helper()
	form := url.Values{"nonce": {nonce}, "expires": {expires}}
	response, err := http.PostForm(f.download.URL+"/d/"+url.PathEscape(token)+"/confirm", form)
	if err != nil {
		f.t.Fatal(err)
	}
	return response
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
		LinkExpiresAt: now.Add(7 * 24 * time.Hour), LinkMaxDownloads: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	nonce, expires := f.downloadNonce(token)
	nonce2, expires2 := f.downloadNonce(token)
	nonce3, expires3 := f.downloadNonce(token)
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

	second := f.confirmDownload(token, nonce2, expires2)
	second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second download status = %d", second.StatusCode)
	}
	third := f.confirmDownload(token, nonce3, expires3)
	third.Body.Close()
	if third.StatusCode != http.StatusGone && third.StatusCode != http.StatusNotFound {
		t.Fatalf("exhausted link status = %d", third.StatusCode)
	}
	unknown, err := http.Get(f.download.URL + "/d/not-a-real-token")
	if err != nil {
		t.Fatal(err)
	}
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

package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// MailDownloadHandler is an independent controlled-download entry. It must be
// mounted on its own listener/domain, never under /api/v1/ and never with the
// Owner session or CSRF cookies. GET shows a confirmation page (not counted);
// POST confirm consumes one download slot.
func (s *Server) MailDownloadHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /d/{token}", s.mailDownloadConfirmPage)
	mux.HandleFunc("POST /d/{token}/confirm", s.mailDownloadConfirm)
	return mux
}

type mailDownloadBucket struct {
	count int
	reset time.Time
}

type mailDownloadLimiter struct {
	mu      sync.Mutex
	buckets map[string]*mailDownloadBucket
}

func newMailDownloadLimiter() *mailDownloadLimiter {
	return &mailDownloadLimiter{buckets: map[string]*mailDownloadBucket{}}
}

func (limiter *mailDownloadLimiter) allow(key string, limit int, now time.Time) bool {
	if limiter == nil || limit <= 0 {
		return true
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.buckets) > 4096 {
		for bucketKey, bucket := range limiter.buckets {
			if !bucket.reset.After(now) {
				delete(limiter.buckets, bucketKey)
			}
		}
	}
	bucket := limiter.buckets[key]
	if bucket == nil || !bucket.reset.After(now) {
		bucket = &mailDownloadBucket{reset: now.Add(time.Minute)}
		limiter.buckets[key] = bucket
	}
	if bucket.count >= limit {
		return false
	}
	bucket.count++
	return true
}

func mailClientIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}

func requestHostname(request *http.Request) string {
	host := request.Host
	if value, _, err := net.SplitHostPort(host); err == nil {
		host = value
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// mailDownloadHostAllowed rejects requests whose Host is not the configured
// public download domain. A spoofed Host cannot reach the entry.
func (s *Server) mailDownloadHostAllowed(request *http.Request) bool {
	return s.mailDownloadHosts[requestHostname(request)]
}

func mailTokenPrefix(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

func (s *Server) mailDownloadNonceMAC(tokenHash string, expires time.Time, id string) string {
	mac := hmac.New(sha256.New, s.mailDownloadKey)
	mac.Write([]byte(tokenHash))
	mac.Write([]byte("|"))
	mac.Write([]byte(strconv.FormatInt(expires.Unix(), 10)))
	mac.Write([]byte("|"))
	mac.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// issueMailDownloadNonce creates a one-time confirmation nonce bound to the
// token hash and a short expiry.
func (s *Server) issueMailDownloadNonce(tokenHash string, expires time.Time) (string, bool) {
	if len(s.mailDownloadKey) == 0 {
		return "", false
	}
	// The form carries whole seconds; bind the MAC and the stored value to the
	// same truncation so verification is stable.
	expires = time.Unix(expires.Unix(), 0).UTC()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", false
	}
	id := base64.RawURLEncoding.EncodeToString(raw[:])
	s.mailDownloadNonceMu.Lock()
	if len(s.mailDownloadNonces) > 4096 {
		for key, expiry := range s.mailDownloadNonces {
			if !expiry.After(time.Now().UTC()) {
				delete(s.mailDownloadNonces, key)
			}
		}
	}
	s.mailDownloadNonces[id] = expires
	s.mailDownloadNonceMu.Unlock()
	return id + "." + s.mailDownloadNonceMAC(tokenHash, expires, id), true
}

// consumeMailDownloadNonce verifies and deletes a confirmation nonce. A replay
// is rejected even within the expiry window.
func (s *Server) consumeMailDownloadNonce(tokenHash, expiresValue, nonce string, now time.Time) bool {
	if len(s.mailDownloadKey) == 0 {
		return false
	}
	expiresUnix, err := strconv.ParseInt(strings.TrimSpace(expiresValue), 10, 64)
	if err != nil {
		return false
	}
	expires := time.Unix(expiresUnix, 0).UTC()
	if now.After(expires) {
		return false
	}
	id, signature, found := strings.Cut(nonce, ".")
	if !found || id == "" || signature == "" {
		return false
	}
	s.mailDownloadNonceMu.Lock()
	stored, ok := s.mailDownloadNonces[id]
	delete(s.mailDownloadNonces, id)
	s.mailDownloadNonceMu.Unlock()
	if !ok || !stored.Equal(expires) {
		return false
	}
	expected := s.mailDownloadNonceMAC(tokenHash, expires, id)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

func mailLinkUsable(attachment domain.MailAttachment, now time.Time) bool {
	if !attachment.LinkRevokedAt.IsZero() {
		return false
	}
	if !attachment.LinkExpiresAt.IsZero() && !attachment.LinkExpiresAt.After(now) {
		return false
	}
	if attachment.LinkMaxDownloads > 0 && attachment.LinkDownloadCount >= attachment.LinkMaxDownloads {
		return false
	}
	return attachment.DeliveryMode == domain.MailAttachmentLink && attachment.LinkTokenHash != ""
}

func (s *Server) lookupMailDownloadLink(request *http.Request, token string) (domain.MailAttachment, bool) {
	hash := mailLinkTokenHash(token)
	if !s.mailDownloadLimiter.allow("token:"+hash, 30, nowUTC()) {
		return domain.MailAttachment{}, false
	}
	attachment, err := s.store.MailAttachmentByLinkTokenHash(request.Context(), hash)
	if err != nil {
		return domain.MailAttachment{}, false
	}
	if !mailLinkUsable(attachment, nowUTC()) {
		return domain.MailAttachment{}, false
	}
	return attachment, true
}

func (s *Server) mailDownloadConfirmPage(writer http.ResponseWriter, request *http.Request) {
	if !s.mailDownloadHostAllowed(request) {
		http.NotFound(writer, request)
		return
	}
	now := nowUTC()
	if !s.mailDownloadLimiter.allow("ip:"+mailClientIP(request), 120, now) {
		writer.Header().Set("Retry-After", "60")
		http.Error(writer, "too many requests", http.StatusTooManyRequests)
		return
	}
	attachment, ok := s.lookupMailDownloadLink(request, request.PathValue("token"))
	if !ok {
		http.NotFound(writer, request)
		return
	}
	expires := now.Add(15 * time.Minute)
	nonce, issued := s.issueMailDownloadNonce(attachment.LinkTokenHash, expires)
	if !issued {
		http.Error(writer, "download entry unavailable", http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	// same-origin keeps the form POST's Origin meaningful while still avoiding
	// token leakage to third parties. Origin: null is accepted after nonce
	// verification because some browsers/privacy modes send it for same-origin
	// POSTs; a genuine cross-site Origin is always rejected.
	writer.Header().Set("Referrer-Policy", "same-origin")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'")
	page := fmt.Sprintf(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>下载附件</title><style>body{font-family:system-ui,sans-serif;max-width:36rem;margin:3rem auto;padding:0 1rem;color:#0f172a}dl{display:grid;grid-template-columns:6rem 1fr;gap:.4rem}dt{color:#475569}dd{margin:0;word-break:break-all}button{padding:.7rem 1.2rem;font-size:1rem}</style></head>
<body><h1>下载确认</h1><dl><dt>文件名</dt><dd>%s</dd><dt>大小</dt><dd>%d 字节</dd><dt>SHA256</dt><dd>%s</dd><dt>有效期</dt><dd>%s</dd></dl>
<form method="post" action="confirm"><input type="hidden" name="expires" value="%d"><input type="hidden" name="nonce" value="%s"><button type="submit">确认下载</button></form>
<p>此页面不会消耗下载次数；点击确认后才会计数。</p></body></html>`,
		html.EscapeString(attachment.FileName), attachment.SizeBytes,
		html.EscapeString(attachment.SHA256),
		html.EscapeString(attachment.LinkExpiresAt.Format("2006-01-02 15:04 MST")),
		expires.Unix(), html.EscapeString(nonce))
	_, _ = writer.Write([]byte(page))
	s.audit(request, "mail.link.view", "mail_attachment", attachment.ID, map[string]any{
		"message_id": attachment.MessageID, "token_hash_prefix": mailTokenPrefix(attachment.LinkTokenHash),
	})
}

func (s *Server) mailDownloadConfirm(writer http.ResponseWriter, request *http.Request) {
	if !s.mailDownloadHostAllowed(request) {
		http.NotFound(writer, request)
		return
	}
	now := nowUTC()
	if !s.mailDownloadLimiter.allow("ip:"+mailClientIP(request), 120, now) {
		writer.Header().Set("Retry-After", "60")
		http.Error(writer, "too many requests", http.StatusTooManyRequests)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	if !sameOriginRequest(request) {
		http.Error(writer, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	token := request.PathValue("token")
	hash := mailLinkTokenHash(token)
	if !s.mailDownloadLimiter.allow("token:"+hash, 30, now) {
		writer.Header().Set("Retry-After", "60")
		http.Error(writer, "too many requests", http.StatusTooManyRequests)
		return
	}
	attachment, err := s.store.MailAttachmentByLinkTokenHash(request.Context(), hash)
	if err != nil || !mailLinkUsable(attachment, now) {
		http.NotFound(writer, request)
		return
	}
	if !s.consumeMailDownloadNonce(hash, request.FormValue("expires"), request.FormValue("nonce"), now) {
		http.Error(writer, "confirmation expired", http.StatusForbidden)
		return
	}
	message, err := s.store.MailMessage(request.Context(), attachment.MessageID)
	if err != nil {
		http.Error(writer, "link is no longer available", http.StatusGone)
		return
	}
	source, err := s.mailDownloadSource(request, message, attachment)
	if err != nil {
		http.Error(writer, "link is no longer available", http.StatusGone)
		return
	}
	file, err := s.store.OpenAttachmentContent(source)
	if err != nil {
		http.Error(writer, "link is no longer available", http.StatusGone)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != attachment.SizeBytes {
		http.Error(writer, "link is no longer available", http.StatusGone)
		return
	}
	// Consume the download slot only after the file is known to be present and
	// the right size. The response is streamed; the file is never buffered.
	consumed, err := s.store.IncrementMailAttachmentLinkDownload(request.Context(), attachment.ID, now)
	if err != nil || !consumed {
		http.Error(writer, "link is no longer available", http.StatusGone)
		return
	}
	contentType := source.MediaType
	if _, _, parseErr := mime.ParseMediaType(contentType); parseErr != nil {
		contentType = "application/octet-stream"
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": attachment.FileName,
	}))
	writer.Header().Set("Content-Length", strconv.FormatInt(attachment.SizeBytes, 10))
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(writer, file)
	s.audit(request, "mail.link.download", "mail_attachment", attachment.ID, map[string]any{
		"message_id": attachment.MessageID, "token_hash_prefix": mailTokenPrefix(hash),
		"download_count": attachment.LinkDownloadCount + 1,
	})
}

func (s *Server) mailDownloadSource(request *http.Request, message domain.MailMessage, attachment domain.MailAttachment) (domain.Attachment, error) {
	switch attachment.SourceType {
	case domain.MailAttachmentSourceTaskAttachment:
		return s.store.Attachment(request.Context(), message.TaskID, attachment.SourceID)
	case domain.MailAttachmentSourceBuildArtifact:
		artifact, err := s.store.BuildArtifact(request.Context(), attachment.SourceID)
		if err != nil || artifact.TaskID != message.TaskID {
			return domain.Attachment{}, errMailDownloadUnavailable
		}
		return s.store.Attachment(request.Context(), message.TaskID, artifact.AttachmentID)
	default:
		return domain.Attachment{}, errMailDownloadUnavailable
	}
}

var errMailDownloadUnavailable = &mailDownloadError{message: "download source unavailable"}

type mailDownloadError struct{ message string }

func (e *mailDownloadError) Error() string { return e.message }

func sameOriginRequest(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin != "" {
		// Browsers may send Origin: null for a same-origin POST (for example
		// with privacy modes). The one-time nonce still binds the request to the
		// confirmation page; a concrete cross-site origin is rejected.
		if !strings.EqualFold(origin, "null") {
			parsed, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(parsed.Host, request.Host) {
				return false
			}
		}
	}
	if referer := strings.TrimSpace(request.Header.Get("Referer")); referer != "" {
		parsed, err := url.Parse(referer)
		if err != nil || !strings.EqualFold(parsed.Host, request.Host) {
			return false
		}
	}
	return true
}

package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/mail/mailtest"
)

type recordingHooks struct {
	mu          sync.Mutex
	phases      []domain.MailSMTPPhase
	dataEntered bool
	failBefore  error
}

func (hooks *recordingHooks) EnterPhase(_ context.Context, phase domain.MailSMTPPhase) error {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.phases = append(hooks.phases, phase)
	return nil
}

func (hooks *recordingHooks) BeforeData(_ context.Context) error {
	if hooks.failBefore != nil {
		return hooks.failBefore
	}
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	hooks.dataEntered = true
	hooks.phases = append(hooks.phases, domain.MailSMTPPhaseData)
	return nil
}

func (hooks *recordingHooks) sawData() bool {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	return hooks.dataEntered
}

func newDeliverySender(server *mailtest.Server) *SMTPSender {
	sender := NewSMTPSender()
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

func deliveryAccount() domain.MailAccount {
	account := testAccount()
	account.SMTPUsername = "noreply@example.com"
	return account
}

func testOutgoing() Outgoing {
	return Outgoing{
		FromAddress: "noreply@example.com", FromName: "AHA 构建通知",
		To: []string{"qa-team@example.com"}, Subject: "[AHA] build ready",
		BodyText: "build ready", MessageID: "<test.1@example.com>",
	}
}

func TestSMTPSenderSuccessAndPhaseOrder(t *testing.T) {
	server := mailtest.Start(t, mailtest.Script{})
	sender := newDeliverySender(server)
	hooks := &recordingHooks{}
	result := sender.Send(context.Background(), deliveryAccount(), "top-secret-code", testOutgoing(), hooks)
	if result.Outcome != domain.MailAttemptSent || result.SMTPCode != 250 || result.ErrorCode != "" {
		t.Fatalf("result = %+v", result)
	}
	if server.DataSeen() != 1 {
		t.Fatalf("DATA count = %d", server.DataSeen())
	}
	messages := server.Messages()
	if len(messages) != 1 || !strings.Contains(messages[0], "[AHA] build ready") ||
		!strings.Contains(messages[0], "build ready") {
		t.Fatalf("message = %#v", messages)
	}
	if strings.Contains(messages[0], "top-secret-code") {
		t.Fatal("authorization code leaked into the message")
	}
	if !hooks.sawData() {
		t.Fatal("BeforeData hook was not called")
	}
	phases := hooks.phases
	for _, want := range []domain.MailSMTPPhase{
		domain.MailSMTPPhaseConnect, domain.MailSMTPPhaseTLS, domain.MailSMTPPhaseAuth,
		domain.MailSMTPPhaseMailFrom, domain.MailSMTPPhaseRcptTo, domain.MailSMTPPhaseData,
	} {
		found := false
		for _, phase := range phases {
			if phase == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("phase %q missing from %v", want, phases)
		}
	}
}

func TestSMTPSenderErrorMapping(t *testing.T) {
	tests := []struct {
		name          string
		script        mailtest.Script
		wantOutcome   domain.MailAttemptOutcome
		wantCode      domain.MailErrorCode
		wantSMTP      int
		wantDataCalls int
	}{
		{
			name: "greeting 421", script: mailtest.Script{Greeting: "421 service unavailable"},
			wantOutcome: domain.MailAttemptTransientFailure, wantCode: domain.MailErrSMTPServiceUnavailable, wantSMTP: 421,
		},
		{
			name: "auth 535", script: mailtest.Script{AuthMode: "fail"},
			wantOutcome: domain.MailAttemptPermanentFailure, wantCode: domain.MailErrSMTPAuthFailed, wantSMTP: 535,
		},
		{
			name: "auth login fallback", script: mailtest.Script{AuthMode: "login_only"},
			wantOutcome: domain.MailAttemptSent, wantSMTP: 250, wantDataCalls: 1,
		},
		{
			name: "mail from 550", script: mailtest.Script{MailFromReply: "550 sender denied"},
			wantOutcome: domain.MailAttemptPermanentFailure, wantCode: domain.MailErrSMTPSenderRejected, wantSMTP: 550,
		},
		{
			name: "mail from 553", script: mailtest.Script{MailFromReply: "553 sender denied"},
			wantOutcome: domain.MailAttemptPermanentFailure, wantCode: domain.MailErrSMTPSenderRejected, wantSMTP: 553,
		},
		{
			name: "mail from 450", script: mailtest.Script{MailFromReply: "450 try later"},
			wantOutcome: domain.MailAttemptTransientFailure, wantCode: domain.MailErrSMTPTemporaryFailure, wantSMTP: 450,
		},
		{
			name: "rcpt 550", script: mailtest.Script{RcptReplies: func(string) string { return "550 no such user" }},
			wantOutcome: domain.MailAttemptPermanentFailure, wantCode: domain.MailErrSMTPRecipientRejected, wantSMTP: 550,
		},
		{
			name: "rcpt 450", script: mailtest.Script{RcptReplies: func(string) string { return "450 mailbox busy" }},
			wantOutcome: domain.MailAttemptTransientFailure, wantCode: domain.MailErrSMTPRecipientTemporarilyUnavailable, wantSMTP: 450,
		},
		{
			name: "rcpt 451", script: mailtest.Script{RcptReplies: func(string) string { return "451 local error" }},
			wantOutcome: domain.MailAttemptTransientFailure, wantCode: domain.MailErrSMTPRecipientTemporarilyUnavailable, wantSMTP: 451,
		},
		{
			name: "rcpt 452", script: mailtest.Script{RcptReplies: func(string) string { return "452 insufficient storage" }},
			wantOutcome: domain.MailAttemptTransientFailure, wantCode: domain.MailErrSMTPRecipientTemporarilyUnavailable, wantSMTP: 452,
		},
		{
			name: "rcpt 553", script: mailtest.Script{RcptReplies: func(string) string { return "553 invalid mailbox" }},
			wantOutcome: domain.MailAttemptPermanentFailure, wantCode: domain.MailErrSMTPRecipientRejected, wantSMTP: 553,
		},
		{
			name: "data 552", script: mailtest.Script{DataReply: "552 message too large"},
			wantOutcome: domain.MailAttemptPermanentFailure, wantCode: domain.MailErrSMTPMessageTooLarge, wantSMTP: 552,
			wantDataCalls: 1,
		},
		{
			name: "data 554", script: mailtest.Script{DataReply: "554 content rejected"},
			wantOutcome: domain.MailAttemptPermanentFailure, wantCode: domain.MailErrSMTPContentRejected, wantSMTP: 554,
			wantDataCalls: 1,
		},
		{
			name: "data 451", script: mailtest.Script{DataReply: "451 try later"},
			wantOutcome: domain.MailAttemptTransientFailure, wantCode: domain.MailErrSMTPTemporaryFailure, wantSMTP: 451,
			wantDataCalls: 1,
		},
		{
			name: "final 554", script: mailtest.Script{FinalReply: "554 spam rejected"},
			wantOutcome: domain.MailAttemptPermanentFailure, wantCode: domain.MailErrSMTPContentRejected, wantSMTP: 554,
			wantDataCalls: 1,
		},
		{
			name: "disconnect before data", script: mailtest.Script{CloseOnRcpt: true},
			wantOutcome: domain.MailAttemptTransientFailure, wantCode: domain.MailErrSMTPConnectionLost,
		},
		{
			name: "disconnect after data", script: mailtest.Script{CloseAfterData: true},
			wantOutcome: domain.MailAttemptUnknown, wantCode: domain.MailErrSMTPAmbiguousAfterData,
			wantDataCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := mailtest.Start(t, test.script)
			sender := newDeliverySender(server)
			result := sender.Send(context.Background(), deliveryAccount(), "secret-code", testOutgoing(), &recordingHooks{})
			if result.Outcome != test.wantOutcome || result.ErrorCode != test.wantCode {
				t.Fatalf("result = %+v, want outcome=%s code=%s", result, test.wantOutcome, test.wantCode)
			}
			if test.wantSMTP != 0 && result.SMTPCode != test.wantSMTP {
				t.Fatalf("smtp code = %d, want %d", result.SMTPCode, test.wantSMTP)
			}
			if server.DataSeen() != test.wantDataCalls {
				t.Fatalf("DATA count = %d, want %d", server.DataSeen(), test.wantDataCalls)
			}
			if strings.Contains(result.Response, "secret-code") || strings.Contains(result.Response, "AUTH") {
				t.Fatalf("result leaked authentication data: %q", result.Response)
			}
		})
	}
}

func TestSMTPSenderPartialRcptNeverSendsData(t *testing.T) {
	tests := []struct {
		name        string
		replies     func(string) string
		wantOutcome domain.MailAttemptOutcome
		wantCode    domain.MailErrorCode
	}{
		{
			name: "one 550 among 250", wantOutcome: domain.MailAttemptPermanentFailure,
			wantCode: domain.MailErrSMTPRecipientRejected,
			replies: func(recipient string) string {
				if strings.Contains(recipient, "bad@") {
					return "550 unknown user"
				}
				return "250 OK"
			},
		},
		{
			name: "one 450 among 250", wantOutcome: domain.MailAttemptTransientFailure,
			wantCode: domain.MailErrSMTPRecipientTemporarilyUnavailable,
			replies: func(recipient string) string {
				if strings.Contains(recipient, "busy@") {
					return "450 mailbox busy"
				}
				return "250 OK"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := mailtest.Start(t, mailtest.Script{RcptReplies: test.replies})
			sender := newDeliverySender(server)
			outgoing := testOutgoing()
			outgoing.To = []string{"good@example.com", "bad@example.com", "busy@example.com"}
			result := sender.Send(context.Background(), deliveryAccount(), "secret", outgoing, &recordingHooks{})
			if result.Outcome != test.wantOutcome || result.ErrorCode != test.wantCode {
				t.Fatalf("result = %+v", result)
			}
			if server.DataSeen() != 0 {
				t.Fatalf("DATA was sent after a partial RCPT failure: %d", server.DataSeen())
			}
			if len(server.RcptCalls()) != 3 {
				t.Fatalf("RCPT calls = %v", server.RcptCalls())
			}
			commands := server.Commands()
			foundReset := false
			for _, command := range commands {
				if command == "RSET" {
					foundReset = true
				}
			}
			if !foundReset {
				t.Fatalf("RSET was not sent: %v", commands)
			}
		})
	}
}

func TestSMTPSenderConnectTimeout(t *testing.T) {
	sender := NewSMTPSender()
	sender.ConnectTimeout = 10 * time.Millisecond
	sender.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, context.DeadlineExceeded
	}
	result := sender.Send(context.Background(), deliveryAccount(), "secret", testOutgoing(), &recordingHooks{})
	if result.Outcome != domain.MailAttemptTransientFailure || result.ErrorCode != domain.MailErrSMTPConnectTimeout {
		t.Fatalf("result = %+v", result)
	}
}

func TestSMTPSenderTLSFailureDoesNotDowngrade(t *testing.T) {
	server := mailtest.Start(t, mailtest.Script{})
	sender := NewSMTPSender()
	sender.ConnectTimeout = time.Second
	sender.TLSTimeout = time.Second
	sender.TotalTimeout = 3 * time.Second
	sender.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Addr)
	}
	// Deliberately trust nothing: the self-signed fake certificate must fail.
	sender.TLSConfig = func(host string) *tls.Config {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	result := sender.Send(context.Background(), deliveryAccount(), "secret", testOutgoing(), &recordingHooks{})
	if result.Outcome != domain.MailAttemptTransientFailure || result.ErrorCode != domain.MailErrSMTPTLSFailed {
		t.Fatalf("result = %+v", result)
	}
	if server.DataSeen() != 0 {
		t.Fatal("DATA was sent over a failed TLS session")
	}
}

func TestSMTPSenderBeforeDataAbortSendsNoData(t *testing.T) {
	server := mailtest.Start(t, mailtest.Script{})
	sender := newDeliverySender(server)
	hooks := &recordingHooks{failBefore: errors.New("lease lost")}
	result := sender.Send(context.Background(), deliveryAccount(), "secret", testOutgoing(), hooks)
	if !result.Aborted {
		t.Fatalf("result = %+v, want aborted", result)
	}
	if server.DataSeen() != 0 {
		t.Fatalf("DATA was sent despite a failed BeforeData hook")
	}
}

func TestSMTPSenderTimeoutAfterDataIsUnknown(t *testing.T) {
	server := mailtest.Start(t, mailtest.Script{DataDelay: 500 * time.Millisecond})
	sender := newDeliverySender(server)
	sender.DataTimeout = 100 * time.Millisecond
	result := sender.Send(context.Background(), deliveryAccount(), "secret", testOutgoing(), &recordingHooks{})
	if result.Outcome != domain.MailAttemptUnknown || result.ErrorCode != domain.MailErrSMTPAmbiguousAfterData {
		t.Fatalf("result = %+v", result)
	}
	if server.DataSeen() != 1 {
		t.Fatalf("DATA count = %d", server.DataSeen())
	}
}

func TestBuildMessageMediaTypeParametersAndLongSubject(t *testing.T) {
	longSubject := strings.TrimSpace(strings.Repeat("very long ascii subject ", 20))
	outgoing := testOutgoing()
	outgoing.Subject = longSubject
	outgoing.Attachments = []OutgoingAttachment{{
		FileName: "firmware.bin", MediaType: "text/plain; charset=utf-8", Content: []byte("payload"),
	}}
	message, err := BuildMessage(outgoing)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(message), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("header line exceeds RFC 5322 limit: %d bytes", len(line))
		}
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(message)))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	if decoded != longSubject {
		t.Fatalf("subject round-trip = %q", decoded)
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("top content type = %q params=%v err=%v", mediaType, params, err)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	found := false
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		if part.FileName() != "firmware.bin" {
			continue
		}
		found = true
		contentType := part.Header.Get("Content-Type")
		if strings.TrimSpace(contentType) == "" ||
			!strings.Contains(contentType, "text/plain") ||
			!strings.Contains(contentType, "firmware.bin") {
			t.Fatalf("attachment Content-Type = %q", contentType)
		}
	}
	if !found {
		t.Fatal("attachment part missing")
	}
}

func TestSMTPSenderBuildsMessageBeforeConnecting(t *testing.T) {
	server := mailtest.Start(t, mailtest.Script{})
	sender := newDeliverySender(server)
	outgoing := testOutgoing()
	outgoing.Subject = "bad\r\nBcc: evil@example.com"
	result := sender.Send(context.Background(), deliveryAccount(), "secret", outgoing, &recordingHooks{})
	if result.Outcome != domain.MailAttemptPermanentFailure {
		t.Fatalf("result = %+v", result)
	}
	if server.Connections() != 0 {
		t.Fatalf("invalid local message opened %d SMTP connections", server.Connections())
	}
}

func TestBuildMessageOmitsBCCAndEscapesHeaders(t *testing.T) {
	outgoing := testOutgoing()
	outgoing.To = []string{"qa@example.com"}
	outgoing.CC = []string{"cc@example.com"}
	outgoing.BCC = []string{"bcc@example.com"}
	outgoing.Attachments = []OutgoingAttachment{{
		FileName: "firmware.bin", MediaType: "application/octet-stream", Content: []byte("payload"),
	}}
	message, err := BuildMessage(outgoing)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(message)
	if strings.Contains(encoded, "bcc@example.com") {
		t.Fatal("BCC leaked into headers")
	}
	if !strings.Contains(encoded, "filename=firmware.bin") &&
		!strings.Contains(encoded, "filename*=utf-8''firmware.bin") &&
		!strings.Contains(encoded, "filename=\"firmware.bin\"") {
		t.Fatalf("attachment disposition missing: %s", encoded)
	}
	bad := outgoing
	bad.Subject = "line\r\nBcc: evil@example.com"
	if _, err := BuildMessage(bad); err == nil {
		t.Fatal("header injection was accepted")
	}
}

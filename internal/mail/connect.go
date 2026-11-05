package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// Tester is the connection-test seam. Tests inject a fake SMTP implementation;
// production uses SMTPTester.
type Tester interface {
	Test(ctx context.Context, account domain.MailAccount, credential string) TestResult
}

// TestResult matches the Owner API response shape from design section 3.3.
type TestResult struct {
	OK           bool                 `json:"ok"`
	Level        string               `json:"level,omitempty"`
	Host         string               `json:"host,omitempty"`
	Port         int                  `json:"port,omitempty"`
	TLSVersion   string               `json:"tls_version,omitempty"`
	Auth         string               `json:"auth,omitempty"`
	Capabilities []string             `json:"capabilities,omitempty"`
	DurationMS   int64                `json:"duration_ms"`
	ErrorCode    domain.MailErrorCode `json:"error_code,omitempty"`
	Retryable    bool                 `json:"retryable,omitempty"`
	Message      string               `json:"message,omitempty"`
}

// SMTPTester performs the design's connection test: DNS/TCP connect, implicit
// TLS, EHLO, AUTH and QUIT. It never sends a message and never records the
// authorization code or a raw SMTP transcript.
type SMTPTester struct {
	ConnectTimeout time.Duration
	TLSTimeout     time.Duration
	CommandTimeout time.Duration
	// DialContext and TLSConfig are injectable for fake SMTP servers in tests.
	DialContext func(ctx context.Context, network, address string) (net.Conn, error)
	TLSConfig   func(host string) *tls.Config
	Now         func() time.Time
}

func NewSMTPTester() *SMTPTester {
	return &SMTPTester{
		ConnectTimeout: 10 * time.Second,
		TLSTimeout:     10 * time.Second,
		CommandTimeout: 15 * time.Second,
		Now:            time.Now,
	}
}

func (tester *SMTPTester) now() time.Time {
	if tester.Now != nil {
		return tester.Now()
	}
	return time.Now()
}

func (tester *SMTPTester) dialContext() func(context.Context, string, string) (net.Conn, error) {
	if tester.DialContext != nil {
		return tester.DialContext
	}
	dialer := &net.Dialer{}
	return dialer.DialContext
}

func (tester *SMTPTester) tlsConfig(host string) *tls.Config {
	if tester.TLSConfig != nil {
		return tester.TLSConfig(host)
	}
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func (tester *SMTPTester) Test(ctx context.Context, account domain.MailAccount, credential string) TestResult {
	if ctx == nil {
		ctx = context.Background()
	}
	host := strings.TrimSpace(account.SMTPHost)
	if host == "" {
		host = domain.MailDefaultSMTPHost
	}
	normalizedHost, allowedHost := NormalizeSMTPHost(host)
	port := account.SMTPPort
	if port <= 0 {
		port = domain.MailDefaultSMTPPort
	}
	started := tester.now()
	result := TestResult{Host: host, Port: port}
	finish := func() TestResult {
		result.DurationMS = tester.now().Sub(started).Milliseconds()
		return result
	}
	fail := func(level string, code domain.MailErrorCode, retryable bool, message string) TestResult {
		result.OK = false
		result.Level = level
		result.ErrorCode = code
		result.Retryable = retryable
		result.Message = message
		return finish()
	}

	if !allowedHost {
		return fail("connect", domain.MailErrAccountInvalid, false, "SMTP 主机不在允许列表")
	}
	if port != domain.MailDefaultSMTPPort {
		return fail("connect", domain.MailErrAccountInvalid, false, "SMTP 端口必须是 465")
	}
	host = normalizedHost
	result.Host = host
	if strings.TrimSpace(credential) == "" {
		return fail("auth", domain.MailErrCredentialMissing, false, "缺少客户端授权码")
	}
	username := strings.TrimSpace(account.SMTPUsername)
	if username == "" {
		username = strings.TrimSpace(account.Address)
	}

	connectTimeout := tester.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := tester.dialContext()(dialCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	cancel()
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return fail("connect", domain.MailErrSMTPDNSFailed, true, "SMTP 主机解析失败")
		}
		if isTimeoutError(err) {
			return fail("connect", domain.MailErrSMTPConnectTimeout, true, "连接 SMTP 主机超时")
		}
		return fail("connect", domain.MailErrSMTPConnectTimeout, true, "无法连接 SMTP 主机")
	}
	defer conn.Close()

	tlsTimeout := tester.TLSTimeout
	if tlsTimeout <= 0 {
		tlsTimeout = 10 * time.Second
	}
	tlsConn := tls.Client(conn, tester.tlsConfig(host))
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, tlsTimeout)
	err = tlsConn.HandshakeContext(handshakeCtx)
	handshakeCancel()
	if err != nil {
		return fail("tls", domain.MailErrSMTPTLSFailed, false, "TLS 握手失败，请检查证书与主机名")
	}
	result.TLSVersion = tlsVersionName(tlsConn.ConnectionState().Version)
	result.Level = "connect"

	commandTimeout := tester.CommandTimeout
	if commandTimeout <= 0 {
		commandTimeout = 15 * time.Second
	}
	deadline := tester.now().Add(commandTimeout)
	_ = tlsConn.SetDeadline(deadline)
	client, err := smtp.NewClient(tlsConn, host)
	if err != nil {
		return fail("connect", domain.MailErrSMTPProtocolError, false, "SMTP 会话初始化失败")
	}
	if err := client.Hello("localhost"); err != nil {
		_ = client.Close()
		return fail("connect", classifySMTPCommandError(err, domain.MailErrSMTPProtocolError), isTimeoutError(err), "SMTP EHLO 失败")
	}
	capabilities := make([]string, 0)
	result.Capabilities = capabilities

	result.Level = "auth"
	if err := client.Auth(smtp.PlainAuth("", username, credential, host)); err != nil {
		_ = client.Close()
		return fail("auth", classifySMTPCommandError(err, domain.MailErrSMTPAuthFailed), isTimeoutError(err), "SMTP 认证失败，请检查客户端授权码是否有效")
	}
	_ = client.Quit()
	result.OK = true
	result.Level = "connect"
	result.Auth = "ok"
	return finish()
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func classifySMTPCommandError(err error, fallback domain.MailErrorCode) domain.MailErrorCode {
	if isTimeoutError(err) {
		return domain.MailErrSMTPConnectTimeout
	}
	// net/smtp does not expose a typed error for server responses; a 421
	// service-unavailable response is the only transient code worth separating
	// from the phase-specific fallback.
	if strings.Contains(err.Error(), "421 ") || strings.HasPrefix(err.Error(), "421") {
		return domain.MailErrSMTPServiceUnavailable
	}
	return fallback
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS13:
		return "TLS1.3"
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS11:
		return "TLS1.1"
	case tls.VersionTLS10:
		return "TLS1.0"
	default:
		return fmt.Sprintf("0x%04x", version)
	}
}

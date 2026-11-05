package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// DeliveryHooks let the durable worker persist and prove ownership of the
// delivery lease around irreversible SMTP steps. EnterPhase is advisory except
// for BeforeData, which must succeed before the DATA command is written.
type DeliveryHooks interface {
	EnterPhase(ctx context.Context, phase domain.MailSMTPPhase) error
	BeforeData(ctx context.Context) error
}

// DeliveryResult is the stable outcome of one SMTP attempt. Aborted means the
// worker hook refused the transition (usually a lost lease); no DATA was sent.
type DeliveryResult struct {
	Outcome   domain.MailAttemptOutcome
	ErrorCode domain.MailErrorCode
	SMTPCode  int
	Phase     domain.MailSMTPPhase
	Response  string
	Aborted   bool
}

// SMTPSender performs implicit-TLS SMTP delivery on port 465. It never logs or
// stores the authorization code, and error messages never include the AUTH
// command line.
type SMTPSender struct {
	ConnectTimeout time.Duration
	TLSTimeout     time.Duration
	AuthTimeout    time.Duration
	CommandTimeout time.Duration
	DataTimeout    time.Duration
	TotalTimeout   time.Duration
	// DialContext and TLSConfig are injectable for fake TLS SMTP servers.
	DialContext func(ctx context.Context, network, address string) (net.Conn, error)
	TLSConfig   func(host string) *tls.Config
	Now         func() time.Time
}

func NewSMTPSender() *SMTPSender {
	return &SMTPSender{
		ConnectTimeout: 10 * time.Second,
		TLSTimeout:     10 * time.Second,
		AuthTimeout:    15 * time.Second,
		CommandTimeout: 15 * time.Second,
		DataTimeout:    60 * time.Second,
		TotalTimeout:   180 * time.Second,
		Now:            time.Now,
	}
}

func (sender *SMTPSender) now() time.Time {
	if sender != nil && sender.Now != nil {
		return sender.Now()
	}
	return time.Now()
}

func (sender *SMTPSender) dialContext() func(context.Context, string, string) (net.Conn, error) {
	if sender != nil && sender.DialContext != nil {
		return sender.DialContext
	}
	dialer := &net.Dialer{}
	return dialer.DialContext
}

func (sender *SMTPSender) tlsConfig(host string) *tls.Config {
	if sender != nil && sender.TLSConfig != nil {
		config := sender.TLSConfig(host)
		if config != nil {
			if config.ServerName == "" {
				config.ServerName = host
			}
			if config.MinVersion == 0 {
				config.MinVersion = tls.VersionTLS12
			}
			return config
		}
	}
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
}

func durationOrDefault(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

// smtpConn is a minimal SMTP command channel. It intentionally avoids
// net/smtp so per-RCPT responses and phase-specific error mapping are explicit.
type smtpConn struct {
	conn  net.Conn
	read  *bufio.Reader
	write *bufio.Writer
}

func newSMTPConn(conn net.Conn) *smtpConn {
	return &smtpConn{
		conn: conn, read: bufio.NewReader(conn), write: bufio.NewWriter(conn),
	}
}

func (session *smtpConn) setDeadline(now time.Time, timeout time.Duration) {
	if timeout > 0 {
		_ = session.conn.SetDeadline(now.Add(timeout))
	}
}

func (session *smtpConn) readResponse() (int, string, error) {
	code := 0
	lines := make([]string, 0, 4)
	for {
		line, err := session.read.ReadString('\n')
		if err != nil {
			return 0, "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if len(line) < 3 {
			return 0, "", fmt.Errorf("malformed SMTP response")
		}
		value, err := strconv.Atoi(line[:3])
		if err != nil {
			return 0, "", fmt.Errorf("malformed SMTP status")
		}
		if code == 0 {
			code = value
		} else if value != code {
			return 0, "", fmt.Errorf("inconsistent SMTP multiline status")
		}
		message := ""
		if len(line) > 4 {
			message = line[4:]
		}
		lines = append(lines, message)
		if len(line) == 3 || line[3] == ' ' {
			break
		}
	}
	return code, strings.Join(lines, " "), nil
}

func (session *smtpConn) command(line string) (int, string, error) {
	if _, err := session.write.WriteString(line + "\r\n"); err != nil {
		return 0, "", err
	}
	if err := session.write.Flush(); err != nil {
		return 0, "", err
	}
	return session.readResponse()
}

func (session *smtpConn) resetAndQuit() {
	_, _, _ = session.command("RSET")
	_, _, _ = session.command("QUIT")
	_ = session.conn.Close()
}

func senderResult(outcome domain.MailAttemptOutcome, code domain.MailErrorCode, smtpCode int, phase domain.MailSMTPPhase, response string) DeliveryResult {
	return DeliveryResult{
		Outcome: outcome, ErrorCode: code, SMTPCode: smtpCode,
		Phase: phase, Response: sanitizeSMTPResponse(response),
	}
}

func replyResult(phase domain.MailSMTPPhase, smtpCode int, message string) DeliveryResult {
	code := domain.MailErrSMTPPermanentFailure
	outcome := domain.MailAttemptPermanentFailure
	switch {
	case smtpCode == 421:
		code, outcome = domain.MailErrSMTPServiceUnavailable, domain.MailAttemptTransientFailure
	case smtpCode >= 500 && smtpCode <= 599:
		switch phase {
		case domain.MailSMTPPhaseAuth:
			code = domain.MailErrSMTPAuthFailed
		case domain.MailSMTPPhaseMailFrom:
			code = domain.MailErrSMTPSenderRejected
		case domain.MailSMTPPhaseRcptTo:
			code = domain.MailErrSMTPRecipientRejected
		case domain.MailSMTPPhaseData:
			switch smtpCode {
			case 552:
				code = domain.MailErrSMTPMessageTooLarge
			case 554:
				code = domain.MailErrSMTPContentRejected
			default:
				code = domain.MailErrSMTPPermanentFailure
			}
		default:
			switch smtpCode {
			case 500, 501, 502, 504:
				code = domain.MailErrSMTPProtocolError
			}
		}
	case smtpCode >= 400 && smtpCode <= 499:
		outcome = domain.MailAttemptTransientFailure
		switch phase {
		case domain.MailSMTPPhaseRcptTo:
			code = domain.MailErrSMTPRecipientTemporarilyUnavailable
		default:
			code = domain.MailErrSMTPTemporaryFailure
		}
	default:
		code = domain.MailErrSMTPProtocolError
	}
	return senderResult(outcome, code, smtpCode, phase, fmt.Sprintf("%d %s", smtpCode, message))
}

func connectionResult(phase domain.MailSMTPPhase, err error) DeliveryResult {
	if phase == domain.MailSMTPPhaseData {
		return senderResult(domain.MailAttemptUnknown, domain.MailErrSMTPAmbiguousAfterData, 0, phase, "")
	}
	code := domain.MailErrSMTPConnectionLost
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		code = domain.MailErrSMTPDNSFailed
	} else if isTimeoutError(err) {
		switch phase {
		case domain.MailSMTPPhaseConnect:
			code = domain.MailErrSMTPConnectTimeout
		case domain.MailSMTPPhaseTLS:
			code = domain.MailErrSMTPTLSFailed
		}
	}
	return senderResult(domain.MailAttemptTransientFailure, code, 0, phase, "")
}

func (sender *SMTPSender) hosts(account domain.MailAccount) ([]string, bool) {
	raw := strings.TrimSpace(account.SMTPHost)
	if raw == "" {
		raw = domain.MailDefaultSMTPHost
	}
	host, ok := NormalizeSMTPHost(raw)
	if !ok {
		return nil, false
	}
	hosts := []string{host}
	if host == domain.MailDefaultSMTPHost {
		hosts = append(hosts, domain.MailBackupSMTPHost)
	}
	return hosts, true
}

// Send delivers one message, trying the configured host and then the backup
// host only for DNS/TCP/TLS connection failures. Authentication failures never
// trigger a host switch.
func (sender *SMTPSender) Send(ctx context.Context, account domain.MailAccount, credential string, outgoing Outgoing, hooks DeliveryHooks) DeliveryResult {
	if ctx == nil {
		ctx = context.Background()
	}
	hosts, ok := sender.hosts(account)
	if !ok {
		return senderResult(domain.MailAttemptPermanentFailure, domain.MailErrAccountInvalid, 0, domain.MailSMTPPhaseConnect, "")
	}
	if account.SMTPPort != 0 && account.SMTPPort != domain.MailDefaultSMTPPort {
		return senderResult(domain.MailAttemptPermanentFailure, domain.MailErrAccountInvalid, 0, domain.MailSMTPPhaseConnect, "")
	}
	if account.SMTPSecurity != "" && account.SMTPSecurity != domain.MailSMTPSecuritySSL {
		return senderResult(domain.MailAttemptPermanentFailure, domain.MailErrAccountInvalid, 0, domain.MailSMTPPhaseConnect, "")
	}
	if strings.TrimSpace(credential) == "" {
		return senderResult(domain.MailAttemptPermanentFailure, domain.MailErrCredentialMissing, 0, domain.MailSMTPPhaseAuth, "")
	}
	totalTimeout := durationOrDefault(sender.totalTimeout(), 180*time.Second)
	sendCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	var last DeliveryResult
	for index, host := range hosts {
		result, failover := sender.sendToHost(sendCtx, host, account, credential, outgoing, hooks)
		last = result
		if !failover || index == len(hosts)-1 {
			return result
		}
	}
	return last
}

func (sender *SMTPSender) totalTimeout() time.Duration {
	if sender == nil {
		return 0
	}
	return sender.TotalTimeout
}

func (sender *SMTPSender) sendToHost(ctx context.Context, host string, account domain.MailAccount, credential string, outgoing Outgoing, hooks DeliveryHooks) (DeliveryResult, bool) {
	phase := domain.MailSMTPPhaseConnect
	enter := func(next domain.MailSMTPPhase) error {
		phase = next
		if hooks == nil {
			return nil
		}
		return hooks.EnterPhase(ctx, next)
	}
	aborted := func() DeliveryResult {
		result := senderResult(domain.MailAttemptTransientFailure, domain.MailErrSMTPLeaseExpiredBeforeData, 0, phase, "")
		result.Aborted = true
		return result
	}
	if err := enter(domain.MailSMTPPhaseConnect); err != nil {
		return aborted(), false
	}
	connectTimeout := durationOrDefault(sender.connectTimeout(), 10*time.Second)
	dialCtx, dialCancel := context.WithTimeout(ctx, connectTimeout)
	conn, err := sender.dialContext()(dialCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(domain.MailDefaultSMTPPort)))
	dialCancel()
	if err != nil {
		return connectionResult(domain.MailSMTPPhaseConnect, err), true
	}
	// The TLS layer owns the connection from this point.
	if err := enter(domain.MailSMTPPhaseTLS); err != nil {
		_ = conn.Close()
		return aborted(), false
	}
	tlsTimeout := durationOrDefault(sender.tlsTimeout(), 10*time.Second)
	tlsConn := tls.Client(conn, sender.tlsConfig(host))
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, tlsTimeout)
	err = tlsConn.HandshakeContext(handshakeCtx)
	handshakeCancel()
	if err != nil {
		_ = conn.Close()
		return senderResult(domain.MailAttemptTransientFailure, domain.MailErrSMTPTLSFailed, 0, domain.MailSMTPPhaseTLS, ""), true
	}
	session := newSMTPConn(tlsConn)
	defer session.conn.Close()
	commandTimeout := durationOrDefault(sender.commandTimeout(), 15*time.Second)

	session.setDeadline(sender.now(), commandTimeout)
	code, message, err := session.readResponse()
	if err != nil {
		return connectionResult(domain.MailSMTPPhaseConnect, err), false
	}
	if code != 220 {
		session.resetAndQuit()
		return replyResult(domain.MailSMTPPhaseConnect, code, message), false
	}
	session.setDeadline(sender.now(), commandTimeout)
	code, message, err = session.command("EHLO localhost")
	if err != nil {
		return connectionResult(domain.MailSMTPPhaseConnect, err), false
	}
	if code != 250 {
		session.resetAndQuit()
		return replyResult(domain.MailSMTPPhaseConnect, code, message), false
	}

	if err := enter(domain.MailSMTPPhaseAuth); err != nil {
		return aborted(), false
	}
	session.setDeadline(sender.now(), durationOrDefault(sender.authTimeout(), 15*time.Second))
	authResult, authOK := authenticate(session, account, credential)
	if !authOK {
		session.resetAndQuit()
		return authResult, false
	}

	if err := enter(domain.MailSMTPPhaseMailFrom); err != nil {
		return aborted(), false
	}
	session.setDeadline(sender.now(), commandTimeout)
	code, message, err = session.command(fmt.Sprintf("MAIL FROM:<%s>", outgoing.FromAddress))
	if err != nil {
		return connectionResult(domain.MailSMTPPhaseMailFrom, err), false
	}
	if code != 250 {
		session.resetAndQuit()
		return replyResult(domain.MailSMTPPhaseMailFrom, code, message), false
	}

	if err := enter(domain.MailSMTPPhaseRcptTo); err != nil {
		return aborted(), false
	}
	recipients := make([]string, 0, len(outgoing.To)+len(outgoing.CC)+len(outgoing.BCC))
	recipients = append(recipients, outgoing.To...)
	recipients = append(recipients, outgoing.CC...)
	recipients = append(recipients, outgoing.BCC...)
	if len(recipients) == 0 {
		session.resetAndQuit()
		return senderResult(domain.MailAttemptPermanentFailure, domain.MailErrRecipientInvalid, 0, domain.MailSMTPPhaseRcptTo, ""), false
	}
	var firstRejected *DeliveryResult
	var firstTemporary *DeliveryResult
	accepted := 0
	for _, recipient := range recipients {
		session.setDeadline(sender.now(), commandTimeout)
		code, message, err = session.command(fmt.Sprintf("RCPT TO:<%s>", recipient))
		if err != nil {
			session.resetAndQuit()
			return connectionResult(domain.MailSMTPPhaseRcptTo, err), false
		}
		if code >= 500 && code <= 599 {
			result := replyResult(domain.MailSMTPPhaseRcptTo, code, message)
			if firstRejected == nil {
				firstRejected = &result
			}
			continue
		}
		if code >= 400 && code <= 499 {
			result := replyResult(domain.MailSMTPPhaseRcptTo, code, message)
			if firstTemporary == nil {
				firstTemporary = &result
			}
			continue
		}
		if code < 200 || code > 299 {
			session.resetAndQuit()
			return senderResult(domain.MailAttemptPermanentFailure, domain.MailErrSMTPProtocolError, code, domain.MailSMTPPhaseRcptTo, ""), false
		}
		accepted++
	}
	if firstRejected != nil {
		// Any 5xx rejects the whole message. Never send DATA to a subset.
		session.resetAndQuit()
		return *firstRejected, false
	}
	if firstTemporary != nil {
		session.resetAndQuit()
		return *firstTemporary, false
	}
	if accepted != len(recipients) {
		session.resetAndQuit()
		return senderResult(domain.MailAttemptPermanentFailure, domain.MailErrSMTPProtocolError, 0, domain.MailSMTPPhaseRcptTo, ""), false
	}

	messageBytes, err := BuildMessage(outgoing)
	if err != nil {
		session.resetAndQuit()
		if mailErr, ok := CodeOf(err); ok {
			return senderResult(domain.MailAttemptPermanentFailure, mailErr, 0, domain.MailSMTPPhaseRcptTo, ""), false
		}
		return senderResult(domain.MailAttemptPermanentFailure, domain.MailErrSMTPPermanentFailure, 0, domain.MailSMTPPhaseRcptTo, ""), false
	}

	// Entering DATA is the single mutual-exclusion point with lease recovery.
	// No DATA may be written unless the conditional phase update succeeded.
	if hooks != nil {
		if err := hooks.BeforeData(ctx); err != nil {
			return aborted(), false
		}
	}
	phase = domain.MailSMTPPhaseData
	session.setDeadline(sender.now(), commandTimeout)
	code, message, err = session.command("DATA")
	if err != nil {
		return connectionResult(domain.MailSMTPPhaseData, err), false
	}
	if code != 354 {
		session.resetAndQuit()
		return replyResult(domain.MailSMTPPhaseData, code, message), false
	}
	session.setDeadline(sender.now(), durationOrDefault(sender.dataTimeout(), 60*time.Second))
	if err := writeDotStuffed(session.write, messageBytes); err != nil {
		return connectionResult(domain.MailSMTPPhaseData, err), false
	}
	if err := session.write.Flush(); err != nil {
		return connectionResult(domain.MailSMTPPhaseData, err), false
	}
	code, message, err = session.readResponse()
	if err != nil {
		// The server received DATA but no final response arrived. The outcome is
		// unknowable; never retry automatically.
		return connectionResult(domain.MailSMTPPhaseData, err), false
	}
	if code >= 200 && code <= 299 {
		_, _, _ = session.command("QUIT")
		return senderResult(domain.MailAttemptSent, "", code, domain.MailSMTPPhaseFinished, fmt.Sprintf("%d %s", code, message)), false
	}
	session.resetAndQuit()
	return replyResult(domain.MailSMTPPhaseData, code, message), false
}

func (sender *SMTPSender) connectTimeout() time.Duration {
	if sender == nil {
		return 0
	}
	return sender.ConnectTimeout
}

func (sender *SMTPSender) tlsTimeout() time.Duration {
	if sender == nil {
		return 0
	}
	return sender.TLSTimeout
}

func (sender *SMTPSender) authTimeout() time.Duration {
	if sender == nil {
		return 0
	}
	return sender.AuthTimeout
}

func (sender *SMTPSender) commandTimeout() time.Duration {
	if sender == nil {
		return 0
	}
	return sender.CommandTimeout
}

func (sender *SMTPSender) dataTimeout() time.Duration {
	if sender == nil {
		return 0
	}
	return sender.DataTimeout
}

// authenticate tries AUTH PLAIN first and then AUTH LOGIN. A credential value
// is never included in a returned error or response.
func authenticate(session *smtpConn, account domain.MailAccount, credential string) (DeliveryResult, bool) {
	username := strings.TrimSpace(account.SMTPUsername)
	if username == "" {
		username = strings.TrimSpace(account.Address)
	}
	plain := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + credential))
	code, message, err := session.command("AUTH PLAIN " + plain)
	if err == nil {
		if code == 235 {
			return DeliveryResult{}, true
		}
		if code == 334 {
			code, message, err = session.command(plain)
			if err == nil && code == 235 {
				return DeliveryResult{}, true
			}
		}
	}
	if err != nil {
		return connectionResult(domain.MailSMTPPhaseAuth, err), false
	}
	if code == 421 {
		return replyResult(domain.MailSMTPPhaseAuth, code, message), false
	}

	// Authentication failure is permanent; a LOGIN fallback is attempted only
	// because some servers do not advertise or accept PLAIN.
	code, message, err = session.command("AUTH LOGIN")
	if err != nil {
		return connectionResult(domain.MailSMTPPhaseAuth, err), false
	}
	if code != 334 {
		return replyResult(domain.MailSMTPPhaseAuth, code, message), false
	}
	code, message, err = session.command(base64.StdEncoding.EncodeToString([]byte(username)))
	if err != nil {
		return connectionResult(domain.MailSMTPPhaseAuth, err), false
	}
	if code != 334 {
		return replyResult(domain.MailSMTPPhaseAuth, code, message), false
	}
	code, message, err = session.command(base64.StdEncoding.EncodeToString([]byte(credential)))
	if err != nil {
		return connectionResult(domain.MailSMTPPhaseAuth, err), false
	}
	if code == 235 {
		return DeliveryResult{}, true
	}
	return replyResult(domain.MailSMTPPhaseAuth, code, message), false
}

func sanitizeSMTPResponse(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}

// writeDotStuffed writes the message body followed by the SMTP terminator and
// escapes leading dots at the start of a line.
func writeDotStuffed(writer *bufio.Writer, data []byte) error {
	lineStart := true
	for _, value := range data {
		if lineStart && value == '.' {
			if err := writer.WriteByte('.'); err != nil {
				return err
			}
		}
		if err := writer.WriteByte(value); err != nil {
			return err
		}
		lineStart = value == '\n'
	}
	if !lineStart {
		if _, err := writer.WriteString("\r\n"); err != nil {
			return err
		}
	}
	_, err := writer.WriteString(".\r\n")
	return err
}

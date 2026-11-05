package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const fakeSMTPHost = "smtp.qiye.163.com"

func newFakeCertificate(t *testing.T, host string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// fakeSMTPFixture starts a loopback SMTP-over-TLS server. The certificate is
// issued for fakeSMTPHost, so the real hostname/certificate verification path
// is exercised; only DNS and TCP are replaced by the local listener.
type fakeSMTPFixture struct {
	tester *SMTPTester
	addr   string
}

func newFakeSMTPFixture(t *testing.T, mode string) *fakeSMTPFixture {
	t.Helper()
	certificate, pool := newFakeCertificate(t, fakeSMTPHost)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveFakeSMTP(connection, certificate, mode)
		}
	}()
	address := listener.Addr().String()
	tester := &SMTPTester{
		ConnectTimeout: time.Second,
		TLSTimeout:     time.Second,
		CommandTimeout: time.Second,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			if mode == "timeout" {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			dialer := &net.Dialer{}
			return dialer.DialContext(ctx, network, address)
		},
		TLSConfig: func(host string) *tls.Config {
			return &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12}
		},
	}
	return &fakeSMTPFixture{tester: tester, addr: address}
}

func serveFakeSMTP(connection net.Conn, certificate tls.Certificate, mode string) {
	defer connection.Close()
	if mode == "tlsfail" {
		// Closing before the handshake makes the client observe a TLS failure.
		return
	}
	server := tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{certificate}})
	defer server.Close()
	if err := server.Handshake(); err != nil {
		return
	}
	reader := bufio.NewReader(server)
	write := func(line string) { _, _ = server.Write([]byte(line + "\r\n")) }
	write("220 fake ESMTP ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"):
			write("250-fake.example.test")
			write("250-AUTH PLAIN LOGIN")
			write("250 SIZE 10485760")
		case strings.HasPrefix(command, "AUTH"):
			if mode == "authfail" {
				write("535 5.7.8 Authentication failed")
			} else {
				write("235 2.7.0 Authentication successful")
			}
		case command == "QUIT":
			write("221 Bye")
			return
		default:
			write("250 OK")
		}
	}
}

func testAccount() domain.MailAccount {
	return domain.MailAccount{
		ID: "mail_account_test", Address: "noreply@example.com",
		SMTPHost: fakeSMTPHost, SMTPPort: 465, SMTPSecurity: "ssl",
		SMTPUsername: "noreply@example.com",
	}
}

func TestSMTPTesterSuccess(t *testing.T) {
	fixture := newFakeSMTPFixture(t, "success")
	result := fixture.tester.Test(context.Background(), testAccount(), "fake-authorization-code")
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	if result.Auth != "ok" || result.TLSVersion == "" || result.ErrorCode != "" {
		t.Fatalf("result = %+v", result)
	}
	if result.Host != fakeSMTPHost || result.Port != 465 {
		t.Fatalf("host/port = %s:%d", result.Host, result.Port)
	}
}

func TestSMTPTesterAuthFailure(t *testing.T) {
	fixture := newFakeSMTPFixture(t, "authfail")
	result := fixture.tester.Test(context.Background(), testAccount(), "wrong-code")
	if result.OK || result.ErrorCode != domain.MailErrSMTPAuthFailed {
		t.Fatalf("result = %+v", result)
	}
	if result.Level != "auth" || result.Retryable {
		t.Fatalf("result = %+v", result)
	}
	if strings.Contains(result.Message, "wrong-code") {
		t.Fatal("credential leaked into result message")
	}
}

func TestSMTPTesterTLSFailure(t *testing.T) {
	fixture := newFakeSMTPFixture(t, "tlsfail")
	result := fixture.tester.Test(context.Background(), testAccount(), "fake-authorization-code")
	if result.OK || result.ErrorCode != domain.MailErrSMTPTLSFailed {
		t.Fatalf("result = %+v", result)
	}
	if result.Level != "tls" {
		t.Fatalf("level = %q", result.Level)
	}
}

func TestSMTPTesterConnectTimeout(t *testing.T) {
	fixture := newFakeSMTPFixture(t, "timeout")
	fixture.tester.ConnectTimeout = 30 * time.Millisecond
	result := fixture.tester.Test(context.Background(), testAccount(), "fake-authorization-code")
	if result.OK || result.ErrorCode != domain.MailErrSMTPConnectTimeout {
		t.Fatalf("result = %+v", result)
	}
	if !result.Retryable {
		t.Fatalf("connect timeout should be retryable: %+v", result)
	}
}

func TestSMTPTesterMissingCredential(t *testing.T) {
	tester := NewSMTPTester()
	result := tester.Test(context.Background(), testAccount(), "")
	if result.OK || result.ErrorCode != domain.MailErrCredentialMissing {
		t.Fatalf("result = %+v", result)
	}
}

func TestSMTPTesterRejectsNonAllowlistedHost(t *testing.T) {
	dialed := false
	tester := &SMTPTester{
		ConnectTimeout: time.Second,
		TLSTimeout:     time.Second,
		CommandTimeout: time.Second,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, errors.New("must not dial")
		},
	}
	account := testAccount()
	account.SMTPHost = "169.254.169.254"
	result := tester.Test(context.Background(), account, "fake-authorization-code")
	if result.OK || result.ErrorCode != domain.MailErrAccountInvalid {
		t.Fatalf("result = %+v", result)
	}
	if dialed {
		t.Fatal("non-allowlisted host was dialed")
	}
}

func TestDefaultTLSConfigIsSecure(t *testing.T) {
	config := (&SMTPTester{}).tlsConfig(fakeSMTPHost)
	if config.InsecureSkipVerify {
		t.Fatal("default TLS config must verify certificates")
	}
	if config.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x, want TLS 1.2", config.MinVersion)
	}
	if config.ServerName != fakeSMTPHost {
		t.Fatalf("ServerName = %q", config.ServerName)
	}
}

func TestSMTPTesterDefaultTLSRejectsSelfSignedCertificate(t *testing.T) {
	certificate, _ := newFakeCertificate(t, fakeSMTPHost)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveFakeSMTP(connection, certificate, "success")
		}
	}()
	address := listener.Addr().String()
	tester := &SMTPTester{
		ConnectTimeout: time.Second,
		TLSTimeout:     time.Second,
		CommandTimeout: time.Second,
		// TLSConfig deliberately nil: exercise the production default.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := &net.Dialer{}
			return dialer.DialContext(ctx, network, address)
		},
	}
	result := tester.Test(context.Background(), testAccount(), "fake-authorization-code")
	if result.OK || result.ErrorCode != domain.MailErrSMTPTLSFailed {
		t.Fatalf("result = %+v", result)
	}
}

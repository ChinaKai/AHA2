// Package mailtest provides a fake implicit-TLS SMTP server for tests. It
// speaks enough of RFC 5321 to exercise the real TLS handshake and every phase
// the mail worker must map.
package mailtest

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Script controls the fake server's responses. Empty fields use the happy path.
type Script struct {
	Greeting        string
	AuthMode        string // "", "login_only", "fail"
	MailFromReply   string
	RcptReplies     func(recipient string) string
	DataReply       string // response to DATA; empty means 354
	FinalReply      string // response after the message body; empty means 250
	CloseAfterRcpt  bool
	CloseOnRcpt     bool
	CloseBeforeData bool
	CloseAfterData  bool
	DataDelay       time.Duration
	OnData          func()
}

type Server struct {
	Addr      string
	TLSConfig *tls.Config
	RootCAs   *x509.CertPool

	mu          sync.Mutex
	connections int
	commands    []string
	rcptCalls   []string
	authLines   []string
	messages    []string
	dataSeen    int
	done        chan struct{}
	listener    net.Listener
}

func (server *Server) Close() {
	if server.listener != nil {
		_ = server.listener.Close()
	}
	<-server.done
}

func (server *Server) Connections() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.connections
}

func (server *Server) Commands() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string{}, server.commands...)
}

func (server *Server) RcptCalls() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string{}, server.rcptCalls...)
}

func (server *Server) AuthLines() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string{}, server.authLines...)
}

func (server *Server) Messages() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string{}, server.messages...)
}

func (server *Server) DataSeen() int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.dataSeen
}

func (server *Server) record(command string) {
	server.mu.Lock()
	server.commands = append(server.commands, command)
	server.mu.Unlock()
}

func generateCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake smtp"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"smtp.qiye.163.com", "smtphz.qiye.163.com", "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool.AddCert(parsed)
	return certificate, pool
}

// Start launches a TLS SMTP listener on 127.0.0.1:0.
func Start(t *testing.T, script Script) *Server {
	t.Helper()
	certificate, pool := generateCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		Addr: listener.Addr().String(), RootCAs: pool,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
		done:      make(chan struct{}),
		listener:  listener,
	}
	t.Cleanup(server.Close)
	go server.accept(script)
	return server
}

func (server *Server) accept(script Script) {
	defer close(server.done)
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			return
		}
		server.mu.Lock()
		server.connections++
		server.mu.Unlock()
		go server.handle(conn, script)
	}
}

func reply(writer *bufio.Writer, value string) bool {
	if value == "" {
		return true
	}
	if _, err := writer.WriteString(value + "\r\n"); err != nil {
		return false
	}
	return writer.Flush() == nil
}

func (server *Server) handle(raw net.Conn, script Script) {
	tlsConn := tls.Server(raw, server.TLSConfig)
	defer tlsConn.Close()
	_ = tlsConn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	reader := bufio.NewReader(tlsConn)
	writer := bufio.NewWriter(tlsConn)
	greeting := script.Greeting
	if greeting == "" {
		greeting = "220 fake ESMTP"
	}
	if !reply(writer, greeting) {
		return
	}
	authenticated := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		server.record(line)
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if !reply(writer, "250-fake") || !reply(writer, "250-AUTH PLAIN LOGIN") || !reply(writer, "250 OK") {
				return
			}
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			server.mu.Lock()
			server.authLines = append(server.authLines, line)
			server.mu.Unlock()
			switch script.AuthMode {
			case "login_only":
				if !reply(writer, "504 AUTH PLAIN not supported") {
					return
				}
			case "fail":
				if !reply(writer, "535 authentication failed") {
					return
				}
			default:
				authenticated = true
				if !reply(writer, "235 2.7.0 authentication successful") {
					return
				}
			}
		case strings.HasPrefix(upper, "AUTH LOGIN"):
			server.mu.Lock()
			server.authLines = append(server.authLines, line)
			server.mu.Unlock()
			if !reply(writer, "334 VXNlcm5hbWU6") {
				return
			}
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
			if !reply(writer, "334 UGFzc3dvcmQ6") {
				return
			}
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
			if script.AuthMode == "fail" {
				if !reply(writer, "535 authentication failed") {
					return
				}
				continue
			}
			authenticated = true
			if !reply(writer, "235 2.7.0 authentication successful") {
				return
			}
		case strings.HasPrefix(upper, "MAIL FROM"):
			if !authenticated {
				if !reply(writer, "530 authentication required") {
					return
				}
				continue
			}
			value := script.MailFromReply
			if value == "" {
				value = "250 2.1.0 OK"
			}
			if !reply(writer, value) {
				return
			}
		case strings.HasPrefix(upper, "RCPT TO"):
			recipient := strings.TrimSpace(line[len("RCPT TO:"):])
			server.mu.Lock()
			server.rcptCalls = append(server.rcptCalls, recipient)
			server.mu.Unlock()
			if script.CloseOnRcpt {
				return
			}
			value := "250 2.1.5 OK"
			if script.RcptReplies != nil {
				value = script.RcptReplies(recipient)
			}
			if !reply(writer, value) {
				return
			}
			if script.CloseAfterRcpt {
				return
			}
		case upper == "DATA":
			server.mu.Lock()
			server.dataSeen++
			server.mu.Unlock()
			if script.OnData != nil {
				script.OnData()
			}
			if script.CloseBeforeData {
				return
			}
			if script.DataReply != "" && !strings.HasPrefix(script.DataReply, "354") {
				if !reply(writer, script.DataReply) {
					return
				}
				continue
			}
			if !reply(writer, "354 End data with <CR><LF>.<CR><LF>") {
				return
			}
			var message strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" || dataLine == ".\n" {
					break
				}
				if strings.HasPrefix(dataLine, "..") {
					dataLine = dataLine[1:]
				}
				message.WriteString(dataLine)
			}
			server.mu.Lock()
			server.messages = append(server.messages, message.String())
			server.mu.Unlock()
			if script.DataDelay > 0 {
				time.Sleep(script.DataDelay)
			}
			if script.CloseAfterData {
				return
			}
			final := script.FinalReply
			if final == "" {
				final = "250 2.0.0 queued"
			}
			if !reply(writer, final) {
				return
			}
		case upper == "RSET":
			if !reply(writer, "250 2.0.0 OK") {
				return
			}
		case upper == "QUIT":
			_ = reply(writer, "221 2.0.0 bye")
			return
		default:
			if !reply(writer, "250 2.0.0 OK") {
				return
			}
		}
	}
}

package hardware

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func privateFixtureSecret(t *testing.T) string {
	t.Helper()
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal("fixture secret generation failed")
	}
	return hex.EncodeToString(value[:])
}

func TestSecretStreamProtectsEveryChunkBoundaryAndFinalPrefix(t *testing.T) {
	secret := privateFixtureSecret(t)
	for split := 0; split <= len(secret); split++ {
		filter := newSecretStream(secret)
		output := filter.Feed([]byte("before:" + secret[:split]))
		output = append(output, filter.Feed([]byte(secret[split:]+":after"))...)
		output = append(output, filter.Finish()...)
		if string(output) != "before:"+secretMask+":after" || bytes.Contains(output, []byte(secret)) {
			t.Fatal("credential escaped across chunk boundary")
		}
	}
	filter := newSecretStream(secret)
	var output []byte
	for _, value := range []byte(secret + secret) {
		output = append(output, filter.Feed([]byte{value})...)
	}
	output = append(output, filter.Finish()...)
	if string(output) != secretMask+secretMask {
		t.Fatal("single-byte/repeated echo was not masked")
	}
	filter = newSecretStream(secret)
	output = filter.Feed([]byte(secret[:len(secret)-1]))
	output = append(output, filter.Finish()...)
	if string(output) != secretMask {
		t.Fatal("EOF released a credential prefix")
	}
}

type oneByteReadConn struct{ net.Conn }

func (connection oneByteReadConn) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	return connection.Conn.Read(buffer)
}

type privateLoginFixture struct {
	manager  *Manager
	recorder *memoryRecorder
	peer     net.Conn
	secret   string
	writes   sync.Mutex
	sends    atomic.Int32
	quiet    atomic.Bool // receive credentials without the echo/shell reply
	outputMu sync.Mutex
	output   []byte
}

func newPrivateLoginFixture(t *testing.T) *privateLoginFixture {
	t.Helper()
	return newPrivateLoginFixtureReads(t, true)
}

// oneByte forces every chunk boundary; false keeps each device write as one
// read, the shape where several prompts arrive together.
func newPrivateLoginFixtureReads(t *testing.T, oneByte bool) *privateLoginFixture {
	t.Helper()
	recorder := &memoryRecorder{}
	manager := NewManager(recorder)
	client, peer := net.Pipe()
	manager.dialNetwork = func(context.Context, string, string) (net.Conn, error) {
		if oneByte {
			return oneByteReadConn{client}, nil
		}
		return client, nil
	}
	f := &privateLoginFixture{manager: manager, recorder: recorder, peer: peer, secret: privateFixtureSecret(t)}
	t.Cleanup(func() { _ = manager.Close(); _ = peer.Close() })
	group := domain.HardwareGroup{ID: "board", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "127.0.0.1", Port: 12345, Protocol: domain.HardwareProtocolRaw}, Access: domain.HardwareAccessReadWrite}
	if _, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task", HardwareID: "board", Transport: "network", Group: group, Password: f.secret}); err != nil {
		t.Fatal("fake transport connect failed")
	}
	live, unsubscribe, err := manager.Subscribe("task", "board", "network")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unsubscribe)
	go func() {
		for event := range live {
			if event.Type == "output" {
				f.outputMu.Lock()
				f.output = append(f.output, event.Data...)
				f.outputMu.Unlock()
			}
		}
	}()
	go func() {
		reader := bufio.NewReader(peer)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimSpace(line) == f.secret {
				f.sends.Add(1)
				if !f.quiet.Load() {
					f.write(t, []byte("echo:"+f.secret+"\n$ "))
				}
			}
		}
	}()
	return f
}

func (f *privateLoginFixture) write(t *testing.T, data []byte) {
	t.Helper()
	f.writes.Lock()
	defer f.writes.Unlock()
	_ = f.peer.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := f.peer.Write(data); err != nil {
		return
	}
}

func (f *privateLoginFixture) waitHistory(t *testing.T, marker string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.recorder.mu.Lock()
		var joined strings.Builder
		for _, event := range f.recorder.events {
			if event.Direction == "rx" {
				joined.WriteString(event.Data)
			}
		}
		found := strings.Contains(joined.String(), marker)
		leaked := strings.Contains(joined.String(), f.secret)
		f.recorder.mu.Unlock()
		if leaked {
			t.Fatal("credential persisted in rx history")
		}
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("fake device marker did not reach sanitized history")
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *privateLoginFixture) waitLive(t *testing.T, marker string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.outputMu.Lock()
		found := bytes.Contains(f.output, []byte(marker))
		f.outputMu.Unlock()
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("sanitized live subscriber did not receive marker")
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *privateLoginFixture) assertPrivate(t *testing.T) {
	t.Helper()
	f.recorder.mu.Lock()
	for _, event := range f.recorder.events {
		if strings.Contains(event.Data, f.secret) {
			f.recorder.mu.Unlock()
			t.Fatal("credential persisted in a terminal record")
		}
	}
	f.recorder.mu.Unlock()
	f.outputMu.Lock()
	leaked := bytes.Contains(f.output, []byte(f.secret))
	f.outputMu.Unlock()
	if leaked {
		t.Fatal("credential reached the live subscriber")
	}
}

func TestLoginEchoIsMaskedAndAuthenticatedLoginCannotRearm(t *testing.T) {
	f := newPrivateLoginFixture(t)
	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 3}); err != nil {
		t.Fatal(err)
	}
	f.write(t, []byte("password:"))
	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "authenticated")
	f.waitHistory(t, secretMask)
	if f.sends.Load() != 1 {
		t.Fatal("initial login did not send exactly one credential")
	}
	if _, err := f.manager.Login("task", "board", "network", LoginOptions{Wakeup: true, Retries: 3}); err != nil {
		t.Fatal(err)
	}
	f.write(t, []byte("password:\nPOST-AUTH-MARKER\n"))
	f.waitHistory(t, "POST-AUTH-MARKER")
	f.waitLive(t, "POST-AUTH-MARKER")
	if f.sends.Load() != 1 {
		t.Fatal("authenticated replay sent the credential again")
	}
	status := f.manager.Status("task", "board", "network", false)
	if status.LoginStatus != "authenticated" {
		t.Fatal("idempotent login reset authenticated state")
	}
	f.assertPrivate(t)
}

func TestLoginFailureTimeoutAndCancelDisarmAndPermitExplicitBudgetedRetry(t *testing.T) {
	for _, outcome := range []string{"failed", "timeout", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			f := newPrivateLoginFixture(t)
			if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 3}); err != nil {
				t.Fatal(err)
			}
			switch outcome {
			case "failed":
				f.write(t, []byte("access denied\n"))
			case "cancelled":
				if _, err := f.manager.Login("task", "board", "network", LoginOptions{Cancel: true}); err != nil {
					t.Fatal(err)
				}
			}
			waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", outcome)
			f.write(t, []byte("password:\nDISARMED-MARKER\n"))
			f.waitHistory(t, "DISARMED-MARKER")
			f.waitLive(t, "DISARMED-MARKER")
			if f.sends.Load() != 0 {
				t.Fatal("a disarmed attempt sent a credential")
			}
			if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 3}); err != nil {
				t.Fatal("explicit retry within budget was refused")
			}
			f.write(t, []byte("password:"))
			waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "authenticated")
			if f.sends.Load() != 1 {
				t.Fatal("explicit retry did not authenticate exactly once")
			}
			f.assertPrivate(t)
		})
	}
}

func TestFailureAndFreshPromptInOneReadReportFailureWithoutResend(t *testing.T) {
	f := newPrivateLoginFixtureReads(t, false)
	f.quiet.Store(true)
	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 30, Retries: 3}); err != nil {
		t.Fatal(err)
	}
	f.write(t, []byte("password:"))
	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "credentials_sent")
	deadline := time.Now().Add(3 * time.Second)
	for f.sends.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("fake device did not receive the credential")
		}
		time.Sleep(time.Millisecond)
	}
	f.write(t, []byte("\r\nLogin incorrect\r\n\r\nboard login: password:"))
	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "failed")
	if f.sends.Load() != 1 {
		t.Fatal("failure with a fresh prompt resent the credential")
	}
	f.assertPrivate(t)
}

func TestBannerMentioningFailureBeforeAnyCredentialDoesNotAbortLogin(t *testing.T) {
	f := newPrivateLoginFixtureReads(t, false)
	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 30, Retries: 3}); err != nil {
		t.Fatal(err)
	}
	f.write(t, []byte("NOTICE: access denied to unauthorized users\r\npassword:"))
	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "authenticated")
	if f.sends.Load() != 1 {
		t.Fatal("banner text changed the credential flow")
	}
	f.assertPrivate(t)
}

func waitForHardwareLoginStatusAt(t *testing.T, manager *Manager, task, id, transport, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for manager.Status(task, id, transport, false).LoginStatus != want {
		if time.Now().After(deadline) {
			t.Fatal("login did not reach expected state")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHistoryDefenseMasksRetainedSplitEventsWithoutChangingCursors(t *testing.T) {
	secret := privateFixtureSecret(t)
	cut := len(secret) / 2
	page := domain.HardwareIOPage{LatestSequence: 3, HasMore: true, Items: []domain.HardwareIOEvent{{Sequence: 1, Transport: "network", Direction: "rx", Data: secret[:cut]}, {Sequence: 2, Transport: "serial", Direction: "rx", Data: "ordinary"}, {Sequence: 3, Transport: "network", Direction: "rx", Data: secret[cut:]}}}
	view := RedactHistory(page, secret)
	var joined strings.Builder
	for _, item := range view.Items {
		joined.WriteString(item.Data)
	}
	if strings.Contains(joined.String(), secret) || !strings.Contains(joined.String(), secretMask) || view.LatestSequence != page.LatestSequence || !view.HasMore {
		t.Fatal("history defense leaked credential or changed cursors")
	}
	if page.Items[0].Data != secret[:cut] {
		t.Fatal("read projection modified its input records")
	}
}

func TestHistoryDefenseCannotReassembleCredentialAcrossPages(t *testing.T) {
	secret := privateFixtureSecret(t)
	// Fixture text has no lowercase hex, so any hex byte in a view is a leak.
	leaked := func(value string) bool { return strings.ContainsAny(value, "0123456789abcdef") }
	for _, direction := range []string{"rx", "tx"} {
		for first := 1; first < len(secret); first++ {
			for _, second := range []int{first, (first + len(secret)) / 2, len(secret) - 1, len(secret)} {
				var items []domain.HardwareIOEvent
				for _, part := range []string{"PRE:" + secret[:first], secret[first:second], secret[second:] + "\nPOST\n"} {
					if part != "" {
						items = append(items, domain.HardwareIOEvent{Sequence: int64(len(items) + 1), Transport: "network", Direction: direction, Data: part})
					}
				}
				for size := 1; size <= len(items); size++ {
					var joined strings.Builder
					for start := 0; start < len(items); start += size {
						window := domain.HardwareIOPage{Items: items[start:min(start+size, len(items))]}
						for _, item := range RedactHistory(window, secret).Items {
							joined.WriteString(item.Data)
						}
					}
					if leaked(joined.String()) || !strings.HasPrefix(joined.String(), "PRE:") || !strings.HasSuffix(joined.String(), "\nPOST\n") {
						t.Fatal("paged history revealed part of a credential or lost ordinary output")
					}
				}
			}
		}
	}
	keystrokes := make([]domain.HardwareIOEvent, 0, len(secret))
	for index := range secret {
		keystrokes = append(keystrokes, domain.HardwareIOEvent{Sequence: int64(index + 1), Transport: "network", Direction: "tx", Data: secret[index : index+1]})
	}
	for start := range keystrokes {
		for _, item := range RedactHistory(domain.HardwareIOPage{Items: keystrokes[start : start+1]}, secret).Items {
			if leaked(item.Data) {
				t.Fatal("single keystroke page revealed a credential byte")
			}
		}
	}
	ordinary := domain.HardwareIOPage{Items: []domain.HardwareIOEvent{{Sequence: 9, Transport: "network", Direction: "rx", Data: "ORDINARY OUTPUT\n"}}}
	if view := RedactHistory(ordinary, secret); view.Items[0].Data != "ORDINARY OUTPUT\n" {
		t.Fatal("unrelated page start was masked")
	}
}

type credentialErrorRecorder struct{ secret string }

func (r credentialErrorRecorder) AppendHardwareIO(context.Context, domain.HardwareIOEvent) (int64, error) {
	return 0, errors.New("fixture storage error " + r.secret)
}

func TestKnownCredentialDoesNotEscapeConnectionErrorsStatusOrLogs(t *testing.T) {
	secret := privateFixtureSecret(t)
	manager := NewManager(nil)
	manager.openSSH = func(context.Context, string, string, string, string) (io.ReadWriteCloser, error) {
		return nil, errors.New("fixture connect error " + secret)
	}
	group := domain.HardwareGroup{ID: "board", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "127.0.0.1", Port: 22, Protocol: domain.HardwareProtocolSSH}, Access: domain.HardwareAccessReadWrite}
	_, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task", HardwareID: "board", Transport: "network", Group: group, Password: secret})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("connection error exposed credential")
	}
	var logs bytes.Buffer
	manager = NewManager(credentialErrorRecorder{secret: secret})
	manager.logger = slog.New(slog.NewTextHandler(&logs, nil))
	client, peer := net.Pipe()
	defer peer.Close()
	defer manager.Close()
	manager.dialNetwork = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	group.Network.Protocol = domain.HardwareProtocolRaw
	status, err := manager.Connect(context.Background(), ConnectRequest{TaskID: "task", HardwareID: "board", Transport: "network", Group: group, Password: secret})
	if err != nil || strings.Contains(status.RecordError, secret) || strings.Contains(logs.String(), secret) {
		t.Fatal("recording failure exposed credential in status/log")
	}
}

func TestDisconnectDisarmsWriterEvenWhileReadOnlyObserverRemains(t *testing.T) {
	f := newPrivateLoginFixture(t)
	group := domain.HardwareGroup{ID: "board", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "127.0.0.1", Port: 12345, Protocol: domain.HardwareProtocolRaw}, Access: domain.HardwareAccessReadWrite}
	if _, err := f.manager.Connect(context.Background(), ConnectRequest{TaskID: "observer", HardwareID: "board", Transport: "network", Group: group, Password: f.secret, ReadOnly: true}); err != nil {
		t.Fatal("observer fixture failed")
	}
	f.manager.Disconnect("task", "board", "network")
	if !f.manager.Status("observer", "board", "network", true).Connected {
		t.Fatal("disconnect dropped read-only observer connection")
	}
	f.write(t, []byte("password:\nOBSERVER-MARKER\n"))
	f.waitHistory(t, "OBSERVER-MARKER")
	deadline := time.Now().Add(3 * time.Second)
	for f.manager.Status("observer", "board", "network", true).LoginStatus != "cancelled" {
		if time.Now().After(deadline) {
			t.Fatal("writer disconnect did not disarm login")
		}
		time.Sleep(time.Millisecond)
	}
	if f.sends.Load() != 0 {
		t.Fatal("detached writer sent a credential")
	}
	if _, err := f.manager.Login("task", "board", "network", LoginOptions{}); !errors.Is(err, ErrNotConnected) {
		t.Fatal("disconnected writer rearmed login")
	}
}

func TestLoginReplayIsIdempotentAndExplicitRetriesHaveFiniteBudget(t *testing.T) {
	f := newPrivateLoginFixture(t)
	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 2}); err != nil {
		t.Fatal(err)
	}
	f.manager.mu.Lock()
	session := f.manager.attachments[attachmentKey("task", "board", "network")]
	f.manager.mu.Unlock()
	session.mu.RLock()
	before := session.loginGeneration
	session.mu.RUnlock()
	if _, err := f.manager.Login("task", "board", "network", LoginOptions{Wakeup: true, Retries: 2}); err != nil {
		t.Fatal(err)
	}
	session.mu.RLock()
	after := session.loginGeneration
	session.mu.RUnlock()
	if before != after {
		t.Fatal("duplicate in-progress login restarted attempt")
	}
	f.write(t, []byte("access denied\n"))
	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "failed")
	if _, err := f.manager.Login("task", "board", "network", LoginOptions{TimeoutSeconds: 3, Retries: 2}); err != nil {
		t.Fatal("eligible explicit retry rejected")
	}
	f.write(t, []byte("access denied\n"))
	waitForHardwareLoginStatusAt(t, f.manager, "task", "board", "network", "failed")
	if _, err := f.manager.Login("task", "board", "network", LoginOptions{Retries: 2}); !errors.Is(err, ErrLoginRetryLimit) {
		t.Fatal("retry budget could be silently reset")
	}
	f.write(t, []byte("password:\nBUDGET-MARKER\n"))
	f.waitHistory(t, "BUDGET-MARKER")
	if f.sends.Load() != 0 {
		t.Fatal("rejected retry sent a credential")
	}
	for _, options := range []LoginOptions{{TimeoutSeconds: 1}, {TimeoutSeconds: 301}, {Retries: 4}, {LineEnding: "invalid"}} {
		if _, err := f.manager.Login("task", "board", "network", options); !errors.Is(err, ErrLoginOptionsInvalid) {
			t.Fatal("manager accepted options outside published bounds")
		}
	}
}

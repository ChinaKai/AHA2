package agentproxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrReadyTimeout reports that the helper never announced a port. It is a setup
// failure, not a stream failure: nothing has been multiplexed yet, so the caller
// falls back to its previous transport and says why.
var ErrReadyTimeout = errors.New("agent-proxy: helper did not report a port in time")

// ErrBadHandshake reports a READY line that is not one, including one longer
// than any legitimate handshake. The line is read before framing starts, so an
// unbounded read there would let a broken helper hand us a buffer of its
// choosing before any protocol check runs.
var ErrBadHandshake = errors.New("agent-proxy: invalid handshake")

// maxReadyLine is generous for "READY proto=1 port=65535" and far below any
// value that could be confused with payload.
const maxReadyLine = 256

// Stdio is one helper process reachable over a byte stream. The SSH transport
// supplies it from a session channel; tests supply it from a local process.
type Stdio struct {
	// Stdin is the helper's stdin. Closing it is the graceful shutdown signal.
	Stdin io.WriteCloser
	// Stdout carries the READY line followed by frames.
	Stdout io.Reader
	// Wait blocks until the helper exits and reports its exit status.
	Wait func() error
	// Close tears the transport down. It is safe to call after Wait returned.
	Close func() error
}

// Session is a running tunnel. Its URL is the address the workspace-side agent
// must use, and every connection to it is carried back to Target.
type Session struct {
	// URL is the Agent API base URL as seen from inside the workspace.
	URL string
	// Target is the AHA-side address each stream is bridged to.
	Target string

	stdio         Stdio
	channel       *multiplexer
	closeOnce     sync.Once
	transportOnce sync.Once
	closeErr      error
}

// StartSession reads the helper's READY line and then serves streams until the
// helper exits or Close is called.
//
// readyTimeout bounds only the handshake. A helper that starts but never
// announces is indistinguishable from one that failed to start, and both must
// fall back rather than hang the Turn.
func StartSession(stdio Stdio, target, scheme string, readyTimeout time.Duration) (*Session, error) {
	if scheme == "" {
		scheme = "http"
	}
	reader := bufio.NewReaderSize(stdio.Stdout, MaxFramePayload+frameHeaderSize)
	port, err := readReady(reader, readyTimeout)
	if err != nil {
		return nil, err
	}
	session := &Session{
		// The helper only carries bytes; the scheme belongs to the AHA listener
		// the stream ends at. Keeping it means an https base URL still works,
		// with TLS running end to end through the tunnel.
		URL:    scheme + "://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Target: target,
		stdio:  stdio,
	}
	session.channel = newMultiplexer(reader, stdio.Stdin, func() (net.Conn, error) {
		return net.DialTimeout("tcp", target, 10*time.Second)
	})
	session.channel.run()
	if stdio.Wait != nil {
		go func() {
			waitErr := stdio.Wait()
			if waitErr != nil {
				session.channel.fail(fmt.Errorf("agent-proxy: helper exited: %w", waitErr))
			} else {
				session.channel.fail(errMuxClosed)
			}
			session.closeTransport()
		}()
	}
	// Whatever ended the channel — a broken frame, a dead helper — closes the
	// transport too. Without this, a request issued after the failure would sit
	// on a half-open SSH session until its own timeout instead of failing now.
	go func() {
		<-session.channel.Done()
		session.closeTransport()
	}()
	return session, nil
}

func readReady(reader *bufio.Reader, timeout time.Duration) (int, error) {
	type result struct {
		line string
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		line := make([]byte, 0, 32)
		for {
			value, err := reader.ReadByte()
			if err != nil {
				ready <- result{err: err}
				return
			}
			if value == '\n' {
				ready <- result{line: string(line)}
				return
			}
			line = append(line, value)
			if len(line) > maxReadyLine {
				ready <- result{err: fmt.Errorf("%w: READY line longer than %d bytes", ErrBadHandshake, maxReadyLine)}
				return
			}
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case value := <-ready:
		if value.err != nil {
			return 0, fmt.Errorf("agent-proxy: read READY: %w", value.err)
		}
		return parseReady(value.line)
	case <-timer.C:
		return 0, ErrReadyTimeout
	}
}

func parseReady(line string) (int, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || fields[0] != ReadyPrefix {
		return 0, fmt.Errorf("agent-proxy: unexpected handshake %q", strings.TrimSpace(line))
	}
	protocol, port := 0, 0
	for _, field := range fields[1:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "proto":
			protocol, _ = strconv.Atoi(value)
		case "port":
			port, _ = strconv.Atoi(value)
		}
	}
	if protocol != Protocol {
		return 0, fmt.Errorf("agent-proxy: helper speaks protocol %d, this build speaks %d", protocol, Protocol)
	}
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("agent-proxy: helper reported invalid port %d", port)
	}
	return port, nil
}

// Done is closed when the tunnel stopped.
func (s *Session) Done() <-chan struct{} { return s.channel.Done() }

// Err reports why the tunnel stopped. A clean Close reports nil.
func (s *Session) Err() error { return s.channel.Err() }

// Close ends the tunnel and releases the helper process. Closing stdin is the
// polite path — the helper exits on EOF and closes its listener — with a forced
// transport close as the bound.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		if s.stdio.Stdin != nil {
			_ = s.stdio.Stdin.Close()
		}
		select {
		case <-s.channel.Done():
		case <-time.After(3 * time.Second):
		}
		s.channel.Close()
		s.closeTransport()
	})
	return s.closeErr
}

// closeTransport tears the host-side transport down exactly once. The helper is
// a child of that transport, so this is also what bounds its lifetime.
func (s *Session) closeTransport() {
	s.transportOnce.Do(func() {
		if s.stdio.Close != nil {
			s.closeErr = s.stdio.Close()
		}
	})
}

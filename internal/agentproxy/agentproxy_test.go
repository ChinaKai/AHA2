package agentproxy

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type accepted struct {
		conn net.Conn
		err  error
	}
	result := make(chan accepted, 1)
	go func() {
		conn, err := listener.Accept()
		result <- accepted{conn: conn, err: err}
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-result
	if server.err != nil {
		t.Fatal(server.err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		if server.conn != nil {
			_ = server.conn.Close()
		}
	})
	return client, server.conn
}

// muxPair wires a dial-side multiplexer to an accept-side one over loopback,
// which is the same shape the SSH channel has: two duplex byte streams.
func muxPair(t *testing.T, target string) (*multiplexer, *multiplexer) {
	t.Helper()
	clientConn, serverConn := tcpPair(t)
	server := newMultiplexer(serverConn, serverConn, nil)
	server.run()
	client := newMultiplexer(clientConn, clientConn, func() (net.Conn, error) {
		return net.DialTimeout("tcp", target, 2*time.Second)
	})
	client.run()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client, server
}

// echoServer reads until EOF, writes everything back, then closes its write
// side. That exercises half-close in both directions instead of hiding it
// behind an echo that only returns after the connection is fully closed.
func echoServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				payload, _ := io.ReadAll(conn)
				_, _ = conn.Write(payload)
				_ = conn.(*net.TCPConn).CloseWrite()
			}(conn)
		}
	}()
	return listener
}

func TestMultiplexerConcurrentStreamsRoundTrip(t *testing.T) {
	echo := echoServer(t)
	_, server := muxPair(t, echo.Addr().String())

	const streams = 8
	var wait sync.WaitGroup
	wait.Add(streams)
	errs := make(chan error, streams)
	for index := 0; index < streams; index++ {
		go func(index int) {
			defer wait.Done()
			agent, helper := tcpPair(t)
			defer agent.Close()
			if _, err := server.attach(helper); err != nil {
				errs <- err
				return
			}
			payload := bytes.Repeat([]byte{byte('a' + index)}, 1024*(index+1))
			go func() {
				_, _ = agent.Write(payload)
				_ = agent.(*net.TCPConn).CloseWrite()
			}()
			reply, err := io.ReadAll(agent)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(reply, payload) {
				errs <- errors.New("stream payload mismatch")
			}
		}(index)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestMultiplexerCarriesPayloadLargerThanWindow(t *testing.T) {
	echo := echoServer(t)
	_, server := muxPair(t, echo.Addr().String())

	agent, helper := tcpPair(t)
	defer agent.Close()
	if _, err := server.attach(helper); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 2*InitialWindow+12345)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = agent.Write(payload)
		_ = agent.(*net.TCPConn).CloseWrite()
	}()
	reply, err := io.ReadAll(agent)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reply, payload) {
		t.Fatalf("round trip mismatched: got %d bytes, want %d", len(reply), len(payload))
	}
}

func TestMultiplexerOneBrokenStreamKeepsOthersAlive(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var served int
	var mu sync.Mutex
	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			served++
			first := served == 1
			mu.Unlock()
			go func(conn net.Conn, closeImmediately bool) {
				defer conn.Close()
				if closeImmediately {
					return
				}
				payload, _ := io.ReadAll(conn)
				_, _ = conn.Write(payload)
			}(conn, first)
		}
	}()

	_, server := muxPair(t, target.Addr().String())

	brokenAgent, brokenHelper := tcpPair(t)
	defer brokenAgent.Close()
	if _, err := server.attach(brokenHelper); err != nil {
		t.Fatal(err)
	}
	// The first stream's target hangs up without echoing; it must die alone.
	_ = brokenAgent.(*net.TCPConn).CloseWrite()
	brokenReply, _ := io.ReadAll(brokenAgent)
	if len(brokenReply) != 0 {
		t.Fatalf("broken stream returned %q", brokenReply)
	}

	agent, helper := tcpPair(t)
	defer agent.Close()
	if _, err := server.attach(helper); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = agent.Write([]byte("still here"))
		_ = agent.(*net.TCPConn).CloseWrite()
	}()
	reply, err := io.ReadAll(agent)
	if err != nil || string(reply) != "still here" {
		t.Fatalf("healthy stream failed: %q err=%v", reply, err)
	}
}

func TestMultiplexerRejectsOversizedFrame(t *testing.T) {
	client, server := tcpPair(t)
	defer client.Close()
	defer server.Close()
	mux := newMultiplexer(client, client, nil)
	mux.run()
	defer mux.Close()

	var header [frameHeaderSize]byte
	header[0] = byte(frameData)
	binary.BigEndian.PutUint32(header[1:5], 7)
	binary.BigEndian.PutUint32(header[5:9], MaxFramePayload+1)
	if _, err := server.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-mux.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("mux accepted an oversized frame instead of failing")
	}
	if !errors.Is(mux.Err(), ErrBadFrame) {
		t.Fatalf("err = %v, want ErrBadFrame", mux.Err())
	}
}

func TestMultiplexerRejectsUnknownFrameType(t *testing.T) {
	client, server := tcpPair(t)
	defer client.Close()
	defer server.Close()
	mux := newMultiplexer(client, client, nil)
	mux.run()
	defer mux.Close()

	if _, err := server.Write([]byte{0x7f, 0, 0, 0, 1, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-mux.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("mux accepted an unknown frame type")
	}
	if !errors.Is(mux.Err(), ErrBadFrame) {
		t.Fatalf("err = %v, want ErrBadFrame", mux.Err())
	}
}

func TestReadFrameRejectsTruncatedBody(t *testing.T) {
	var header [frameHeaderSize]byte
	header[0] = byte(frameData)
	binary.BigEndian.PutUint32(header[5:9], 8)
	_, err := readFrame(bytes.NewReader(append(header[:], 'x')))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want unexpected EOF", err)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	for _, original := range []frame{
		{kind: frameData, id: 42, payload: []byte("hello")},
		{kind: frameWindow, id: 7, payload: []byte{0, 0, 1, 0}},
		{kind: frameClose, id: 1},
		{kind: frameReset, id: 9},
	} {
		var buffer bytes.Buffer
		if err := writeFrame(&buffer, original); err != nil {
			t.Fatalf("write %v: %v", original, err)
		}
		decoded, err := readFrame(&buffer)
		if err != nil {
			t.Fatalf("read %v: %v", original, err)
		}
		if decoded.kind != original.kind || decoded.id != original.id || !bytes.Equal(decoded.payload, original.payload) {
			t.Fatalf("round trip = %v, want %v", decoded, original)
		}
	}
}

func TestHelperCarriesRealHTTPRequest(t *testing.T) {
	target := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"service":"aha2","path":"`+r.URL.Path+`"}`)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = target.Serve(listener) }()
	defer target.Close()

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	helperDone := make(chan error, 1)
	go func() { helperDone <- RunHelper(stdinReader, stdoutWriter, Protocol) }()

	session, err := StartSession(Stdio{
		Stdin:  stdinWriter,
		Stdout: stdoutReader,
		Wait:   func() error { return <-helperDone },
		Close:  func() error { return nil },
	}, listener.Addr().String(), "http", 5*time.Second)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(session.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET through helper: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"service":"aha2"`) || !strings.Contains(string(body), "/healthz") {
		t.Fatalf("body = %q", body)
	}
	// Several requests in a row reuse the tunnel through separate streams, which
	// is the shape the Agent API client actually produces.
	for index := 0; index < 3; index++ {
		response, err := client.Get(session.URL + "/api/v1/ping")
		if err != nil {
			t.Fatalf("request %d: %v", index, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
}

func TestReadyRejectsProtocolMismatch(t *testing.T) {
	if _, err := parseReady("READY proto=2 port=1234\n"); err == nil {
		t.Fatal("accepted a different protocol version")
	}
	if _, err := parseReady("READY proto=1 port=0\n"); err == nil {
		t.Fatal("accepted an invalid port")
	}
	port, err := parseReady("READY proto=1 port=4321\n")
	if err != nil || port != 4321 {
		t.Fatalf("port=%d err=%v", port, err)
	}
}

func TestStartSessionTimesOutWithoutReady(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	defer stdoutReader.Close()
	go func() {
		// A helper that starts but never announces its port.
		_, _ = io.Copy(io.Discard, stdinReader)
	}()
	defer stdinWriter.Close()
	defer stdoutWriter.Close()
	start := time.Now()
	_, err := StartSession(Stdio{
		Stdin:  stdinWriter,
		Stdout: stdoutReader,
		Wait:   func() error { return nil },
		Close:  func() error { return nil },
	}, "127.0.0.1:1", "http", 150*time.Millisecond)
	if !errors.Is(err, ErrReadyTimeout) {
		t.Fatalf("err = %v, want ErrReadyTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("READY timeout took %s", elapsed)
	}
}

// TestStreamRejectsDataBeyondItsWindow checks the receive-side bound directly:
// the peer may only run InitialWindow bytes ahead of the last WINDOW. Anything
// more is a protocol violation and must not be buffered.
func TestStreamRejectsDataBeyondItsWindow(t *testing.T) {
	client, _ := tcpPair(t)
	mux := newMultiplexer(client, client, nil)
	defer mux.Close()
	agent, helper := tcpPair(t)
	defer agent.Close()

	// Register a stream without starting its pumps, so the queue cannot drain
	// and the accounting under test is deterministic.
	instance := newStream(mux, 1, helper)
	mux.mu.Lock()
	mux.streams[1] = instance
	mux.mu.Unlock()

	// Exactly the window is accepted: eight full frames.
	for index := 0; index < InitialWindow/MaxFramePayload; index++ {
		if err := mux.handle(frame{kind: frameData, id: 1, payload: make([]byte, MaxFramePayload)}); err != nil {
			t.Fatalf("frame %d: %v", index, err)
		}
	}
	if mux.lookup(1) == nil {
		t.Fatal("the stream was reset before the window was exceeded")
	}
	// One byte more is not.
	if err := mux.handle(frame{kind: frameData, id: 1, payload: make([]byte, 1)}); err != nil {
		t.Fatalf("over-window frame: %v", err)
	}
	if mux.lookup(1) != nil {
		t.Fatal("over-window data did not reset the stream")
	}
	reset := waitForFrameKind(t, mux, frameReset)
	if reset.id != 1 {
		t.Fatalf("reset id = %d, want 1", reset.id)
	}
}

// fillerStream occupies a stream slot without a live connection. Its conn is a
// pipe that nothing reads, so nothing in the test depends on its behaviour.
func fillerStream(t *testing.T, mux *multiplexer, id uint32) *stream {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return newStream(mux, id, client)
}

func waitForFrameKind(t *testing.T, mux *multiplexer, kind frameType) frame {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case value := <-mux.outgoing:
			if value.kind == kind {
				return value
			}
		case <-deadline:
			t.Fatalf("no %v frame was produced", kind)
		}
	}
}

// TestChannelRefusesMoreThanMaxStreams pins the per-channel stream ceiling: a
// peer that keeps opening connections must be refused, not allowed to spawn
// goroutines and sockets without limit.
func TestChannelRefusesMoreThanMaxStreams(t *testing.T) {
	client, _ := tcpPair(t)
	mux := newMultiplexer(client, client, nil)
	defer mux.Close()
	for index := 1; index <= MaxStreamsPerChannel; index++ {
		mux.mu.Lock()
		mux.streams[uint32(index)] = fillerStream(t, mux, uint32(index))
		mux.mu.Unlock()
	}
	agent, helper := tcpPair(t)
	defer agent.Close()
	defer helper.Close()
	if _, err := mux.attach(helper); !errors.Is(err, errTooManyStreams) {
		t.Fatalf("attach err = %v, want errTooManyStreams", err)
	}

	// The dial side refuses too, without calling the dialer.
	dials := 0
	dialMux := newMultiplexer(client, client, func() (net.Conn, error) {
		dials++
		return nil, errors.New("unexpected dial")
	})
	defer dialMux.Close()
	for index := 1; index <= MaxStreamsPerChannel; index++ {
		dialMux.mu.Lock()
		dialMux.streams[uint32(index)] = fillerStream(t, dialMux, uint32(index))
		dialMux.mu.Unlock()
	}
	if err := dialMux.handle(frame{kind: frameOpen, id: 9000}); err != nil {
		t.Fatalf("open over cap: %v", err)
	}
	if dials != 0 {
		t.Fatalf("dial side opened %d connections past the cap", dials)
	}
	if reset := waitForFrameKind(t, dialMux, frameReset); reset.id != 9000 {
		t.Fatalf("reset id = %d, want 9000", reset.id)
	}
}

func TestStartSessionRejectsOversizedReadyLine(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	defer stdinWriter.Close()
	defer stdinReader.Close()
	go func() {
		_, _ = stdoutWriter.Write([]byte(strings.Repeat("x", maxReadyLine+1)))
		_, _ = stdoutWriter.Write([]byte("\n"))
	}()
	defer stdoutWriter.Close()
	_, err := StartSession(Stdio{
		Stdin:  stdinWriter,
		Stdout: stdoutReader,
		Wait:   func() error { return nil },
		Close:  func() error { return nil },
	}, "127.0.0.1:1", "http", 2*time.Second)
	if !errors.Is(err, ErrBadHandshake) {
		t.Fatalf("err = %v, want ErrBadHandshake", err)
	}
}

// TestSessionKeepsTheConfiguredScheme matters for an https loopback listener:
// the tunnel carries bytes, so dropping the scheme would silently send
// plaintext at a TLS port.
func TestSessionKeepsTheConfiguredScheme(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	helperDone := make(chan error, 1)
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() { helperDone <- RunHelper(stdinReader, stdoutWriter, Protocol) }()
	session, err := StartSession(Stdio{
		Stdin:  stdinWriter,
		Stdout: stdoutReader,
		Wait:   func() error { return <-helperDone },
		Close:  func() error { return nil },
	}, target.Addr().String(), "https", 5*time.Second)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	defer session.Close()
	if !strings.HasPrefix(session.URL, "https://127.0.0.1:") {
		t.Fatalf("url = %q, want the https scheme preserved", session.URL)
	}
}

// TestStreamClampsCreditToTheInitialWindow pins the sender-side bound: a peer
// that grants more than the window would otherwise buy permission to push an
// unbounded backlog.
func TestStreamClampsCreditToTheInitialWindow(t *testing.T) {
	mux := newMultiplexer(nil, nil, nil)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	instance := newStream(mux, 1, client)
	instance.addCredit(1 << 30)
	instance.sendMu.Lock()
	credit := instance.credit
	instance.sendMu.Unlock()
	if credit != InitialWindow {
		t.Fatalf("credit = %d, want %d", credit, InitialWindow)
	}
}

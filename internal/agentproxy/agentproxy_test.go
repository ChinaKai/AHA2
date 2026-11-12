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
	}, listener.Addr().String(), 5*time.Second)
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
	}, "127.0.0.1:1", 150*time.Millisecond)
	if !errors.Is(err, ErrReadyTimeout) {
		t.Fatalf("err = %v, want ErrReadyTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("READY timeout took %s", elapsed)
	}
}

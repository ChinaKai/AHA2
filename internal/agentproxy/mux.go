package agentproxy

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

// errMuxClosed is returned for work attempted after the peer went away. It is
// not a protocol violation: the SSH channel simply ended.
var errMuxClosed = errors.New("agent-proxy: channel closed")

// multiplexer carries many TCP streams over one byte stream.
//
// The accept side (the workspace helper) attaches sockets it accepted; the dial
// side (AHA) creates a stream on the first frame that names an unknown id. That
// asymmetry is deliberate: the agent inside the workspace opens the connections,
// so only the helper knows a stream exists before any bytes move.
type multiplexer struct {
	reader *bufio.Reader
	writer io.Writer
	dial   func() (net.Conn, error)

	outgoing chan frame
	done     chan struct{}

	closeOnce sync.Once

	mu      sync.Mutex
	streams map[uint32]*stream
	nextID  uint32
	err     error
	closed  bool
}

func newMultiplexer(r io.Reader, w io.Writer, dial func() (net.Conn, error)) *multiplexer {
	return &multiplexer{
		reader:   bufio.NewReader(r),
		writer:   w,
		dial:     dial,
		outgoing: make(chan frame, 64),
		done:     make(chan struct{}),
		streams:  map[uint32]*stream{},
	}
}

func (m *multiplexer) run() {
	go m.writeLoop()
	go m.readLoop()
}

func (m *multiplexer) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// Done is closed when the peer went away or the framing broke.
func (m *multiplexer) Done() <-chan struct{} { return m.done }

func (m *multiplexer) writeLoop() {
	// Each frame is one buffered write, so a DATA frame does not become two
	// transport packets on the SSH channel.
	writer := bufio.NewWriterSize(m.writer, MaxFramePayload+frameHeaderSize)
	for {
		select {
		case <-m.done:
			return
		case f := <-m.outgoing:
			if err := writeFrame(writer, f); err != nil {
				m.fail(fmt.Errorf("agent-proxy: write frame: %w", err))
				return
			}
			if err := writer.Flush(); err != nil {
				m.fail(fmt.Errorf("agent-proxy: write frame: %w", err))
				return
			}
		}
	}
}

func (m *multiplexer) readLoop() {
	for {
		f, err := readFrame(m.reader)
		if err != nil {
			// EOF on the channel is the normal end: the helper's stdin closed or
			// the SSH session ended. A frame cut in half is a fault and is kept
			// as one, because it means bytes were lost inside a stream.
			if errors.Is(err, io.ErrUnexpectedEOF) {
				err = fmt.Errorf("agent-proxy: truncated frame: %w", err)
			}
			m.fail(err)
			return
		}
		if err := m.handle(f); err != nil {
			m.fail(err)
			return
		}
	}
}

func (m *multiplexer) handle(f frame) error {
	switch f.kind {
	case frameOpen:
		if m.dial == nil {
			return fmt.Errorf("%w: open frame on the accept side", ErrBadFrame)
		}
		if existing := m.lookup(f.id); existing != nil {
			return fmt.Errorf("%w: duplicate open for stream %d", ErrBadFrame, f.id)
		}
		conn, err := m.dial()
		if err != nil {
			// Tell the peer its connection was refused, so the agent sees a
			// reset now instead of a Turn that times out later.
			m.send(frame{kind: frameReset, id: f.id})
			return nil
		}
		m.register(f.id, conn)
	case frameData:
		s := m.lookup(f.id)
		if s == nil {
			// Data before its OPEN, or for a stream the dial side abandoned.
			// Reset rather than leaving the peer waiting.
			m.send(frame{kind: frameReset, id: f.id})
			return nil
		}
		s.receive(f.payload)
	case frameClose:
		if s := m.lookup(f.id); s != nil {
			s.peerClosed()
		}
	case frameReset:
		if s := m.lookup(f.id); s != nil {
			s.abort()
		}
	case frameWindow:
		if s := m.lookup(f.id); s != nil {
			s.addCredit(int64(binary.BigEndian.Uint32(f.payload)))
		}
	}
	return nil
}

// lookup returns an existing stream without creating one. Control frames must
// never open a connection: a stray CLOSE for a finished stream would otherwise
// dial the target and immediately abandon it.
func (m *multiplexer) lookup(id uint32) *stream {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streams[id]
}

// register installs a stream and starts its pumps.
func (m *multiplexer) register(id uint32, conn net.Conn) *stream {
	s := newStream(m, id, conn)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	m.streams[id] = s
	m.mu.Unlock()
	s.start()
	return s
}

// attach registers an accepted connection on the accept side, tells the dial
// side to open its socket, and returns the stream id.
//
// OPEN goes out before any pump starts so the peer always learns about a stream
// before its first byte, including the empty stream of a connection that is
// closed right after connecting.
func (m *multiplexer) attach(conn net.Conn) (uint32, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0, errMuxClosed
	}
	m.nextID++
	id := m.nextID
	m.mu.Unlock()
	if !m.send(frame{kind: frameOpen, id: id}) {
		return 0, errMuxClosed
	}
	if m.register(id, conn) == nil {
		return 0, errMuxClosed
	}
	return id, nil
}

func (m *multiplexer) send(f frame) bool {
	select {
	case m.outgoing <- f:
		return true
	case <-m.done:
		return false
	}
}

func (m *multiplexer) remove(id uint32) {
	m.mu.Lock()
	delete(m.streams, id)
	m.mu.Unlock()
}

// fail tears the channel down once. A broken frame makes every stream
// untrustworthy, so they are all reset rather than left half-open.
func (m *multiplexer) fail(err error) {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		// A closed channel is the normal end, not a fault worth reporting.
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, errMuxClosed) {
			m.err = err
		}
		streams := make([]*stream, 0, len(m.streams))
		for _, s := range m.streams {
			streams = append(streams, s)
		}
		m.streams = map[uint32]*stream{}
		m.mu.Unlock()
		close(m.done)
		for _, s := range streams {
			s.abort()
		}
	})
}

// Close ends the channel from the local side and releases every stream. It does
// not write a frame: closing the transport is what tells the peer.
func (m *multiplexer) Close() {
	m.fail(errMuxClosed)
}

// stream is one TCP connection carried over the channel.
//
// Each direction is independent: an EOF from the local socket half-closes only
// that direction, so HTTP request/response bodies that end before the other
// side does keep working.
type stream struct {
	mux  *multiplexer
	id   uint32
	conn net.Conn

	sendMu     sync.Mutex
	credit     int64
	creditWake chan struct{}

	recvMu     sync.Mutex
	recvCond   *sync.Cond
	queue      [][]byte
	recvClosed bool

	stateMu   sync.Mutex
	writeDone bool
	readDone  bool
	finished  bool

	dead      chan struct{}
	deadOnce  sync.Once
	closeOnce sync.Once
}

func newStream(m *multiplexer, id uint32, conn net.Conn) *stream {
	s := &stream{
		mux: m, id: id, conn: conn,
		credit: InitialWindow, creditWake: make(chan struct{}, 1),
		dead: make(chan struct{}),
	}
	s.recvCond = sync.NewCond(&s.recvMu)
	return s
}

func (s *stream) start() {
	go s.pumpOut()
	go s.pumpIn()
}

// pumpOut forwards bytes from the local socket to the peer, waiting for credit
// before each frame so a fast producer cannot outrun a slow consumer.
func (s *stream) pumpOut() {
	defer s.directionDone(true)
	buffer := make([]byte, MaxFramePayload)
	for {
		n, err := s.conn.Read(buffer)
		if n > 0 {
			payload := make([]byte, n)
			copy(payload, buffer[:n])
			if !s.sendData(payload) {
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.mux.send(frame{kind: frameClose, id: s.id})
			} else {
				s.mux.send(frame{kind: frameReset, id: s.id})
			}
			return
		}
	}
}

func (s *stream) sendData(payload []byte) bool {
	for {
		s.sendMu.Lock()
		if s.credit >= int64(len(payload)) {
			s.credit -= int64(len(payload))
			s.sendMu.Unlock()
			return s.mux.send(frame{kind: frameData, id: s.id, payload: payload})
		}
		s.sendMu.Unlock()
		select {
		case <-s.creditWake:
		case <-s.dead:
			return false
		}
	}
}

func (s *stream) addCredit(value int64) {
	s.sendMu.Lock()
	s.credit += value
	s.sendMu.Unlock()
	select {
	case s.creditWake <- struct{}{}:
	default:
	}
}

func (s *stream) receive(payload []byte) {
	if len(payload) == 0 {
		return
	}
	s.recvMu.Lock()
	if s.recvClosed {
		s.recvMu.Unlock()
		return
	}
	s.queue = append(s.queue, payload)
	s.recvCond.Signal()
	s.recvMu.Unlock()
}

func (s *stream) peerClosed() {
	s.recvMu.Lock()
	s.recvClosed = true
	s.recvCond.Signal()
	s.recvMu.Unlock()
}

// pumpIn writes queued payloads to the local socket and returns their credit as
// they are consumed, which is what bounds memory to InitialWindow per stream.
func (s *stream) pumpIn() {
	defer s.directionDone(false)
	for {
		s.recvMu.Lock()
		for len(s.queue) == 0 && !s.recvClosed {
			s.recvCond.Wait()
		}
		if len(s.queue) == 0 && s.recvClosed {
			s.recvMu.Unlock()
			if halfCloser, ok := s.conn.(interface{ CloseWrite() error }); ok {
				_ = halfCloser.CloseWrite()
			}
			return
		}
		payload := s.queue[0]
		s.queue = s.queue[1:]
		s.recvMu.Unlock()
		if _, err := s.conn.Write(payload); err != nil {
			s.mux.send(frame{kind: frameReset, id: s.id})
			s.abort()
			return
		}
		s.grant(len(payload))
	}
}

func (s *stream) grant(n int) {
	var payload [4]byte
	binary.BigEndian.PutUint32(payload[:], uint32(n))
	s.mux.send(frame{kind: frameWindow, id: s.id, payload: payload[:]})
}

func (s *stream) directionDone(write bool) {
	s.stateMu.Lock()
	if write {
		s.writeDone = true
	} else {
		s.readDone = true
	}
	finished := s.finished || (s.writeDone && s.readDone)
	s.stateMu.Unlock()
	if finished {
		s.finish()
	}
}

func (s *stream) finish() {
	s.closeOnce.Do(func() {
		_ = s.conn.Close()
		s.deadOnce.Do(func() { close(s.dead) })
		s.mux.remove(s.id)
	})
}

// abort ends the stream immediately. Both pumps observe the closed socket or
// the dead channel and stop; the peer is not told because either the peer asked
// for it or the channel is already gone.
func (s *stream) abort() {
	s.stateMu.Lock()
	s.finished = true
	s.stateMu.Unlock()
	s.closeOnce.Do(func() {
		_ = s.conn.Close()
		s.deadOnce.Do(func() { close(s.dead) })
		s.mux.remove(s.id)
	})
	s.recvMu.Lock()
	s.recvCond.Broadcast()
	s.recvMu.Unlock()
}

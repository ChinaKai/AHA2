// Package agentproxy carries TCP connections between AHA and a remote
// workspace over the stdin/stdout of a single SSH session.
//
// The workspace side runs a helper that listens on its own loopback and
// multiplexes every accepted connection onto the SSH channel. The AHA side
// demultiplexes those streams and dials the real Agent API for each one. The
// SSH channel therefore replaces sshd port forwarding: it needs no
// AllowTcpForwarding, no PermitListen and no extra port on either side.
package agentproxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Protocol is the wire version carried by the READY line and by every manifest.
// A helper that speaks a different version is refused rather than guessed at:
// frame semantics are positional, so a mismatch corrupts streams instead of
// failing loudly.
const Protocol = 1

const (
	// MaxFramePayload bounds one DATA frame. It is small enough that a stalled
	// peer cannot pin a large buffer, and large enough that bulk transfers stay
	// efficient.
	MaxFramePayload = 32 << 10
	// InitialWindow is the per-stream credit, in each direction, before the
	// receiver has acknowledged anything. Memory use per stream is bounded by
	// it, which is what keeps a slow consumer from buffering a whole request.
	InitialWindow = 256 << 10
)

type frameType uint8

const (
	// frameOpen announces a stream the accept side has already accepted. The
	// dial side opens its socket on this frame, so a connection that sends
	// nothing (or closes immediately) still reaches the target.
	frameOpen   frameType = 1
	frameData   frameType = 2
	frameWindow frameType = 3
	frameClose  frameType = 4
	frameReset  frameType = 5
)

const frameHeaderSize = 9 // type(1) + stream id(4) + payload length(4)

// ErrBadFrame marks input that violates the framing contract. It always tears
// the whole multiplexer down: there is no safe resynchronisation point after a
// wrong length.
var ErrBadFrame = errors.New("agent-proxy: bad frame")

type frame struct {
	kind    frameType
	id      uint32
	payload []byte
}

func (f frame) String() string {
	names := map[frameType]string{
		frameOpen: "OPEN", frameData: "DATA", frameWindow: "WINDOW",
		frameClose: "CLOSE", frameReset: "RESET",
	}
	return fmt.Sprintf("%s id=%d len=%d", names[f.kind], f.id, len(f.payload))
}

func writeFrame(w io.Writer, f frame) error {
	var header [frameHeaderSize]byte
	header[0] = byte(f.kind)
	binary.BigEndian.PutUint32(header[1:5], f.id)
	binary.BigEndian.PutUint32(header[5:9], uint32(len(f.payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if len(f.payload) == 0 {
		return nil
	}
	_, err := w.Write(f.payload)
	return err
}

func readFrame(r io.Reader) (frame, error) {
	var header [frameHeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return frame{}, err
	}
	kind := frameType(header[0])
	id := binary.BigEndian.Uint32(header[1:5])
	length := binary.BigEndian.Uint32(header[5:9])
	if length > MaxFramePayload {
		return frame{}, fmt.Errorf("%w: frame length %d exceeds %d", ErrBadFrame, length, MaxFramePayload)
	}
	switch kind {
	case frameData:
	case frameOpen:
		if length != 0 {
			return frame{}, fmt.Errorf("%w: open frame length %d", ErrBadFrame, length)
		}
	case frameWindow:
		if length != 4 {
			return frame{}, fmt.Errorf("%w: window frame length %d", ErrBadFrame, length)
		}
	case frameClose, frameReset:
		if length != 0 {
			return frame{}, fmt.Errorf("%w: control frame length %d", ErrBadFrame, length)
		}
	default:
		return frame{}, fmt.Errorf("%w: unknown frame type %d", ErrBadFrame, header[0])
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return frame{}, err
		}
	}
	return frame{kind: kind, id: id, payload: payload}, nil
}

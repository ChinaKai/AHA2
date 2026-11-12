package agentproxy

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
)

// ReadyPrefix is the line the helper prints on stdout once its loopback
// listener exists. Everything after the newline is framing, so the client must
// stop reading lines at exactly that point.
const ReadyPrefix = "READY"

// RunHelper is the workspace side of the tunnel.
//
// It listens on the workspace's own loopback, announces the port it received,
// and then multiplexes every accepted connection over stdin/stdout. The SSH
// session that carries those two pipes is the only transport involved: no
// sshd forwarding request, no listening port on the AHA side, no configuration
// on the workspace.
func RunHelper(stdin io.Reader, stdout io.Writer, protocol int) error {
	if protocol != Protocol {
		return fmt.Errorf("agent-proxy: unsupported protocol %d (this build speaks %d)", protocol, Protocol)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("agent-proxy: listen on workspace loopback: %w", err)
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || address.Port == 0 {
		return fmt.Errorf("agent-proxy: listener did not report a port")
	}
	ready := bufio.NewWriter(stdout)
	if _, err := fmt.Fprintf(ready, "%s proto=%d port=%d\n", ReadyPrefix, Protocol, address.Port); err != nil {
		return fmt.Errorf("agent-proxy: announce port: %w", err)
	}
	if err := ready.Flush(); err != nil {
		return fmt.Errorf("agent-proxy: announce port: %w", err)
	}

	channel := newMultiplexer(stdin, stdout, nil)
	channel.run()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			if _, err := channel.attach(connection); err != nil {
				// One refused connection (over the stream ceiling, or racing the
				// channel's shutdown) must not stop the listener: the agent can
				// open another. Only a dead channel ends the loop.
				_ = connection.Close()
				if errors.Is(err, errMuxClosed) {
					return
				}
				continue
			}
		}
	}()
	<-channel.Done()
	listener.Close()
	// stdin EOF ends the session normally; only a broken frame or a transport
	// error is worth reporting.
	return channel.Err()
}

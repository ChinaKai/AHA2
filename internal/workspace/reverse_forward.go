package workspace

import (
	"io"
	"net"
	"sync"
)

// ReverseForward makes an AHA-side address reachable from inside a workspace
// that cannot dial AHA directly.
//
// AHA always reaches the workspace (it starts the backend process there), but the
// reverse direction is not guaranteed: NAT, an outbound-only firewall, a jump
// host, or an AHA bound to loopback all leave the workspace unable to open the
// Agent API connection, and no amount of address configuration can fix a path
// that does not exist. Carrying the connection back over the transport that
// already works removes the requirement entirely.
type ReverseForward struct {
	// Target is the AHA-side address to forward to, as host:port.
	Target string
	// EnvName receives the workspace-side base URL of the forward, so the
	// backend process can be told where AHA is without knowing about any of this.
	EnvName string
}

// serveReverseForward proxies every connection accepted on listener to target
// until the listener closes.
//
// The remote side accepts the connections; this side must dial the real
// destination and pump bytes, which is what makes the forward transparent. Each
// connection is handled independently so one stalled peer cannot block others.
func serveReverseForward(listener net.Listener, target string) {
	for {
		incoming, err := listener.Accept()
		if err != nil {
			// A closed listener is the normal way this loop ends.
			return
		}
		go proxyForwardedConnection(incoming, target)
	}
}

func proxyForwardedConnection(incoming net.Conn, target string) {
	defer incoming.Close()
	outgoing, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer outgoing.Close()

	// Copy both directions and close the write side as soon as one ends, so the
	// peer sees EOF instead of waiting for a half-open connection to time out.
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = io.Copy(outgoing, incoming)
		if closer, ok := outgoing.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
	}()
	go func() {
		defer wait.Done()
		_, _ = io.Copy(incoming, outgoing)
		if closer, ok := incoming.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
	}()
	wait.Wait()
}

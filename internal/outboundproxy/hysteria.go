package outboundproxy

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	hyclient "github.com/apernet/hysteria/core/v2/client"
	"github.com/apernet/hysteria/extras/v2/obfs"
)

type tunnelClient interface {
	TCP(string) (net.Conn, error)
	Close() error
}

type packetFactory struct{ password []byte }

func (f packetFactory) New(_ net.Addr) (net.PacketConn, error) {
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	if len(f.password) == 0 {
		return conn, nil
	}
	wrapped, err := obfs.WrapPacketConnSalamander(conn, f.password)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return wrapped, nil
}

func newHysteriaClient(node Node) (tunnelClient, error) {
	address, err := net.ResolveUDPAddr("udp", net.JoinHostPort(node.Server, strconv.Itoa(node.Port)))
	if err != nil {
		return nil, fmt.Errorf("resolve Hysteria2 server: %w", err)
	}
	sni := node.SNI
	if sni == "" {
		sni = node.Server
	}
	config := &hyclient.Config{
		ServerAddr: address,
		Auth:       node.Password,
		TLSConfig: hyclient.TLSConfig{
			ServerName: sni, InsecureSkipVerify: node.SkipCertVerify,
		},
		QUICConfig: hyclient.QUICConfig{
			MaxIdleTimeout: 30 * time.Second, KeepAlivePeriod: 10 * time.Second,
		},
	}
	if node.Obfs == "salamander" {
		config.ConnFactory = packetFactory{password: []byte(node.ObfsPassword)}
	}
	client, _, err := hyclient.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("connect Hysteria2 node: %w", err)
	}
	return client, nil
}

type hysteriaDialer struct {
	mu        sync.Mutex
	node      Node
	client    tunnelClient
	newClient func(Node) (tunnelClient, error)
}

func newHysteriaDialer(node Node) *hysteriaDialer {
	return &hysteriaDialer{node: node, newClient: newHysteriaClient}
}

func (d *hysteriaDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("managed proxy only supports TCP destinations")
	}
	for attempt := 0; attempt < 2; attempt++ {
		client, err := d.ensureClient(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("Hysteria2 connection failed")
		}
		conn, err := dialTunnelContext(ctx, client, address)
		if err == nil {
			return conn, nil
		}
		d.reset(client)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("Hysteria2 destination connection failed")
}

func (d *hysteriaDialer) ensureClient(ctx context.Context) (tunnelClient, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client != nil {
		return d.client, nil
	}
	type result struct {
		client tunnelClient
		err    error
	}
	ready := make(chan result, 1)
	go func() {
		client, err := d.newClient(d.node)
		ready <- result{client: client, err: err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			result := <-ready
			if result.client != nil {
				_ = result.client.Close()
			}
		}()
		return nil, ctx.Err()
	case result := <-ready:
		if result.err != nil {
			return nil, result.err
		}
		d.client = result.client
		return d.client, nil
	}
}

func dialTunnelContext(ctx context.Context, client tunnelClient, address string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		conn, err := client.TCP(address)
		ready <- result{conn: conn, err: err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			result := <-ready
			if result.conn != nil {
				_ = result.conn.Close()
			}
		}()
		return nil, ctx.Err()
	case result := <-ready:
		return result.conn, result.err
	}
}

func (d *hysteriaDialer) reset(expected tunnelClient) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client == expected {
		_ = d.client.Close()
		d.client = nil
	}
}

func (d *hysteriaDialer) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client == nil {
		return nil
	}
	err := d.client.Close()
	d.client = nil
	return err
}

package outboundproxy

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"sync"

	dispatcher "github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/proxyman"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/vless"
	vlessout "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/reality"
	transtcp "github.com/xtls/xray-core/transport/internet/tcp"
)

type managedNodeDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
	Close() error
}

func newManagedNodeDialer(node Node) (managedNodeDialer, error) {
	switch node.Protocol {
	case "hysteria2":
		return newHysteriaDialer(node), nil
	case "vless":
		return newXrayDialer(node)
	default:
		return nil, fmt.Errorf("managed proxy node protocol is unsupported")
	}
}

type xrayDialer struct {
	mu       sync.RWMutex
	instance *core.Instance
}

func newXrayDialer(node Node) (*xrayDialer, error) {
	config, err := buildXrayConfig(node)
	if err != nil {
		return nil, fmt.Errorf("invalid VLESS Reality configuration")
	}
	instance, err := core.New(config)
	if err != nil {
		return nil, fmt.Errorf("initialize VLESS Reality core failed")
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return nil, fmt.Errorf("start VLESS Reality core failed")
	}
	return &xrayDialer{instance: instance}, nil
}

func buildXrayConfig(node Node) (*core.Config, error) {
	if node.Protocol != "vless" || node.Network != "tcp" || node.Flow != "xtls-rprx-vision" {
		return nil, fmt.Errorf("unsupported VLESS transport")
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(node.RealityPublicKey)
	if err != nil || len(publicKey) != 32 {
		return nil, fmt.Errorf("invalid REALITY public key")
	}
	shortIDBytes, err := hex.DecodeString(node.RealityShortID)
	if err != nil || len(shortIDBytes) > 8 {
		return nil, fmt.Errorf("invalid REALITY short ID")
	}
	shortID := make([]byte, 8)
	copy(shortID, shortIDBytes)
	realityConfig := serial.ToTypedMessage(&reality.Config{
		Fingerprint: node.ClientFingerprint,
		ServerName:  node.SNI,
		PublicKey:   publicKey,
		ShortId:     shortID,
		SpiderX:     node.RealitySpiderX,
		SpiderY:     make([]int64, 10),
	})
	streamConfig := &internet.StreamConfig{
		ProtocolName: "tcp",
		TransportSettings: []*internet.TransportConfig{{
			ProtocolName: "tcp", Settings: serial.ToTypedMessage(&transtcp.Config{}),
		}},
		SecurityType:     realityConfig.Type,
		SecuritySettings: []*serial.TypedMessage{realityConfig},
	}
	account := serial.ToTypedMessage(&vless.Account{
		Id: node.UUID, Flow: node.Flow, Encryption: "none",
	})
	outboundConfig := serial.ToTypedMessage(&vlessout.Config{
		Vnext: &protocol.ServerEndpoint{
			Address: xnet.NewIPOrDomain(xnet.ParseAddress(node.Server)),
			Port:    uint32(node.Port),
			User:    &protocol.User{Account: account},
		},
	})
	return &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
		},
		Outbound: []*core.OutboundHandlerConfig{{
			Tag: "aha-managed-vless", SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{StreamSettings: streamConfig}),
			ProxySettings: outboundConfig,
		}},
	}, nil
}

func (d *xrayDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("managed proxy only supports TCP destinations")
	}
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return nil, fmt.Errorf("managed proxy destination is invalid")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("managed proxy destination is invalid")
	}
	d.mu.RLock()
	instance := d.instance
	d.mu.RUnlock()
	if instance == nil {
		return nil, fmt.Errorf("managed proxy is closed")
	}
	connection, err := core.Dial(ctx, instance, xnet.TCPDestination(xnet.ParseAddress(host), xnet.Port(port)))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("VLESS Reality destination connection failed")
	}
	return connection, nil
}

func (d *xrayDialer) Close() error {
	d.mu.Lock()
	instance := d.instance
	d.instance = nil
	d.mu.Unlock()
	if instance == nil {
		return nil
	}
	if err := instance.Close(); err != nil {
		return fmt.Errorf("close VLESS Reality core failed")
	}
	return nil
}

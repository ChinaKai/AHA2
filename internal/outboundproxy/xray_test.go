package outboundproxy

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestXrayDialerBuildsAndClosesVLESSVisionRealityCore(t *testing.T) {
	node := testVLESSRealityNode()
	config, err := buildXrayConfig(node)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Inbound) != 0 || len(config.Outbound) != 1 || len(config.App) != 2 {
		t.Fatalf("Xray config is not outbound-only: inbounds=%d outbounds=%d apps=%d", len(config.Inbound), len(config.Outbound), len(config.App))
	}
	dialer, err := newXrayDialer(node)
	if err != nil {
		t.Fatal(err)
	}
	if dialer.instance == nil {
		t.Fatal("Xray instance was not started")
	}
	if _, err := dialer.DialContext(context.Background(), "udp", "example.invalid:443"); err == nil {
		t.Fatal("non-TCP destination was accepted")
	}
	if err := dialer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dialer.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
	if _, err := dialer.DialContext(context.Background(), "tcp", "example.invalid:443"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed dialer result: %v", err)
	}
}

func TestXrayDialerErrorsDoNotExposeNodeOrDestinationSecrets(t *testing.T) {
	node := testVLESSRealityNode()
	dialer, err := newXrayDialer(node)
	if err != nil {
		t.Fatal(err)
	}
	defer dialer.Close()
	_, err = dialer.DialContext(context.Background(), "tcp", "sensitive-destination-without-port")
	if err == nil {
		t.Fatal("invalid destination was accepted")
	}
	for _, secret := range []string{node.Server, node.UUID, node.RealityPublicKey, "sensitive-destination-without-port"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("dial error exposed secret %q: %v", secret, err)
		}
	}
}

func TestManagedNodeDialerRejectsUnknownProtocolWithoutDetails(t *testing.T) {
	if _, err := newManagedNodeDialer(Node{Protocol: "secret-protocol", Server: "secret-server"}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unexpected unknown protocol error: %v", err)
	}
}

func testVLESSRealityNode() Node {
	return Node{
		Protocol: "vless", Name: "test-reality", Server: "127.0.0.1", Port: 1,
		UUID: "00000000-0000-4000-8000-000000000001", Flow: "xtls-rprx-vision", Network: "tcp",
		SNI: "cover.example.invalid", ClientFingerprint: "chrome",
		RealityPublicKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), RealityShortID: "0123abcd", RealitySpiderX: "/",
	}
}

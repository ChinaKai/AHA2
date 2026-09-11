package outboundproxy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseSubscriptionKeepsOnlyHysteria2AndDoesNotExposeSecrets(t *testing.T) {
	raw := `proxies:
  - name: hy-node
    type: hysteria2
    server: example.invalid
    port: 443
    password: highly-secret-auth
    sni: edge.example.invalid
    skip-cert-verify: false
    obfs: salamander
    obfs-password: highly-secret-obfs
  - name: cdn-node
    type: vless
    server: cdn.example.invalid
    port: 443
`
	subscription, err := ParseSubscription([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(subscription.Nodes) != 1 || subscription.Nodes[0].Name != "hy-node" {
		t.Fatalf("unexpected nodes: %#v", subscription.Nodes)
	}
	if subscription.UnsupportedCount != 1 || len(subscription.UnsupportedTypes) != 1 || subscription.UnsupportedTypes[0] != "vless" {
		t.Fatalf("unexpected unsupported summary: %#v", subscription)
	}
	encoded, err := json.Marshal(subscription.Summaries())
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"highly-secret-auth", "highly-secret-obfs", "example.invalid"} {
		if strings.Contains(text, secret) {
			t.Fatalf("summary exposed secret or server value %q: %s", secret, text)
		}
	}
}

func TestParseSubscriptionSupportsVLESSVisionReality(t *testing.T) {
	publicKey := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	raw := `proxies:
  - name: reality-node
    type: vless
    server: reality.example.invalid
    port: 443
    uuid: 00000000-0000-4000-8000-000000000001
    network: tcp
    tls: true
    flow: xtls-rprx-vision
    servername: cover.example.invalid
    client-fingerprint: chrome
    reality-opts:
      public-key: ` + publicKey + `
      short-id: 0123abcd
  - name: websocket-node
    type: vless
    server: websocket.example.invalid
    port: 443
    uuid: 00000000-0000-4000-8000-000000000002
    network: ws
    tls: true
`
	subscription, err := ParseSubscription([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(subscription.Nodes) != 1 || subscription.Nodes[0].Protocol != "vless" || subscription.Nodes[0].Name != "reality-node" {
		t.Fatalf("unexpected nodes: %#v", subscription.Nodes)
	}
	if subscription.Nodes[0].RealitySpiderX != "/" || subscription.UnsupportedCount != 1 || len(subscription.UnsupportedTypes) != 1 || subscription.UnsupportedTypes[0] != "vless" {
		t.Fatalf("unexpected subscription summary: %#v", subscription)
	}
	encoded, err := json.Marshal(subscription.Summaries())
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"00000000-0000-4000-8000-000000000001", publicKey, "reality.example.invalid", "cover.example.invalid"} {
		if strings.Contains(text, secret) {
			t.Fatalf("summary exposed VLESS secret or server value %q: %s", secret, text)
		}
	}
}

func TestParseSubscriptionRedactsInvalidVLESSRealityFields(t *testing.T) {
	secrets := []string{"00000000-0000-4000-8000-000000000001", "sensitive-public-key", "sensitive.example.invalid"}
	raw := `proxies:
  - name: reality-node
    type: vless
    server: sensitive.example.invalid
    port: 443
    uuid: 00000000-0000-4000-8000-000000000001
    network: tcp
    tls: true
    flow: xtls-rprx-vision
    servername: cover.example.invalid
    client-fingerprint: chrome
    reality-opts:
      public-key: sensitive-public-key
      short-id: 0123abcd
`
	_, err := ParseSubscription([]byte(raw))
	if err == nil {
		t.Fatal("invalid REALITY key was accepted")
	}
	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("parse error exposed secret %q: %v", secret, err)
		}
	}
}

func TestParseSubscriptionRejectsUnsupportedOrIncompleteInput(t *testing.T) {
	for name, raw := range map[string]string{
		"no supported node": `proxies: [{name: cdn, type: vless, server: example.invalid, port: 443}]`,
		"missing password":  `proxies: [{name: hy, type: hysteria2, server: example.invalid, port: 443}]`,
		"unsupported obfs": `proxies:
  - {name: hy, type: hysteria2, server: example.invalid, port: 443, password: test, obfs: unknown, obfs-password: test}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSubscription([]byte(raw)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestParseSubscriptionReturnsUnsupportedMetadata(t *testing.T) {
	subscription, err := ParseSubscription([]byte(`proxies:
  - {name: vless-node, type: vless, server: example.invalid, port: 443}
  - {name: vmess-node, type: vmess, server: example.invalid, port: 443}
`))
	if !errors.Is(err, ErrNoSupportedNodes) {
		t.Fatalf("expected ErrNoSupportedNodes, got %v", err)
	}
	if subscription.UnsupportedCount != 2 || len(subscription.UnsupportedTypes) != 2 {
		t.Fatalf("unexpected unsupported metadata: %#v", subscription)
	}
}

package outboundproxy

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	xraytls "github.com/xtls/xray-core/transport/internet/tls"
	"gopkg.in/yaml.v3"
)

const maxSubscriptionBytes = 4 << 20

var ErrNoSupportedNodes = errors.New("subscription contains no supported nodes")

var errUnsupportedNodeVariant = errors.New("unsupported node variant")

type Node struct {
	Protocol          string
	ID                string
	Name              string
	Server            string
	Port              int
	Password          string
	SNI               string
	SkipCertVerify    bool
	Obfs              string
	ObfsPassword      string
	UUID              string
	Flow              string
	Network           string
	ClientFingerprint string
	RealityPublicKey  string
	RealityShortID    string
	RealitySpiderX    string
}

type NodeSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

type Subscription struct {
	Nodes            []Node
	UnsupportedCount int
	UnsupportedTypes []string
}

type rawSubscription struct {
	Proxies []rawNode `yaml:"proxies"`
}

type rawNode struct {
	Name              string `yaml:"name"`
	Type              string `yaml:"type"`
	Server            string `yaml:"server"`
	Port              int    `yaml:"port"`
	Password          string `yaml:"password"`
	SNI               string `yaml:"sni"`
	SkipCertVerify    bool   `yaml:"skip-cert-verify"`
	Obfs              string `yaml:"obfs"`
	ObfsPassword      string `yaml:"obfs-password"`
	UUID              string `yaml:"uuid"`
	AlterID           int    `yaml:"alterId"`
	Cipher            string `yaml:"cipher"`
	Network           string `yaml:"network"`
	TLS               bool   `yaml:"tls"`
	ServerName        string `yaml:"servername"`
	Encryption        string `yaml:"encryption"`
	Flow              string `yaml:"flow"`
	ClientFingerprint string `yaml:"client-fingerprint"`
	RealityOptions    struct {
		PublicKey string `yaml:"public-key"`
		ShortID   string `yaml:"short-id"`
		SpiderX   string `yaml:"spider-x"`
	} `yaml:"reality-opts"`
	GRPCOptions struct {
		ServiceName string `yaml:"grpc-service-name"`
	} `yaml:"grpc-opts"`
	WSOptions struct {
		Path    string            `yaml:"path"`
		Headers map[string]string `yaml:"headers"`
	} `yaml:"ws-opts"`
}

func ParseSubscription(data []byte) (Subscription, error) {
	if len(data) == 0 {
		return Subscription{}, fmt.Errorf("subscription is empty")
	}
	if len(data) > maxSubscriptionBytes {
		return Subscription{}, fmt.Errorf("subscription exceeds 4 MiB")
	}
	var raw rawSubscription
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&raw); err != nil {
		return Subscription{}, fmt.Errorf("invalid Clash YAML")
	}
	if len(raw.Proxies) > 1000 {
		return Subscription{}, fmt.Errorf("subscription has too many nodes")
	}
	result := Subscription{}
	unsupported := map[string]struct{}{}
	for _, item := range raw.Proxies {
		kind := strings.ToLower(strings.TrimSpace(item.Type))
		var node Node
		var err error
		switch kind {
		case "hysteria2", "hy2":
			node, err = normalizeHysteria2Node(item)
		case "vless":
			node, err = normalizeVLESSRealityNode(item)
		default:
			result.UnsupportedCount++
			if kind != "" {
				unsupported[kind] = struct{}{}
			}
			continue
		}
		if errors.Is(err, errUnsupportedNodeVariant) {
			result.UnsupportedCount++
			unsupported[kind] = struct{}{}
			continue
		}
		if err != nil {
			return Subscription{}, err
		}
		result.Nodes = append(result.Nodes, node)
	}
	for kind := range unsupported {
		result.UnsupportedTypes = append(result.UnsupportedTypes, kind)
	}
	sort.Strings(result.UnsupportedTypes)
	if len(result.Nodes) == 0 {
		return result, ErrNoSupportedNodes
	}
	return result, nil
}

func normalizeHysteria2Node(item rawNode) (Node, error) {
	name := strings.TrimSpace(item.Name)
	server := strings.TrimSpace(item.Server)
	password := strings.TrimSpace(item.Password)
	if name == "" || server == "" || item.Port < 1 || item.Port > 65535 || password == "" {
		return Node{}, fmt.Errorf("a Hysteria2 node is missing name, server, port, or password")
	}
	if len(name) > 200 || len(server) > 253 || len(password) > 4096 || len(item.SNI) > 253 || len(item.ObfsPassword) > 4096 {
		return Node{}, fmt.Errorf("a Hysteria2 node contains an oversized field")
	}
	if net.ParseIP(server) == nil {
		if strings.ContainsAny(server, " /\\@?#") {
			return Node{}, fmt.Errorf("a Hysteria2 node has an invalid server name")
		}
	}
	obfs := strings.ToLower(strings.TrimSpace(item.Obfs))
	if obfs != "" && obfs != "salamander" {
		return Node{}, fmt.Errorf("Hysteria2 node %q uses unsupported obfuscation", name)
	}
	if obfs != "" && strings.TrimSpace(item.ObfsPassword) == "" {
		return Node{}, fmt.Errorf("Hysteria2 node %q is missing its obfuscation password", name)
	}
	fingerprint := strings.Join([]string{name, server, strconv.Itoa(item.Port), password, strings.TrimSpace(item.SNI), strconv.FormatBool(item.SkipCertVerify), obfs, item.ObfsPassword}, "\x00")
	sum := sha256.Sum256([]byte(fingerprint))
	return Node{
		Protocol: "hysteria2", ID: hex.EncodeToString(sum[:12]), Name: name, Server: server, Port: item.Port,
		Password: password, SNI: strings.TrimSpace(item.SNI), SkipCertVerify: item.SkipCertVerify,
		Obfs: obfs, ObfsPassword: item.ObfsPassword,
	}, nil
}

func normalizeVLESSRealityNode(item rawNode) (Node, error) {
	flow := strings.ToLower(strings.TrimSpace(item.Flow))
	network := strings.ToLower(strings.TrimSpace(item.Network))
	if network == "" {
		network = "tcp"
	}
	if network != "tcp" || flow != "xtls-rprx-vision" || !item.TLS {
		return Node{}, errUnsupportedNodeVariant
	}
	name := strings.TrimSpace(item.Name)
	server := strings.TrimSpace(item.Server)
	userID := strings.TrimSpace(item.UUID)
	serverName := strings.TrimSpace(item.ServerName)
	fingerprint := strings.ToLower(strings.TrimSpace(item.ClientFingerprint))
	publicKey := strings.TrimSpace(item.RealityOptions.PublicKey)
	shortID := strings.ToLower(strings.TrimSpace(item.RealityOptions.ShortID))
	spiderX := strings.TrimSpace(item.RealityOptions.SpiderX)
	if name == "" || server == "" || item.Port < 1 || item.Port > 65535 || userID == "" || serverName == "" || fingerprint == "" || publicKey == "" {
		return Node{}, fmt.Errorf("a VLESS Reality node is missing a required field")
	}
	if len(name) > 200 || len(server) > 253 || len(userID) > 64 || len(serverName) > 253 || len(fingerprint) > 64 || len(publicKey) > 128 || len(shortID) > 16 || len(spiderX) > 2048 {
		return Node{}, fmt.Errorf("a VLESS Reality node contains an oversized field")
	}
	if !validServerName(server) || !validServerName(serverName) {
		return Node{}, fmt.Errorf("a VLESS Reality node has an invalid server name")
	}
	if !validUUID(userID) {
		return Node{}, fmt.Errorf("a VLESS Reality node has an invalid UUID")
	}
	if fingerprint == "unsafe" || fingerprint == "hellogolang" || xraytls.GetFingerprint(fingerprint) == nil {
		return Node{}, fmt.Errorf("a VLESS Reality node has an invalid client fingerprint")
	}
	if encryption := strings.ToLower(strings.TrimSpace(item.Encryption)); encryption != "" && encryption != "none" {
		return Node{}, errUnsupportedNodeVariant
	}
	key, err := base64.RawURLEncoding.DecodeString(publicKey)
	if err != nil || len(key) != 32 {
		return Node{}, fmt.Errorf("a VLESS Reality node has an invalid public key")
	}
	if len(shortID)%2 != 0 {
		return Node{}, fmt.Errorf("a VLESS Reality node has an invalid short ID")
	}
	if _, err := hex.DecodeString(shortID); err != nil {
		return Node{}, fmt.Errorf("a VLESS Reality node has an invalid short ID")
	}
	if spiderX == "" {
		spiderX = "/"
	}
	if !strings.HasPrefix(spiderX, "/") {
		return Node{}, fmt.Errorf("a VLESS Reality node has an invalid spider path")
	}
	fingerprintValue := strings.Join([]string{
		"vless", name, server, strconv.Itoa(item.Port), userID, flow, network, serverName,
		fingerprint, publicKey, shortID, spiderX,
	}, "\x00")
	sum := sha256.Sum256([]byte(fingerprintValue))
	return Node{
		Protocol: "vless", ID: hex.EncodeToString(sum[:12]), Name: name, Server: server, Port: item.Port,
		UUID: userID, Flow: flow, Network: network, SNI: serverName, ClientFingerprint: fingerprint,
		RealityPublicKey: publicKey, RealityShortID: shortID, RealitySpiderX: spiderX,
	}, nil
}

func validServerName(value string) bool {
	if net.ParseIP(value) != nil {
		return true
	}
	return value != "" && !strings.ContainsAny(value, " /\\@?#")
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}

func (s Subscription) Summaries() []NodeSummary {
	items := make([]NodeSummary, 0, len(s.Nodes))
	for _, node := range s.Nodes {
		items = append(items, NodeSummary{ID: node.ID, Name: node.Name, Protocol: node.Protocol})
	}
	return items
}

func (s Subscription) Node(id string) (Node, bool) {
	for _, node := range s.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return Node{}, false
}

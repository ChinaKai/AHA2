package app

import (
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// TestAgentAPIReachabilityDistinguishesTunnelFromDirect pins how a successful
// detection is described.
//
// For a remote workspace the stored address is AHA's own loopback: it is what the
// reverse tunnel forwards to, and the Agent receives a per-Turn port instead. The
// address is kept unchanged because the tunnel gate keys off exactly that loopback
// form — so the label is the only thing that can stop the UI from claiming the
// workspace dials 127.0.0.1, which it cannot.
func TestAgentAPIReachabilityDistinguishesTunnelFromDirect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		transport string
		resolved  string
		want      string
	}{
		{"remote ssh on loopback is tunnelled", "ssh", "http://127.0.0.1:8766", "tunnel"},
		{"remote ssh on localhost is tunnelled", "ssh", "http://localhost:8766", "tunnel"},
		{"remote ssh on a routable address is direct", "ssh", "http://192.168.0.39:8766", "direct"},
		{"remote ssh behind https is direct", "ssh", "https://aha.example.com", "direct"},
		{"wsl runs on this host", "wsl", "http://127.0.0.1:8766", "direct"},
		{"native runs on this host", "native", "http://127.0.0.1:8766", "direct"},
	}
	for _, testCase := range cases {
		item := domain.Workspace{Transport: testCase.transport, Locality: "remote"}
		if got := agentAPIReachability(item, testCase.resolved); got != testCase.want {
			t.Errorf("%s: got %q want %q", testCase.name, got, testCase.want)
		}
	}
}

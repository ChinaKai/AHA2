package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestAccessScopeLocalRefusesNonLoopbackCallers(t *testing.T) {
	t.Parallel()
	gate := newAccessScopeGate(domain.AccessScopeLocal)
	cases := []struct {
		remote string
		allow  bool
		why    string
	}{
		{"127.0.0.1:5000", true, "loopback IPv4"},
		{"[::1]:5000", true, "loopback IPv6"},
		{"192.168.1.20:5000", false, "LAN address"},
		{"10.0.0.5:5000", false, "private address"},
		{"8.8.8.8:5000", false, "public address"},
		{"unparseable", false, "an address we cannot parse must not be trusted"},
		{"[fe80::1]:5000", false, "link-local is not loopback"},
	}
	for _, test := range cases {
		if got := gate.allows(test.remote); got != test.allow {
			t.Fatalf("local scope allows(%q) = %t, want %t (%s)", test.remote, got, test.allow, test.why)
		}
	}
}

func TestAccessScopeLANAnswersEveryone(t *testing.T) {
	t.Parallel()
	gate := newAccessScopeGate(domain.AccessScopeLAN)
	for _, remote := range []string{"127.0.0.1:5000", "192.168.1.20:5000", "10.0.0.5:5000", "8.8.8.8:5000"} {
		if !gate.allows(remote) {
			t.Fatalf("lan scope refused %q", remote)
		}
	}
}

func TestAccessScopeDefaultsToLAN(t *testing.T) {
	t.Parallel()
	// Anything unrecognised must fall back to the permissive-but-existing
	// behaviour: narrowing by accident would cut off clients that already work.
	for _, value := range []string{"", "  ", "LAN", "bogus", "local ", "LoCaL"} {
		gate := newAccessScopeGate(value)
		if value == "local " || value == "LoCaL" {
			if gate.current() != domain.AccessScopeLocal {
				t.Fatalf("scope %q resolved to %q, want local", value, gate.current())
			}
			continue
		}
		if gate.current() != domain.AccessScopeLAN {
			t.Fatalf("scope %q resolved to %q, want lan", value, gate.current())
		}
	}
}

func TestAccessScopeGateTakesEffectImmediately(t *testing.T) {
	t.Parallel()
	gate := newAccessScopeGate(domain.AccessScopeLAN)
	if !gate.allows("192.168.1.20:5000") {
		t.Fatal("lan scope should answer a LAN caller")
	}
	// Switching must apply without restarting: the listen address is fixed at
	// launch, so this is the only way to narrow access on a running server.
	gate.set(domain.AccessScopeLocal)
	if gate.allows("192.168.1.20:5000") {
		t.Fatal("switching to local must refuse a LAN caller immediately")
	}
}

func TestAccessScopeMiddlewareRefusesBeforeRouting(t *testing.T) {
	t.Parallel()
	reached := false
	handler := (&Server{accessScope: newAccessScopeGate(domain.AccessScopeLocal)}).enforceAccessScope(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }),
	)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system", nil)
	request.RemoteAddr = "192.168.1.20:5000"
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if reached {
		t.Fatal("a refused request must not reach the handler tree")
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/system", nil)
	request.RemoteAddr = "127.0.0.1:5000"
	handler.ServeHTTP(recorder, request)
	if !reached {
		t.Fatal("a loopback request must reach the handler tree")
	}
}

func TestAccessScopeAllowsWhenNoGateIsConfigured(t *testing.T) {
	t.Parallel()
	// Embedders that build a Server directly get no restriction, matching the
	// behaviour before this setting existed.
	if !(&Server{}).accessScopeAllows("192.168.1.20:5000") {
		t.Fatal("an unconfigured server must not refuse callers")
	}
}

package proxyconfig

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestProxySelectionHonorsSchemeAndNoProxy(t *testing.T) {
	settings := domain.ProxySettings{
		Mode:      "external",
		HTTPProxy: "http://127.0.0.1:7897", HTTPSProxy: "http://127.0.0.1:7898",
		NoProxy: "localhost,.internal.example,10.0.0.0/8",
	}
	proxy := proxyFunc(settings)
	for target, expected := range map[string]string{
		"http://example.com":              settings.HTTPProxy,
		"https://example.com":             settings.HTTPSProxy,
		"https://api.internal.example":    "",
		"https://localhost:1455/callback": "",
		"https://10.1.2.3/resource":       "",
	} {
		request, _ := http.NewRequest(http.MethodGet, target, nil)
		actual, err := proxy(request)
		if err != nil {
			t.Fatal(err)
		}
		value := ""
		if actual != nil {
			value = actual.String()
		}
		if value != expected {
			t.Fatalf("proxy for %s = %q, want %q", target, value, expected)
		}
	}
}

func TestNormalizeRejectsCredentialsAndUnsupportedSchemes(t *testing.T) {
	for _, value := range []string{"socks5://127.0.0.1:7897", "http://user:secret@127.0.0.1:7897", "127.0.0.1:7897"} {
		if _, err := Normalize(domain.ProxySettings{HTTPProxy: value}); err == nil {
			t.Fatalf("expected invalid proxy %q", value)
		}
	}
}

func TestNormalizeModesAndManagedDefaults(t *testing.T) {
	settings, err := Normalize(domain.ProxySettings{Mode: "managed_hysteria2"})
	if err != nil {
		t.Fatal(err)
	}
	if settings.ManagedRefreshIntervalMins != 1440 {
		t.Fatalf("unexpected refresh default: %d", settings.ManagedRefreshIntervalMins)
	}
	if _, err := Normalize(domain.ProxySettings{Mode: "unsupported"}); err == nil {
		t.Fatal("expected unsupported mode to fail")
	}
}

func TestNormalizeDefaultsOmittedModeToOff(t *testing.T) {
	settings, err := Normalize(domain.ProxySettings{
		HTTPProxy: "http://127.0.0.1:7897", HTTPSProxy: "http://127.0.0.1:7897",
	})
	if err != nil {
		t.Fatal(err)
	}
	if settings.Mode != "off" {
		t.Fatalf("omitted mode normalized to %q, want off", settings.Mode)
	}
	for _, mode := range []string{"off", "external", "managed_hysteria2"} {
		normalized, err := Normalize(domain.ProxySettings{Mode: mode})
		if err != nil || normalized.Mode != mode {
			t.Fatalf("explicit mode %q changed: %#v %v", mode, normalized, err)
		}
	}
}

func TestOffModeDoesNotInjectProxyEnvironment(t *testing.T) {
	environment := map[string]string{
		"HTTP_PROXY": "http://legacy.example:7897", "HTTPS_PROXY": "http://legacy.example:7897",
		"NO_PROXY": "old.example", "KEEP": "1",
	}
	ApplyEnvironment(environment, domain.ProxySettings{
		Mode: "off", HTTPProxy: "http://stored.example:7897", HTTPSProxy: "http://stored.example:7897",
		NoProxy: "localhost,127.0.0.1,::1",
	})
	if _, ok := environment["HTTP_PROXY"]; ok {
		t.Fatalf("off mode kept HTTP_PROXY: %#v", environment)
	}
	if _, ok := environment["HTTPS_PROXY"]; ok {
		t.Fatalf("off mode kept HTTPS_PROXY: %#v", environment)
	}
	if environment["NO_PROXY"] != "localhost,127.0.0.1,::1" || environment["KEEP"] != "1" {
		t.Fatalf("unexpected environment after off mode: %#v", environment)
	}

	external := map[string]string{}
	ApplyEnvironment(external, domain.ProxySettings{
		Mode: "external", HTTPProxy: "http://stored.example:7897", HTTPSProxy: "http://stored.example:8443",
		NoProxy: "localhost",
	})
	if external["HTTP_PROXY"] != "http://stored.example:7897" || external["HTTPS_PROXY"] != "http://stored.example:8443" {
		t.Fatalf("external mode did not inject its addresses: %#v", external)
	}
}

func TestOffModeClientConnectsDirectlyEvenWithStoredAddress(t *testing.T) {
	var proxyHits atomic.Int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		http.Error(writer, "proxy must not be used", http.StatusBadGateway)
	}))
	defer proxyServer.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "origin")
	}))
	defer origin.Close()

	client, err := Client(nil, domain.ProxySettings{
		Mode: "off", HTTPProxy: proxyServer.URL, HTTPSProxy: proxyServer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "origin" || proxyHits.Load() != 0 {
		t.Fatalf("off mode did not go direct: body=%q proxy_hits=%d", strings.TrimSpace(string(body)), proxyHits.Load())
	}
}

func TestExternalModeClientUsesStoredProxy(t *testing.T) {
	var proxyHits atomic.Int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxyHits.Add(1)
		if request.URL.Host != "origin.invalid" {
			t.Errorf("proxy received %q, want origin.invalid", request.URL.String())
		}
		_, _ = io.WriteString(writer, "proxied")
	}))
	defer proxyServer.Close()

	client, err := Client(nil, domain.ProxySettings{
		Mode: "external", HTTPProxy: proxyServer.URL, HTTPSProxy: proxyServer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("http://origin.invalid/resource")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "proxied" || proxyHits.Load() != 1 {
		t.Fatalf("external mode did not use its proxy: body=%q hits=%d", strings.TrimSpace(string(body)), proxyHits.Load())
	}
}

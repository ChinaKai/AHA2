package proxyconfig

import (
	"net/http"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestProxySelectionHonorsSchemeAndNoProxy(t *testing.T) {
	settings := domain.ProxySettings{
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

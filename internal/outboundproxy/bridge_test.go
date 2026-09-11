package outboundproxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestProxyBridgeRequiresAuthenticationAndForwardsHTTP(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Target", "yes")
		_, _ = writer.Write([]byte("through-bridge"))
	}))
	defer target.Close()
	bridge, err := newProxyBridge((&net.Dialer{}).DialContext)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = bridge.Close(ctx)
	}()

	proxyURL, _ := url.Parse(bridge.URL())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 3 * time.Second}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "through-bridge" || response.Header.Get("X-Target") != "yes" {
		t.Fatalf("unexpected forwarded response: status=%d body=%q", response.StatusCode, body)
	}

	unauthenticatedURL := *proxyURL
	unauthenticatedURL.User = nil
	unauthenticated := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&unauthenticatedURL)}, Timeout: 3 * time.Second}
	response, err = unauthenticated.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("expected 407 without credentials, got %d", response.StatusCode)
	}
}

func TestProxyBridgeForwardsHTTPSConnect(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("secure-through-bridge"))
	}))
	defer target.Close()
	bridge, err := newProxyBridge((&net.Dialer{}).DialContext)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = bridge.Close(ctx)
	}()
	proxyURL, _ := url.Parse(bridge.URL())
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // test server certificate
	}, Timeout: 3 * time.Second}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "secure-through-bridge" {
		t.Fatalf("unexpected CONNECT response: status=%d body=%q", response.StatusCode, body)
	}
}

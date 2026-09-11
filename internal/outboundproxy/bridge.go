package outboundproxy

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type proxyBridge struct {
	listener  net.Listener
	server    *http.Server
	dial      dialContextFunc
	transport *http.Transport
	secret    string
	url       string
	once      sync.Once
}

func newProxyBridge(dial dialContextFunc) (*proxyBridge, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start managed proxy bridge: %w", err)
	}
	secretBytes := make([]byte, 24)
	if _, err := rand.Read(secretBytes); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("create managed proxy credential: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	bridge := &proxyBridge{listener: listener, dial: dial, secret: secret}
	bridge.transport = &http.Transport{DialContext: dial, ForceAttemptHTTP2: true}
	bridge.server = &http.Server{
		Handler: bridge, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second,
	}
	bridge.url = (&url.URL{Scheme: "http", Host: listener.Addr().String(), User: url.UserPassword("aha", secret)}).String()
	go func() { _ = bridge.server.Serve(listener) }()
	return bridge, nil
}

func (b *proxyBridge) URL() string { return b.url }

func (b *proxyBridge) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	username, password, ok := proxyBasicAuth(request.Header.Get("Proxy-Authorization"))
	if !ok || username != "aha" || subtle.ConstantTimeCompare([]byte(password), []byte(b.secret)) != 1 {
		writer.Header().Set("Proxy-Authenticate", `Basic realm="AHA2"`)
		http.Error(writer, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if request.Method == http.MethodConnect {
		b.connect(writer, request)
		return
	}
	b.forward(writer, request)
}

func proxyBasicAuth(value string) (string, string, bool) {
	const prefix = "Basic "
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value[len(prefix):]))
	if err != nil {
		return "", "", false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	return username, password, ok
}

func (b *proxyBridge) connect(writer http.ResponseWriter, request *http.Request) {
	if request.Host == "" {
		http.Error(writer, "destination required", http.StatusBadRequest)
		return
	}
	upstream, err := b.dial(request.Context(), "tcp", request.Host)
	if err != nil {
		http.Error(writer, "destination unavailable", http.StatusBadGateway)
		return
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(writer, "tunneling unavailable", http.StatusInternalServerError)
		return
	}
	downstream, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
	_ = buffered.Flush()
	go relayConnections(downstream, upstream)
}

func (b *proxyBridge) forward(writer http.ResponseWriter, request *http.Request) {
	outbound := request.Clone(request.Context())
	outbound.RequestURI = ""
	outbound.Header = request.Header.Clone()
	removeHopHeaders(outbound.Header)
	outbound.Header.Del("Proxy-Authorization")
	response, err := b.transport.RoundTrip(outbound)
	if err != nil {
		http.Error(writer, "destination unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	removeHopHeaders(response.Header)
	for key, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
}

func relayConnections(left, right net.Conn) {
	defer left.Close()
	defer right.Close()
	done := make(chan struct{}, 2)
	copyOne := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyOne(left, right)
	go copyOne(right, left)
	<-done
}

func removeHopHeaders(header http.Header) {
	for _, value := range strings.Split(header.Get("Connection"), ",") {
		header.Del(strings.TrimSpace(value))
	}
	for _, key := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(key)
	}
}

func (b *proxyBridge) Close(ctx context.Context) error {
	var err error
	b.once.Do(func() {
		b.transport.CloseIdleConnections()
		err = b.server.Shutdown(ctx)
	})
	return err
}

package proxyconfig

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func Normalize(input domain.ProxySettings) (domain.ProxySettings, error) {
	input.HTTPProxy = strings.TrimSpace(input.HTTPProxy)
	input.HTTPSProxy = strings.TrimSpace(input.HTTPSProxy)
	input.NoProxy = strings.TrimSpace(input.NoProxy)
	for name, value := range map[string]string{"HTTP_PROXY": input.HTTPProxy, "HTTPS_PROXY": input.HTTPSProxy} {
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return domain.ProxySettings{}, fmt.Errorf("%s must be an http:// or https:// URL", name)
		}
		if parsed.User != nil {
			return domain.ProxySettings{}, fmt.Errorf("%s credentials are not supported in phase one", name)
		}
	}
	return input, nil
}

func ApplyEnvironment(environment map[string]string, settings domain.ProxySettings) {
	for key, value := range map[string]string{
		"HTTP_PROXY": settings.HTTPProxy, "HTTPS_PROXY": settings.HTTPSProxy, "NO_PROXY": settings.NoProxy,
	} {
		if value != "" {
			environment[key] = value
		} else {
			delete(environment, key)
		}
	}
}

func Client(base *http.Client, settings domain.ProxySettings) (*http.Client, error) {
	normalized, err := Normalize(settings)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxyFunc(normalized)
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	if base != nil && base.Timeout > 0 {
		client.Timeout = base.Timeout
	}
	return client, nil
}

func proxyFunc(settings domain.ProxySettings) func(*http.Request) (*url.URL, error) {
	return func(request *http.Request) (*url.URL, error) {
		if bypassProxy(request.URL.Hostname(), request.URL.Port(), settings.NoProxy) {
			return nil, nil
		}
		value := settings.HTTPProxy
		if request.URL.Scheme == "https" {
			value = settings.HTTPSProxy
		}
		if value == "" {
			return nil, nil
		}
		return url.Parse(value)
	}
}

func bypassProxy(host, port, noProxy string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "" {
		return false
	}
	for _, raw := range strings.Split(noProxy, ",") {
		entry := strings.TrimSpace(strings.ToLower(raw))
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		if _, network, err := net.ParseCIDR(entry); err == nil {
			if address := net.ParseIP(host); address != nil && network.Contains(address) {
				return true
			}
			continue
		}
		entryHost, entryPort := entry, ""
		if parsedHost, parsedPort, err := net.SplitHostPort(entry); err == nil {
			entryHost, entryPort = strings.Trim(parsedHost, "[]"), parsedPort
		}
		if entryPort != "" && entryPort != port {
			continue
		}
		entryHost = strings.TrimPrefix(entryHost, ".")
		if host == entryHost || strings.HasSuffix(host, "."+entryHost) {
			return true
		}
	}
	return false
}

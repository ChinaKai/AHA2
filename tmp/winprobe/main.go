package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/gateway"
	"github.com/ChinaKai/AHA2/internal/secrets"
)

type lt struct{ inner http.RoundTripper }

func (t lt) RoundTrip(r *http.Request) (*http.Response, error) {
	s := time.Now()
	resp, err := t.inner.RoundTrip(r)
	code := 0
	if resp != nil {
		code = resp.StatusCode
	}
	fmt.Printf("  %-40s %6.2fs http=%d err=%v\n", strings.TrimPrefix(r.URL.Path, "/"), time.Since(s).Seconds(), code, err)
	return resp, err
}

func main() {
	store, err := secrets.Open(`D:\ProgramData\AHA2\secrets.json`)
	if err != nil {
		fmt.Println("secret open error:", err)
		return
	}
	key, ok := store.Get("provider/hualai-claw-new-2ff1422f3902/credential")
	if !ok || key == "" {
		fmt.Println("credential missing")
		return
	}
	models := []string{
		"codex-gpt-5.6-terra", "gpt-6.1-terra", "claude-gpt-6.1-terra", "claude-kimi-k3.1-pro",
		"gpt-6.1-sol", "claude-gpt-6.1-sol", "claude-glm-5.3", "codex-gpt-5.6-sol",
		"claude-gpt-5.6-luna", "codex-gpt-5.6-luna", "claude-glm-5.3-flash", "codex-glm-5.3-flash",
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, model := range models {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			c := &http.Client{Timeout: 8 * time.Second, Transport: lt{http.DefaultTransport}}
			start := time.Now()
			caps, _ := gateway.ProbeModelCapabilitiesContextWithClient(context.Background(), "https://tokens-api.hualaikeji.com", key, "bearer", model, 8*time.Second, c)
			mu.Lock()
			fmt.Printf("RESULT %s total=%.2fs caps=%v\n", model, time.Since(start).Seconds(), caps)
			mu.Unlock()
		}(model)
	}
	wg.Wait()
	os.Exit(0)
}

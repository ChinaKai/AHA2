package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/gateway"
	"github.com/ChinaKai/AHA2/internal/secrets"
)

type lt struct{ inner http.RoundTripper }

func (t lt) RoundTrip(r *http.Request) (*http.Response, error) {
	s := time.Now()
	resp, err := t.inner.RoundTrip(r)
	d := time.Since(s)
	code := 0
	if resp != nil {
		code = resp.StatusCode
	}
	fmt.Printf("  %-22s %6.2fs http=%d err=%v\n", strings.TrimPrefix(r.URL.Path, "/"), d.Seconds(), code, err)
	return resp, err
}

func main() {
	st, _ := secrets.Open("/mnt/d/ProgramData/AHA2/secrets.json")
	key, _ := st.Get("provider/hualai-claw-new-2ff1422f3902/credential")
	model := "codex-gpt-5.6-terra"
	for _, style := range []string{"bearer", "x-api-key", "auto"} {
		c := &http.Client{Timeout: 8 * time.Second, Transport: lt{http.DefaultTransport}}
		start := time.Now()
		m, _ := gateway.ProbeModelCapabilitiesContextWithClient(context.Background(), "https://tokens-api.hualaikeji.com", key, style, model, 8*time.Second, c)
		fmt.Printf("STYLE=%s total=%.2fs %v\n", style, time.Since(start).Seconds(), m)
	}
	for i := 1; i <= 8; i++ {
		c := &http.Client{Timeout: 8 * time.Second, Transport: lt{http.DefaultTransport}}
		start := time.Now()
		m, _ := gateway.ProbeModelCapabilitiesContextWithClient(context.Background(), "https://tokens-api.hualaikeji.com", key, "bearer", model, 8*time.Second, c)
		fmt.Printf("ROUND=%d total=%.2fs %v\n", i, time.Since(start).Seconds(), m)
	}
	os.Exit(0)
}

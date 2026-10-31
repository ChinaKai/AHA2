package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/gateway"
	"github.com/ChinaKai/AHA2/internal/secrets"
)

func main() {
	store, err := secrets.Open("/mnt/d/ProgramData/AHA2/secrets.json")
	if err != nil { fmt.Println("secret open error:", err); return }
	key, ok := store.Get("provider/hualai-claw-new-2ff1422f3902/credential")
	if !ok || key == "" { fmt.Println("credential missing"); return }
	for _, model := range []string{"gpt-6.1-terra", "claude-gpt-6.1-terra", "claude-kimi-k3.1-pro"} {
		start := time.Now()
		caps, anthropicBase := gateway.ProbeModelCapabilitiesContext(context.Background(), "https://tokens-api.hualaikeji.com", key, "bearer", model, 15*time.Second)
		fmt.Printf("%s elapsed=%s caps=%v anthropic=%s\n", model, time.Since(start), caps, anthropicBase)
	}
}

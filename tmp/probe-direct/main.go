package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/gateway"
	"github.com/ChinaKai/AHA2/internal/secrets"
)

func main() {
	st, _ := secrets.Open("/mnt/d/ProgramData/AHA2/secrets.json")
	key, _ := st.Get("provider/hualai-claw-new-2ff1422f3902/credential")
	start := time.Now()
	caps, base, errs := gateway.ProbeModelCapabilitiesContextWithClientDetailed(context.Background(), "https://tokens-api.hualaikeji.com", key, "bearer", "codex-gpt-5.6-terra", 25*time.Second, nil)
	fmt.Printf("direct concurrent probe total=%s caps=%v base=%q errors=%v\n", time.Since(start), caps, base, errs)
}

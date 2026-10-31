package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/secrets"
	syncer "github.com/ChinaKai/AHA2/internal/sync"
)

func main() {
	store, err := secrets.Open("/mnt/d/ProgramData/AHA2/secrets.json")
	if err != nil {
		fmt.Println("secrets:", err)
		os.Exit(1)
	}
	token, ok := store.Get("sync/default/token")
	if !ok || token == "" {
		fmt.Println("sync token missing")
		os.Exit(1)
	}
	client := &syncer.Client{
		BaseURL: "https://101.201.66.192", DeviceID: "dev_f977219682439e08ad3ebbf57e1ebd1a",
		Credential: func(context.Context) (string, error) { return token, nil },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	pull, err := client.Pull(ctx, "default", "309004", "dev_f977219682439e08ad3ebbf57e1ebd1a", 5)
	fmt.Printf("PULL duration=%.2fs objects=%d cursor=%q has_more=%v err=%v\n", time.Since(start).Seconds(), len(pull.Objects), pull.Cursor, pull.HasMore, err)

	probe := domain.SyncObject{
		Type: "model", ID: "model_a3a5a98d28ef5784a42e", Operation: "upsert",
		Payload: json.RawMessage(`{"id":"model_a3a5a98d28ef5784a42e","display_name":"diag-probe"}`),
		BaseVersion: "99999999", IdempotencyKey: fmt.Sprintf("diag-%d", time.Now().UnixNano()),
	}
	start = time.Now()
	resp, err := client.Push(ctx, syncer.PushRequest{Scope: "default", DeviceID: "dev_f977219682439e08ad3ebbf57e1ebd1a", Objects: []domain.SyncObject{probe}})
	fmt.Printf("PUSH duration=%.2fs acked=%d conflicts=%d err=%v\n", time.Since(start).Seconds(), len(resp.AckedKeys), len(resp.Conflicts), err)
	if len(resp.Conflicts) > 0 {
		for _, c := range resp.Conflicts {
			fmt.Printf("  conflict type=%s id=%s local_version=%q remote_version=%q local_bytes=%d remote_bytes=%d\n", c.ObjectType, c.ObjectID, c.LocalVersion, c.RemoteVersion, len(c.LocalPayload), len(c.RemotePayload))
		}
	}
}

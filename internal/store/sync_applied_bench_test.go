package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// BenchmarkSyncAppliedLookup compares the point-query loop the sync runner
// used to run per exported object with the batched lookup that replaced it.
// Seed size is kept small so the benchmark stays usable; the ratio is what the
// no-change sync path scales with.
func BenchmarkSyncAppliedLookup(b *testing.B) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(b.TempDir(), "aha2.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	keys := make([]string, 2000)
	now := time.Now().UTC()
	for index := range keys {
		keys[index] = fmt.Sprintf("applied-%05d", index)
		if err := database.MarkSyncApplied(ctx, "default", domain.SyncObject{
			Type: "test", ID: keys[index], IdempotencyKey: keys[index],
		}, now); err != nil {
			b.Fatal(err)
		}
	}

	b.Run("batched", func(b *testing.B) {
		b.ReportAllocs()
		for iteration := 0; iteration < b.N; iteration++ {
			if _, err := database.SyncAppliedKeys(ctx, keys); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("point-loop", func(b *testing.B) {
		b.ReportAllocs()
		for iteration := 0; iteration < b.N; iteration++ {
			for _, key := range keys {
				if _, err := database.SyncWasApplied(ctx, key); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}

package sync

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func benchmarkStore(b *testing.B) *store.Store {
	b.Helper()
	database, err := store.Open(context.Background(), filepath.Join(b.TempDir(), "aha2.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	return database
}

// BenchmarkUnchangedHistoryExport compares the old preview preparation
// (materialize every object, then issue one SyncWasApplied query per object)
// with the incremental export used by scheduled runs. Setup marks the whole
// history applied, so the measured run is the no-change path.
func BenchmarkUnchangedHistoryExport(b *testing.B) {
	for _, items := range []int{1000, 4000} {
		b.Run(fmt.Sprintf("conversation_items=%d", items), func(b *testing.B) {
			ctx := context.Background()
			database := benchmarkStore(b)
			task := seedSyncExportTask(b, database)
			now := time.Now().UTC()
			for index := 0; index < items; index++ {
				if _, err := database.AddConversationItem(ctx, domain.ConversationItem{
					ID: fmt.Sprintf("conversation_bench_%05d", index), TaskID: task.ID,
					Category: "chat", Kind: "user_message", Summary: "benchmark",
					CreatedAt: now.Add(time.Duration(index) * time.Microsecond),
				}); err != nil {
					b.Fatal(err)
				}
			}
			applied, err := ExportBusinessObjectsForDevice(ctx, database, "device-bench")
			if err != nil {
				b.Fatal(err)
			}
			for _, object := range applied {
				if err := database.MarkSyncApplied(ctx, "default", object, now); err != nil {
					b.Fatal(err)
				}
			}

			b.Run("full_export_and_point_lookups", func(b *testing.B) {
				for iteration := 0; iteration < b.N; iteration++ {
					objects, err := ExportBusinessObjectsForDevice(ctx, database, "device-bench")
					if err != nil {
						b.Fatal(err)
					}
					for _, object := range objects {
						if _, err := database.SyncWasApplied(ctx, object.IdempotencyKey); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
			b.Run("incremental_export", func(b *testing.B) {
				for iteration := 0; iteration < b.N; iteration++ {
					pending, err := ExportPendingBusinessObjectsForDevice(ctx, database, "device-bench")
					if err != nil {
						b.Fatal(err)
					}
					if len(pending) != 0 {
						b.Fatalf("pending objects=%d, want 0", len(pending))
					}
				}
			})
		})
	}
}

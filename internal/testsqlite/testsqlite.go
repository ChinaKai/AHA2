// Package testsqlite keeps SQLite-backed tests fast on disks where fsync is
// expensive.
//
// Every test that opens a store runs the full migration chain against a fresh
// database file, which is several hundred commits. With SQLite's default
// synchronous mode each commit waits for an fsync; on a disk where that costs
// around 10 ms a single store.Open takes about six seconds, and the app,
// httpapi and store packages cannot finish inside go test's 10 minute limit.
// Test databases live in t.TempDir and are thrown away, so they do not need to
// survive a power loss.
//
// Import this package for its side effect from a _test.go file:
//
//	import _ "github.com/ChinaKai/AHA2/internal/testsqlite"
//
// It must never be imported by production code. As a second line of defence
// the hook is only installed inside a test binary.
package testsqlite

import (
	"context"
	"testing"

	sqlite "modernc.org/sqlite"
)

func init() {
	if !testing.Testing() {
		return
	}
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, _ string) error {
		_, err := conn.ExecContext(context.Background(), "PRAGMA synchronous=OFF", nil)
		return err
	})
}

package testsqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestConnectionsSkipFsyncInTests(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Two connections: the hook has to reach every connection in the pool, not
	// only the first one.
	db.SetMaxOpenConns(2)
	first, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, conn := range []*sql.Conn{first, second} {
		var mode int
		if err := conn.QueryRowContext(t.Context(), "PRAGMA synchronous").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if mode != 0 {
			t.Fatalf("synchronous = %d, want 0 (OFF)", mode)
		}
	}
}

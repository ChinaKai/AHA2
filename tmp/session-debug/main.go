package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:/mnt/d/ProgramData/AHA2/aha2.db?mode=rwc")
	if err != nil { panic(err) }
	defer db.Close()
	raw := "debug-session-7f3781a60841b9328742"
	sum := sha256.Sum256([]byte(raw))
	now := time.Now().UTC()
	_, err = db.Exec(`INSERT OR REPLACE INTO sessions(id,owner_id,token_hash,csrf_token,created_at,last_seen_at,expires_at,revoked_at) VALUES(?,?,?,?,?,?,?,?)`,
		"session_debug_probe", "owner_7f3781a60841b9328742", hex.EncodeToString(sum[:]), "debug-csrf", now.Format(time.RFC3339), now.Format(time.RFC3339), now.Add(2*time.Hour).Format(time.RFC3339), "")
	if err != nil { panic(err) }
	fmt.Println(raw)
}

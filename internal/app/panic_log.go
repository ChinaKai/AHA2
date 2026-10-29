package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

func recordPanic(dataDir, scope string, recovered any) {
	if dataDir == "" {
		return
	}
	path := filepath.Join(dataDir, "panics.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "\n[%s] %s: %v\n%s\n", time.Now().Format(time.RFC3339), scope, recovered, debug.Stack())
}

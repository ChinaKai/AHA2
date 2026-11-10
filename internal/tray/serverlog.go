package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// ServerLogMaxBytes bounds the live server log. When it is exceeded the
	// current file is renamed to <name>.1, so at most two files are kept.
	ServerLogMaxBytes int64 = 10 << 20
	serverLogName           = "aha2-server.log"
)

// ServerLogPath is the persistent log for the managed server's stdout/stderr.
// The tray creates the logs directory on demand; the path is also documented
// in README.md and docs/deployment.md so an operator can find crash output.
func ServerLogPath(dataDir string) string {
	return filepath.Join(dataDir, "logs", serverLogName)
}

// rotatingLog collects the server's stdout/stderr in one size-bounded file.
// The server is started in a hidden window, so this file is the only place a
// Go panic, runtime fatal error, or startup failure can survive the process.
type rotatingLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

func openServerLog(path string, maxBytes int64) (*rotatingLog, error) {
	if maxBytes <= 0 {
		maxBytes = ServerLogMaxBytes
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create server log directory: %w", err)
	}
	file, err := openServerLogFile(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat server log: %w", err)
	}
	// Best effort: on Windows the inherited data-directory ACL is usually
	// already user-scoped, and a logging setup problem must not stop AHA.
	_ = restrictLogAccess(path)
	return &rotatingLog{path: path, maxBytes: maxBytes, file: file, size: info.Size()}, nil
}

func openServerLogFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open server log: %w", err)
	}
	return file, nil
}

func (l *rotatingLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return 0, os.ErrClosed
	}
	// Rotate before appending so a long-running process cannot exceed the cap
	// by much. A single oversized write is kept whole: panic output matters
	// more than a hard size limit.
	if l.size > 0 && l.size+int64(len(data)) > l.maxBytes {
		_ = l.rotateLocked()
	}
	n, err := l.file.Write(data)
	l.size += int64(n)
	return n, err
}

func (l *rotatingLog) rotateLocked() error {
	if l.file == nil {
		return os.ErrClosed
	}
	closeErr := l.file.Close()
	l.file = nil
	backup := l.path + ".1"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return l.reopenLocked(closeErr)
	}
	if err := os.Rename(l.path, backup); err != nil {
		return l.reopenLocked(err)
	}
	file, err := openServerLogFile(l.path)
	if err != nil {
		return err
	}
	_ = restrictLogAccess(l.path)
	l.file = file
	l.size = 0
	if closeErr != nil {
		return closeErr
	}
	return nil
}

// reopenLocked restores a writable handle after a partial rotation. If even
// that fails, the next Write reports os.ErrClosed instead of panicking.
func (l *rotatingLog) reopenLocked(cause error) error {
	file, err := openServerLogFile(l.path)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	if statErr == nil {
		l.size = info.Size()
	}
	l.file = file
	return cause
}

func (l *rotatingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// writeEntry prefixes a timestamp and appends one diagnostic line.
func (l *rotatingLog) writeEntry(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	_, _ = l.Write([]byte(time.Now().Format("2006-01-02T15:04:05.000Z07:00") + " " + line + "\n"))
}

// formatCommand renders the launched command without environment contents.
// Command arguments never carry credentials: the tray validates that the
// agent API URL has no user-info, query, or fragment.
func formatCommand(command Command) string {
	parts := make([]string, 0, len(command.Args)+1)
	parts = append(parts, quoteCommandArg(command.Path))
	for _, arg := range command.Args {
		parts = append(parts, quoteCommandArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteCommandArg(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\"") {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return value
}

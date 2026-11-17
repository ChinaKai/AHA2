// Package instancelock coordinates the one AHA2 control plane that may own a
// data directory. The service holds an OS advisory lock for its whole lifetime;
// the offline maintenance CLI acquires the same lock non-blockingly. An idle
// service keeps no SQLite transaction open, so the SQLite file lock alone cannot
// tell the CLI that the service is still serving the Web UI. This lock is the
// cross-process signal for that case.
package instancelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the advisory lock file under the AHA2 data directory. It is
// intentionally not a pid file: a crashed process releases the OS lock
// automatically and a stale file does not keep a later start out.
const FileName = "aha2.instance.lock"

// ErrHeld means another process currently owns the data directory.
var ErrHeld = errors.New("another AHA2 process is using this data directory")

// Lock is the held advisory lock. Close releases it.
type Lock struct {
	file *os.File
	path string
}

// Acquire takes an exclusive, non-blocking advisory lock for dataDir. The data
// directory is created when missing. A second Acquire from this or another
// process returns ErrHeld instead of waiting.
func Acquire(dataDir string) (*Lock, error) {
	if dataDir == "" {
		return nil, errors.New("data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dataDir, FileName)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if isHeld(err) {
			return nil, fmt.Errorf("%w: %s", ErrHeld, dataDir)
		}
		return nil, fmt.Errorf("lock data directory: %w", err)
	}
	return &Lock{file: file, path: path}, nil
}

// Path reports the lock file path.
func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Close releases the lock. It is safe to call more than once.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unlockFile(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

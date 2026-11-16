package knowledgevault

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AutoIndexInterval is how often the vault is polled for external edits. A
// poll is used instead of a platform watcher so Windows, Linux and WSL behave
// the same and no extra native dependency ships with the control plane.
const AutoIndexInterval = 3 * time.Second

// autoIndexFingerprint summarizes every Markdown file and directory the index
// reads. File sizes and mtimes are enough to notice an editor save; directory
// entries are included so creating an empty folder (which gets a generated
// index.md) still triggers a rebuild. Content hashes would reread the whole
// vault every tick.
func autoIndexFingerprint(vaultPath string) (string, error) {
	var builder strings.Builder
	err := filepath.WalkDir(vaultPath, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if strings.HasPrefix(name, ".") || skippedDirectoryNames[name] {
				return filepath.SkipDir
			}
			if relative, err := filepath.Rel(vaultPath, filePath); err == nil && relative != "." {
				builder.WriteString(filepath.ToSlash(relative))
				builder.WriteString("|dir\n")
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			return nil
		}
		relative, err := filepath.Rel(vaultPath, filePath)
		if err != nil || relative == readmeFileName {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		builder.WriteString(filepath.ToSlash(relative))
		builder.WriteByte('|')
		builder.WriteString(fmt.Sprintf("%d|%d\n", info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		return "", err
	}
	return hashBytes([]byte(builder.String())), nil
}

// StartAutoIndex begins the single-writer auto-index loop. It is idempotent
// and stops on Close or when ctx is cancelled. The first tick establishes a
// baseline; only later changes trigger a rebuild. The startup reconciliation
// performs the initial repair.
func (m *Manager) StartAutoIndex(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = AutoIndexInterval
	}
	m.autoMu.Lock()
	if m.autoCancel != nil {
		m.autoMu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.autoCancel = cancel
	m.autoMu.Unlock()
	// Establish the baseline before returning so a caller that edits the vault
	// immediately after StartAutoIndex still produces a change tick.
	baseline, _ := autoIndexFingerprint(m.config.VaultPath)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
			fingerprint, err := autoIndexFingerprint(m.config.VaultPath)
			if err != nil {
				m.recordAutoIndexError(fmt.Errorf("scan vault: %w", err))
				continue
			}
			if fingerprint == baseline {
				continue
			}
			baseline = fingerprint
			m.enqueueAutoIndex(runCtx)
		}
	}()
}

// autoIndexState is guarded by autoMu; it is separate from m.mu so Status can
// read health without waiting for a long rebuild.
type autoIndexState struct {
	lastAt     time.Time
	lastError  string
	failures   int
	running    bool
	pending    int
	lastReport RebuildReport
}

func (m *Manager) recordAutoIndexError(err error) {
	m.autoMu.Lock()
	m.auto.failures++
	m.auto.lastError = err.Error()
	m.autoMu.Unlock()
	log.Printf("automatic knowledge index update failed: %v", err)
}

// enqueueAutoIndex coalesces bursts into at most one queued rebuild. A running
// rebuild records one pending tick; the loop immediately picks it up after.
func (m *Manager) enqueueAutoIndex(ctx context.Context) {
	m.autoMu.Lock()
	if m.auto.running {
		m.auto.pending = 1
		m.autoMu.Unlock()
		return
	}
	m.auto.running = true
	m.autoMu.Unlock()
	go m.runAutoIndexLoop(context.WithoutCancel(ctx))
}

func (m *Manager) runAutoIndexLoop(ctx context.Context) {
	for {
		report, err := m.Rebuild(ctx)
		m.autoMu.Lock()
		m.auto.lastAt = time.Now().UTC()
		if err != nil {
			m.auto.failures++
			m.auto.lastError = err.Error()
		} else {
			m.auto.failures = 0
			m.auto.lastError = ""
			m.auto.lastReport = report
		}
		pending := m.auto.pending
		m.auto.pending = 0
		if pending == 0 {
			m.auto.running = false
			m.autoMu.Unlock()
			return
		}
		m.autoMu.Unlock()
	}
}

// AutoIndexHealth is exposed in the read-only settings panel. It never offers
// a manual rebuild; a failing loop is reported as "needs attention".
type AutoIndexHealth struct {
	LastAt    time.Time `json:"last_at,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	Failures  int       `json:"failures"`
	Running   bool      `json:"running"`
	Pending   int       `json:"pending"`
	Entries   int       `json:"entries,omitempty"`
}

// AutoIndexHealth returns a snapshot safe to render while a rebuild is running.
func (m *Manager) AutoIndexHealth() AutoIndexHealth {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	return AutoIndexHealth{
		LastAt: m.auto.lastAt, LastError: m.auto.lastError, Failures: m.auto.failures,
		Running: m.auto.running, Pending: m.auto.pending, Entries: m.auto.lastReport.Entries,
	}
}

func (m *Manager) stopAutoIndex() {
	m.autoMu.Lock()
	cancel := m.autoCancel
	m.autoCancel = nil
	m.autoMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// RecoverIndex repairs a missing or corrupt index from the files. A corrupt
// index file is moved aside (never deleted in place) and rebuilt; if even that
// fails, the error is returned and the caller keeps the previous state.
func (m *Manager) RecoverIndex(ctx context.Context) (RebuildReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !VaultInitialized(m.config.VaultPath) {
		return RebuildReport{}, ErrVaultNotInitialized
	}
	report, err := m.rebuildLocked(ctx)
	if err == nil {
		return report, nil
	}
	if _, statErr := os.Stat(m.config.IndexPath); statErr != nil {
		return report, err
	}
	quarantine := m.config.IndexPath + ".corrupt-" + time.Now().UTC().Format("20060102T150405")
	if renameErr := os.Rename(m.config.IndexPath, quarantine); renameErr != nil {
		return report, fmt.Errorf("%w (quarantine index: %v)", err, renameErr)
	}
	log.Printf("quarantined corrupt knowledge index at %s", quarantine)
	report, retryErr := m.rebuildLocked(ctx)
	if retryErr != nil {
		return report, fmt.Errorf("rebuild after quarantining the index: %w", retryErr)
	}
	return report, nil
}

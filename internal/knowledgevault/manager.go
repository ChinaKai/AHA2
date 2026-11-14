package knowledgevault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Manager owns the runtime feature switch and the single read-only Reader used
// by the Phase 2/3 Web API. The legacy knowledge store is never involved.
//
// Locking contract: every read (WithReader, Status) holds RLock for the whole
// callback, so a Reader can never be closed while a handler is querying its
// index. SetEnabled, Rebuild, Initialize and Close hold the write lock.
type Manager struct {
	mu      sync.RWMutex
	config  Config
	enabled bool
	source  string
	reader  *Reader
}

// The effective switch comes either from the persisted database row or from
// the startup environment/flags. Only an explicit Web toggle writes the DB.
const (
	SourceStartup  = "startup"
	SourceDatabase = "database"
)

var (
	// ErrDisabled is returned by readers while the runtime switch is off. The
	// HTTP layer maps it to 404 so the old knowledge UI keeps working.
	ErrDisabled = errors.New("knowledge vault is disabled")
	// ErrVaultExists protects an initialized vault from a second init.
	ErrVaultExists = errors.New("knowledge vault is already initialized")
	// ErrVaultNotInitialized rejects reads/rebuilds before the manifest exists.
	ErrVaultNotInitialized = errors.New("knowledge vault is not initialized")
)

// NewManager normalizes the configured paths and starts disabled. The caller
// applies the effective startup state with SetEnabled.
func NewManager(config Config) (*Manager, error) {
	normalized, err := config.Normalized()
	if err != nil {
		return nil, err
	}
	return &Manager{
		config: normalized,
		source: SourceStartup,
	}, nil
}

func (m *Manager) Config() Config { return m.config }

func (m *Manager) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

func (m *Manager) Source() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.source
}

// EffectiveStartupSwitch applies the Phase 3 precedence rule: once the owner
// used the Web switch (configured=true), the persisted value wins; until then
// the environment/flag default decides and is deliberately not persisted.
func EffectiveStartupSwitch(configured, storedEnabled, startupDefault bool) (bool, string) {
	if configured {
		return storedEnabled, SourceDatabase
	}
	return startupDefault, SourceStartup
}

// SetSource records where the current switch value came from for status
// display. Persistence is handled by the HTTP layer; the manager stays free of
// store dependencies.
func (m *Manager) SetSource(source string) {
	source = strings.TrimSpace(source)
	if source != SourceDatabase {
		source = SourceStartup
	}
	m.mu.Lock()
	m.source = source
	m.mu.Unlock()
}

// SetEnabled applies the switch immediately. Opening a vault that is merely
// missing (no directory or index) succeeds and reports the existing empty
// state; only an unreadable manifest/index is a hard error. Repeating the same
// value is a no-op.
func (m *Manager) SetEnabled(ctx context.Context, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if enabled == m.enabled && (m.reader != nil) == enabled {
		return nil
	}
	if !enabled {
		reader := m.reader
		m.reader = nil
		m.enabled = false
		if reader != nil {
			return reader.Close()
		}
		return nil
	}
	reader, err := OpenReader(ctx, m.config)
	if err != nil {
		return err
	}
	previous := m.reader
	m.reader = reader
	m.enabled = true
	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

// WithReader runs fn under the read lock. The Reader must never escape fn.
func (m *Manager) WithReader(fn func(*Reader) error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.enabled {
		return ErrDisabled
	}
	if m.reader == nil {
		return errors.New("knowledge vault reader is unavailable")
	}
	return fn(m.reader)
}

// Status is safe in both switch states. When enabled it reports the live
// Reader; when disabled it inspects the files on disk without keeping a
// connection open, so the settings panel can still offer "initialize".
type Status struct {
	Enabled          bool   `json:"enabled"`
	Source           string `json:"source"`
	VaultReady       bool   `json:"vault_ready"`
	VaultInitialized bool   `json:"vault_initialized"`
	IndexReady       bool   `json:"index_ready"`
	VaultName        string `json:"vault_name,omitempty"`
	VaultPath        string `json:"vault_path"`
	IndexPath        string `json:"index_path"`
	EntryCount       int    `json:"entry_count"`
	RebuiltAt        string `json:"rebuilt_at,omitempty"`
	Warning          string `json:"warning,omitempty"`
}

func (m *Manager) Status(ctx context.Context) Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := Status{
		Enabled: m.enabled, Source: m.source,
		VaultPath: m.config.VaultPath, IndexPath: m.config.IndexPath,
		VaultInitialized: VaultInitialized(m.config.VaultPath),
	}
	if m.reader != nil {
		status.VaultReady = m.reader.VaultReady()
		status.IndexReady = m.reader.IndexReady()
		status.VaultName = m.reader.Manifest().VaultName
		if status.IndexReady {
			m.fillIndexStatus(ctx, &status, m.reader.index)
		}
	} else {
		status.VaultReady = vaultDirectoryExists(m.config.VaultPath)
		if status.VaultInitialized {
			if manifest, err := ReadManifest(m.config.VaultPath); err == nil {
				status.VaultName = manifest.VaultName
			}
		}
		if info, err := os.Stat(m.config.IndexPath); err == nil && !info.IsDir() {
			if index, openErr := openReadOnlyIndex(ctx, m.config.IndexPath); openErr == nil {
				// Only claim "index ready" when the projection is actually
				// readable; a truncated or foreign SQLite file must not make
				// the settings panel lie.
				if indexStatus, statusErr := index.Status(ctx); statusErr == nil {
					status.IndexReady = true
					status.RebuiltAt = indexStatus.RebuiltAt
					if count, countErr := index.VisibleEntryCount(ctx); countErr == nil {
						status.EntryCount = count
					}
				}
				_ = index.Close()
			}
		}
	}
	if !status.VaultReady {
		status.Warning = "vault_missing"
	} else if !status.IndexReady {
		status.Warning = "index_missing"
	}
	return status
}

func (m *Manager) fillIndexStatus(ctx context.Context, status *Status, index *Index) {
	if index == nil {
		return
	}
	if count, err := index.VisibleEntryCount(ctx); err == nil {
		status.EntryCount = count
	}
	if indexStatus, err := index.Status(ctx); err == nil {
		status.RebuiltAt = indexStatus.RebuiltAt
	}
}

// Rebuild closes the Reader, rebuilds the index from files, then opens a fresh
// Reader. On failure it tries to restore the previous read view so a bad edit
// does not take the page down.
func (m *Manager) Rebuild(ctx context.Context) (RebuildReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled {
		return RebuildReport{}, ErrDisabled
	}
	if !VaultInitialized(m.config.VaultPath) {
		return RebuildReport{}, ErrVaultNotInitialized
	}
	if m.reader != nil {
		_ = m.reader.Close()
		m.reader = nil
	}
	index, err := OpenIndex(ctx, m.config.IndexPath)
	if err != nil {
		m.reopenLocked(ctx)
		return RebuildReport{}, fmt.Errorf("open knowledge index: %w", err)
	}
	report, rebuildErr := index.Rebuild(ctx, m.config.VaultPath)
	closeErr := index.Close()
	if rebuildErr != nil {
		m.reopenLocked(ctx)
		return report, fmt.Errorf("rebuild knowledge index: %w", rebuildErr)
	}
	if closeErr != nil {
		m.reopenLocked(ctx)
		return report, fmt.Errorf("close knowledge index: %w", closeErr)
	}
	reader, openErr := OpenReader(ctx, m.config)
	if openErr != nil {
		// The files are intact but the new index cannot be read; report the
		// failure instead of silently serving an empty page.
		m.enabled = false
		return report, fmt.Errorf("open knowledge vault after rebuild: %w", openErr)
	}
	m.reader = reader
	return report, nil
}

// SampleInitResult is returned by Initialize. SampleCount is the number of
// files this call actually created, not the fixed seed size.
type SampleInitResult struct {
	VaultPath string        `json:"vault_path"`
	VaultName string        `json:"vault_name"`
	Created   []string      `json:"created"`
	Skipped   []string      `json:"skipped,omitempty"`
	Samples   int           `json:"samples"`
	Rebuild   RebuildReport `json:"rebuild"`
}

// Initialize creates a brand-new vault (manifest absent), optionally seeds the
// fixed samples, rebuilds the index, and enables the runtime switch. Existing
// vaults are rejected before any file is written.
func (m *Manager) Initialize(ctx context.Context, samples bool) (SampleInitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if VaultInitialized(m.config.VaultPath) {
		return SampleInitResult{}, ErrVaultExists
	}
	result := SampleInitResult{VaultPath: m.config.VaultPath}
	// Samples go first: if bootstrap then fails, the vault still has no
	// manifest, so a retry is allowed and the already-written samples are
	// simply reported as skipped instead of blocking the owner out.
	if samples {
		written, writeErr := WriteSamples(m.config, time.Now().UTC())
		if writeErr != nil {
			return result, writeErr
		}
		result.Created = written.Created
		result.Skipped = written.Skipped
		result.Samples = len(written.Created)
	}
	initReport, err := Bootstrap(m.config, InitOptions{Commit: false})
	if err != nil {
		return result, err
	}
	result.VaultName = initReport.VaultName
	index, err := OpenIndex(ctx, m.config.IndexPath)
	if err != nil {
		return result, fmt.Errorf("open knowledge index: %w", err)
	}
	rebuildReport, rebuildErr := index.Rebuild(ctx, m.config.VaultPath)
	closeErr := index.Close()
	if rebuildErr != nil {
		return result, fmt.Errorf("rebuild knowledge index: %w", rebuildErr)
	}
	if closeErr != nil {
		return result, fmt.Errorf("close knowledge index: %w", closeErr)
	}
	result.Rebuild = rebuildReport
	reader, err := OpenReader(ctx, m.config)
	if err != nil {
		return result, fmt.Errorf("open knowledge vault: %w", err)
	}
	if m.reader != nil {
		_ = m.reader.Close()
	}
	m.reader = reader
	m.enabled = true
	return result, nil
}

func (m *Manager) reopenLocked(ctx context.Context) {
	reader, err := OpenReader(context.WithoutCancel(ctx), m.config)
	if err != nil {
		m.reader = nil
		m.enabled = false
		return
	}
	m.reader = reader
}

// Close releases the Reader on process shutdown.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	reader := m.reader
	m.reader = nil
	m.enabled = false
	if reader != nil {
		return reader.Close()
	}
	return nil
}

func vaultDirectoryExists(vaultPath string) bool {
	info, err := os.Stat(vaultPath)
	return err == nil && info.IsDir()
}

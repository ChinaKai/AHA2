package knowledgevault

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Manager owns the runtime feature switch and the single read-only Reader used
// by the Phase 2/3 Web API. The legacy knowledge store is never involved.
//
// The owner-visible model has one switch: on routes reads, writes, skills and
// the Web pages to the vault; off sends all of them back to legacy. The
// runtimeWrite field and the old project allowlist are support-only emergency
// overrides for diagnostics, not user-visible gates.
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
	// runtimeWrite is a support-only emergency write cutoff. It defaults on so
	// the single manager switch is enough; support can still set it false to
	// stop vault writes without touching the owner-visible switch.
	runtimeWrite bool
	// writeBudget is the per-Task/Turn entry write limit; zero uses
	// DefaultWriteBudget. It is a field so tests can shrink the budget.
	writeBudget int
	// writeHook is a test seam called after the file write and before the
	// index rebuild. Production leaves it nil.
	writeHook func() error
	// autoMu guards the single-writer auto-index loop and its health snapshot.
	// It is separate from mu so Status never waits behind a long rebuild.
	autoMu     sync.Mutex
	auto       autoIndexState
	autoCancel context.CancelFunc
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
		config:       normalized,
		source:       SourceStartup,
		runtimeWrite: true,
	}, nil
}

func (m *Manager) Config() Config { return m.config }

// Guide reads the canonical guide directly from disk. It intentionally does
// not require the runtime switch or a rebuilt index: the Agent API must be
// able to explain the vault while the index is being repaired.
func (m *Manager) Guide() (Guide, error) {
	return ReadGuide(m.config.VaultPath)
}

// ensureGuide repairs the upgrade case where an initialized vault predates the
// canonical guide. It never overwrites an existing (including hand-written)
// guide, and a failure is logged instead of blocking knowledge reads.
func (m *Manager) ensureGuide() {
	if !VaultInitialized(m.config.VaultPath) {
		return
	}
	if _, err := EnsureGuide(m.config.VaultPath, time.Now().UTC()); err != nil {
		log.Printf("ensure knowledge guide for %s: %v", m.config.VaultPath, err)
	}
	// The guide is hand-editable and survives upgrades, so a vault created by an
	// older release can still name the retired Agent endpoints. Repair only the
	// endpoint strings; the owner's prose stays untouched.
	if changed, err := RepairGuideEndpoints(m.config.VaultPath); err != nil {
		log.Printf("repair knowledge guide endpoints for %s: %v", m.config.VaultPath, err)
	} else if changed {
		log.Printf("updated retired Agent endpoints in knowledge guide %s", m.config.VaultPath)
	}
}

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

// SetRuntimeWrite applies the support-only emergency write cutoff. It never
// enables or disables the vault itself; the owner-visible manager switch is the
// only routing control.
func (m *Manager) SetRuntimeWrite(enabled bool) {
	m.mu.Lock()
	m.runtimeWrite = enabled
	m.mu.Unlock()
}

func (m *Manager) RuntimeWriteEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runtimeWrite
}

func (m *Manager) setWriteBudgetForTest(budget int) {
	m.mu.Lock()
	m.writeBudget = budget
	m.mu.Unlock()
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
	// Upgrade path: a vault created by an older release has no guide file.
	// Repair it before opening so the Agent guide endpoint works without a
	// manual rebuild.
	m.ensureGuide()
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
// connection open, so the settings panel can still offer migration/import.
// VaultWritable/IndexWritable/IndexStale/ProjectBindings feed the backend
// readiness calculation.
type Status struct {
	Enabled          bool              `json:"enabled"`
	Source           string            `json:"source"`
	VaultReady       bool              `json:"vault_ready"`
	VaultInitialized bool              `json:"vault_initialized"`
	IndexReady       bool              `json:"index_ready"`
	VaultWritable    bool              `json:"vault_writable"`
	IndexWritable    bool              `json:"index_writable"`
	IndexStale       bool              `json:"index_stale,omitempty"`
	IndexError       string            `json:"index_error,omitempty"`
	VaultName        string            `json:"vault_name,omitempty"`
	VaultPath        string            `json:"vault_path"`
	IndexPath        string            `json:"index_path"`
	EntryCount       int               `json:"entry_count"`
	IndexWarnings    int               `json:"index_warnings,omitempty"`
	RebuiltAt        string            `json:"rebuilt_at,omitempty"`
	ProjectBindings  map[string]string `json:"project_bindings,omitempty"`
	AutoIndex        *AutoIndexHealth  `json:"auto_index,omitempty"`
	Warning          string            `json:"warning,omitempty"`
}

func (m *Manager) Status(ctx context.Context) Status {
	m.mu.RLock()
	enabled, source, reader := m.enabled, m.source, m.reader
	m.mu.RUnlock()
	probe := m.Probe(ctx)
	status := Status{
		Enabled: enabled, Source: source,
		VaultPath: m.config.VaultPath, IndexPath: m.config.IndexPath,
		VaultReady: probe.VaultReady, VaultInitialized: probe.VaultInitialized,
		VaultWritable: probe.VaultWritable, IndexReady: probe.IndexReady,
		IndexWritable: probe.IndexWritable, IndexStale: probe.IndexStale,
		IndexError: probe.IndexError, EntryCount: probe.EntryCount,
		IndexWarnings: probe.IndexWarnings, RebuiltAt: probe.RebuiltAt,
		ProjectBindings: probe.Manifest.Projects,
	}
	health := m.AutoIndexHealth()
	status.AutoIndex = &health
	if reader != nil {
		status.VaultName = reader.Manifest().VaultName
	} else if probe.ManifestOK {
		status.VaultName = probe.Manifest.VaultName
	}
	if !status.VaultReady {
		status.Warning = "vault_missing"
	} else if !status.IndexReady {
		status.Warning = "index_missing"
	} else if status.IndexStale {
		status.Warning = "index_stale"
	}
	return status
}

// CheckProjectSpacePrerequisites is the preflight a project creation uses
// before it inserts a row. It refuses a vault that is not initialized or not
// writable instead of leaving a half-created project behind.
func (m *Manager) CheckProjectSpacePrerequisites() error {
	return CheckProjectSpacePrerequisites(m.config)
}

// ProvisionProjectSpace creates the project's manifest mapping, directory and
// root index, then rebuilds the index so the new space is immediately
// searchable. It works while the owner-visible switch is off (migration and
// import need it); callers decide whether the project may be created.
func (m *Manager) ProvisionProjectSpace(ctx context.Context, projectID, projectName string) (ProjectSpaceResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !VaultInitialized(m.config.VaultPath) {
		return ProjectSpaceResult{}, ErrVaultNotInitialized
	}
	result, err := EnsureProjectSpace(m.config, projectID, projectName, time.Now().UTC())
	if err != nil {
		return result, err
	}
	if _, err := m.rebuildLocked(ctx); err != nil {
		return result, err
	}
	return result, nil
}

// RenameProjectSpace records a project rename in the manifest without moving
// its files or changing its slug.
func (m *Manager) RenameProjectSpace(projectID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return RenameProjectSpace(m.config, projectID, name)
}

// Rebuild closes the Reader, rebuilds the index from files, then opens a fresh
// Reader when the manager is on. It also works while the switch is off so a
// migration, import or Git pull can refresh the projection before the owner
// enables the vault. On failure it tries to restore the previous read view so
// a bad edit does not take the page down.
func (m *Manager) Rebuild(ctx context.Context) (RebuildReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !VaultInitialized(m.config.VaultPath) {
		return RebuildReport{}, ErrVaultNotInitialized
	}
	return m.rebuildLocked(ctx)
}

// rebuildLocked rebuilds the projection and reopens the reader. The caller
// holds m.mu for writing.
func (m *Manager) rebuildLocked(ctx context.Context) (RebuildReport, error) {
	if !VaultInitialized(m.config.VaultPath) {
		return RebuildReport{}, ErrVaultNotInitialized
	}
	wasEnabled := m.enabled
	// Backfill before the scan so a repaired guide is indexed by this rebuild.
	m.ensureGuide()
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
	if !wasEnabled {
		// Refreshing the index for a migration/import/Git pull must not
		// silently turn the owner-visible switch on.
		m.reader = nil
		m.enabled = false
		return report, nil
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
	if !m.enabled {
		m.reader = nil
		return
	}
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
	m.stopAutoIndex()
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

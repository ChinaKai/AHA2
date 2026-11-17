package knowledgevault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The auto-sync engine is intentionally small and single-flight. It polls the
// worktree instead of installing a platform watcher so Windows, Linux and WSL
// behave identically, commits only gated text changes, and then runs a
// non-interactive pull --ff-only + push. It never merges, resets, amends or
// force-pushes, and every pause is visible in the settings status.

const (
	AutoSyncPollIntervalDefault   = 3 * time.Second
	AutoSyncQuietPeriodDefault    = 30 * time.Second
	AutoSyncWriteDebounceDefault  = time.Second
	AutoSyncRemoteIntervalDefault = 60 * time.Second
	AutoSyncRetryBaseDefault      = 5 * time.Second
	AutoSyncRetryMaxDefault       = 5 * time.Minute
	AutoSyncMaxFailuresDefault    = 5
	AutoSyncStateFile             = "auto-sync.json"
	AutoSyncPreflightVersion      = 1

	// SyncEventConflict is the pet-facing conflict event. It fires once per
	// (local HEAD, remote HEAD) pair and contains commits plus file names only.
	SyncEventConflict = "knowledge_sync_conflict"
	// SyncEventPaused reports a preflight failure or exhausted retry budget.
	SyncEventPaused = "knowledge_sync_paused"
)

// AutoSyncSettings is the persisted owner switch, normally backed by
// knowledge_vault_settings.auto_sync.
type AutoSyncSettings interface {
	AutoSyncEnabled(ctx context.Context) (bool, error)
	SetAutoSyncEnabled(ctx context.Context, enabled bool) error
}

// SyncEvent is emitted to the pet/audit sink. Detail is short and never
// contains document bodies, remote credentials or environment values.
type SyncEvent struct {
	Kind   string
	Detail string
}

// AutoSyncStatus is the read model rendered by the settings panel.
type AutoSyncStatus struct {
	Enabled        bool            `json:"enabled"`
	Running        bool            `json:"running"`
	Paused         bool            `json:"paused"`
	PauseReason    string          `json:"pause_reason,omitempty"`
	Conflict       bool            `json:"conflict"`
	ConflictDetail string          `json:"conflict_detail,omitempty"`
	LastSyncAt     string          `json:"last_sync_at,omitempty"`
	LastCommitAt   string          `json:"last_commit_at,omitempty"`
	LastCommit     string          `json:"last_commit,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	Warning        string          `json:"warning,omitempty"`
	Pending        int             `json:"pending"`
	Blocked        int             `json:"blocked"`
	BlockedFiles   []BlockedChange `json:"blocked_files,omitempty"`
	Failures       int             `json:"failures,omitempty"`
	NextRetryAt    string          `json:"next_retry_at,omitempty"`
	PreflightDone  bool            `json:"preflight_done"`
}

// SyncerConfig carries test seams and production defaults. Zero values use the
// AutoSync*Default constants.
type SyncerConfig struct {
	PollInterval   time.Duration
	QuietPeriod    time.Duration
	WriteDebounce  time.Duration
	RemoteInterval time.Duration
	RetryBase      time.Duration
	RetryMax       time.Duration
	MaxFailures    int
	Now            func() time.Time
	Emit           func(SyncEvent)
}

type autoSyncState struct {
	enabled                  bool
	running                  bool
	paused                   bool
	pauseReason              string
	conflict                 bool
	conflictKey              string
	conflictDetail           string
	conflictEmittedKey       string
	lastSyncAt               time.Time
	lastCommitAt             time.Time
	lastCommit               string
	lastError                string
	warning                  string
	pending                  int
	blocked                  []BlockedChange
	blockedTotal             int
	failures                 int
	nextAttempt              time.Time
	lastFingerprint          string
	fingerprintSince         time.Time
	stableCount              int
	lastCommittedFingerprint string
	lastBlockedFingerprint   string
	writeRequested           bool
	writeRequestedAt         time.Time
	preflightVersion         int
	lastRemoteAttempt        time.Time
	lastEventKey             string
}

type autoSyncStateFile struct {
	PreflightVersion   int    `json:"preflight_version"`
	LastSyncAt         string `json:"last_sync_at,omitempty"`
	LastCommitAt       string `json:"last_commit_at,omitempty"`
	LastCommit         string `json:"last_commit,omitempty"`
	LastError          string `json:"last_error,omitempty"`
	Warning            string `json:"warning,omitempty"`
	PauseReason        string `json:"pause_reason,omitempty"`
	ConflictKey        string `json:"conflict_key,omitempty"`
	ConflictDetail     string `json:"conflict_detail,omitempty"`
	ConflictEmittedKey string `json:"conflict_emitted_key,omitempty"`
}

// Syncer owns the background loop. Manager owns the vault, Git owns the
// process invocations, and this type owns only the policy/state machine.
type Syncer struct {
	manager  *Manager
	settings AutoSyncSettings
	config   SyncerConfig

	mu      sync.Mutex
	state   autoSyncState
	started bool
	cancel  context.CancelFunc
	wake    chan struct{}
}

// NewSyncer builds the engine and restores the small persisted state file. It
// does not start any goroutine; call Start.
func NewSyncer(manager *Manager, settings AutoSyncSettings, config SyncerConfig) *Syncer {
	if config.PollInterval <= 0 {
		config.PollInterval = AutoSyncPollIntervalDefault
	}
	if config.QuietPeriod <= 0 {
		config.QuietPeriod = AutoSyncQuietPeriodDefault
	}
	if config.WriteDebounce < 0 {
		config.WriteDebounce = 0
	} else if config.WriteDebounce == 0 {
		config.WriteDebounce = AutoSyncWriteDebounceDefault
	}
	if config.RemoteInterval <= 0 {
		config.RemoteInterval = AutoSyncRemoteIntervalDefault
	}
	if config.RetryBase <= 0 {
		config.RetryBase = AutoSyncRetryBaseDefault
	}
	if config.RetryMax <= 0 {
		config.RetryMax = AutoSyncRetryMaxDefault
	}
	if config.MaxFailures <= 0 {
		config.MaxFailures = AutoSyncMaxFailuresDefault
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	syncer := &Syncer{manager: manager, settings: settings, config: config, wake: make(chan struct{}, 1)}
	syncer.loadState()
	return syncer
}

func (s *Syncer) now() time.Time { return s.config.Now() }

// Start reads the persisted owner switch and launches the poll loop. It is
// idempotent. A settings read failure leaves auto-sync off and reports it in
// the status instead of taking the control plane down.
func (s *Syncer) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	enabled := false
	if s.settings != nil {
		value, err := s.settings.AutoSyncEnabled(ctx)
		if err != nil {
			s.mu.Lock()
			s.state.lastError = "读取自动同步设置失败：" + err.Error()
			s.mu.Unlock()
			log.Printf("read knowledge auto-sync setting: %v", err)
		} else {
			enabled = value
		}
	}
	s.mu.Lock()
	s.state.enabled = enabled
	s.mu.Unlock()
	go s.loop(runCtx)
}

// Stop cancels the loop. A git process already running is bounded by the git
// command timeout and is allowed to finish.
func (s *Syncer) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.started = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Syncer) loop(ctx context.Context) {
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runCycle(ctx, false)
		case <-s.wake:
			s.runCycle(ctx, false)
		}
	}
}

// Wake requests an immediate cycle without blocking the caller.
func (s *Syncer) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// NotifyWrite is installed on the Manager and called after a successful AHA
// write. It only marks the request; the loop performs the actual commit.
func (s *Syncer) NotifyWrite() {
	s.mu.Lock()
	s.state.writeRequested = true
	s.state.writeRequestedAt = s.now()
	s.mu.Unlock()
	s.Wake()
}

// SetEnabled persists the owner switch and applies it immediately. Turning the
// switch on clears a previous pause so the owner can retry after fixing the
// cause; conflict de-duplication still remembers the exact pair it reported.
func (s *Syncer) SetEnabled(ctx context.Context, enabled bool) error {
	if s.settings == nil {
		return errors.New("auto-sync settings are unavailable")
	}
	if err := s.settings.SetAutoSyncEnabled(ctx, enabled); err != nil {
		return err
	}
	s.mu.Lock()
	s.state.enabled = enabled
	if enabled {
		s.state.paused = false
		s.state.pauseReason = ""
		s.state.conflict = false
		s.state.failures = 0
		s.state.nextAttempt = time.Time{}
		s.state.lastError = ""
	} else {
		s.state.writeRequested = false
	}
	s.mu.Unlock()
	s.saveState()
	s.Wake()
	return nil
}

// ResetPause clears a pause after a successful manual Git operation. The
// persistent conflict de-duplication key is kept so the same pair never
// spams the pet twice.
func (s *Syncer) ResetPause() {
	s.mu.Lock()
	s.state.paused = false
	s.state.pauseReason = ""
	s.state.conflict = false
	s.state.conflictKey = ""
	s.state.conflictDetail = ""
	s.state.failures = 0
	s.state.nextAttempt = time.Time{}
	s.state.lastError = ""
	s.mu.Unlock()
	s.saveState()
	s.Wake()
}

// RunOnce performs one cycle synchronously. Tests use it with a fake clock;
// production uses the timer loop.
func (s *Syncer) RunOnce(ctx context.Context) {
	s.runCycle(ctx, true)
}

// Status returns a snapshot safe to render while a cycle is running.
func (s *Syncer) Status() AutoSyncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := AutoSyncStatus{
		Enabled: s.state.enabled, Running: s.state.running,
		Paused: s.state.paused, PauseReason: s.state.pauseReason,
		Conflict: s.state.conflict, ConflictDetail: s.state.conflictDetail,
		LastCommit: s.state.lastCommit, LastError: s.state.lastError,
		Warning: s.state.warning,
		Pending: s.state.pending, Blocked: s.state.blockedTotal,
		BlockedFiles:  append([]BlockedChange{}, s.state.blocked...),
		Failures:      s.state.failures,
		PreflightDone: s.state.preflightVersion >= AutoSyncPreflightVersion,
	}
	status.LastSyncAt = formatSyncTime(s.state.lastSyncAt)
	status.LastCommitAt = formatSyncTime(s.state.lastCommitAt)
	status.NextRetryAt = formatSyncTime(s.state.nextAttempt)
	return status
}

func formatSyncTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func (s *Syncer) enabledNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.enabled
}

func (s *Syncer) runCycle(ctx context.Context, force bool) {
	s.mu.Lock()
	if s.state.running {
		s.mu.Unlock()
		return
	}
	if !s.state.enabled {
		s.mu.Unlock()
		return
	}
	if s.state.paused {
		s.mu.Unlock()
		return
	}
	s.state.running = true
	s.mu.Unlock()

	func() {
		defer func() {
			s.mu.Lock()
			s.state.running = false
			s.mu.Unlock()
			s.saveState()
		}()
		s.cycle(ctx, force)
	}()
}

func (s *Syncer) cycle(ctx context.Context, force bool) {
	git := s.manager.Git()
	if !VaultInitialized(git.vaultPath) {
		s.pause("vault_not_initialized", "知识库尚未初始化")
		return
	}
	if !git.isRepository(ctx) {
		s.pause("not_git_repository", "知识库还不是 git 仓库")
		return
	}
	if !git.hasCommit(ctx) {
		s.pause("no_commit", "知识库仓库还没有提交")
		return
	}

	s.mu.Lock()
	preflightNeeded := s.state.preflightVersion < AutoSyncPreflightVersion
	s.mu.Unlock()
	if preflightNeeded {
		if err := s.runPreflight(ctx); err != nil {
			detail := sanitizeSyncDetail(err.Error())
			s.pause("preflight_failed", detail)
			s.emitOnce("preflight:"+detail, SyncEventPaused, "行尾迁移 preflight 失败："+detail)
			return
		}
	}
	// The owner may have switched auto-sync off while preflight ran; stop
	// before the next mutating git command.
	if !s.enabledNow() {
		return
	}

	scan, err := git.ScanAutoCommit(ctx)
	if err != nil {
		s.noteError("scan_failed", err)
		return
	}
	s.applyScan(scan)
	if scan.Unmerged {
		s.pause("unmerged", "知识库工作区存在未解决冲突")
		return
	}
	if len(scan.Blocked) > 0 {
		s.mu.Lock()
		shouldLog := s.state.lastBlockedFingerprint != scan.Fingerprint
		if shouldLog {
			s.state.lastBlockedFingerprint = scan.Fingerprint
		}
		s.mu.Unlock()
		if shouldLog {
			log.Printf("knowledge auto-sync blocked %d file(s): %s", scan.BlockedTotal, blockedLogSummary(scan.Blocked))
		}
	}

	now := s.now()
	s.mu.Lock()
	if scan.Clean {
		s.state.writeRequested = false
		s.state.lastCommittedFingerprint = ""
	}
	if scan.Fingerprint != s.state.lastFingerprint {
		s.state.lastFingerprint = scan.Fingerprint
		s.state.fingerprintSince = now
		s.state.stableCount = 1
	} else {
		s.state.stableCount++
	}
	stableCount := s.state.stableCount
	stableSince := s.state.fingerprintSince
	writeRequested := s.state.writeRequested
	writeAt := s.state.writeRequestedAt
	lastCommitted := s.state.lastCommittedFingerprint
	s.mu.Unlock()

	dirty := !scan.Clean
	if dirty && len(scan.Eligible) == 0 && scan.Fingerprint != lastCommitted {
		// Only blocked/internal changes: remember this exact state so the loop
		// does not call git every tick. A real edit changes the fingerprint.
		s.mu.Lock()
		s.state.lastCommittedFingerprint = scan.Fingerprint
		s.state.writeRequested = false
		s.mu.Unlock()
	}
	ahaReady := writeRequested && !now.Before(writeAt.Add(s.config.WriteDebounce))
	externalReady := stableCount >= 2 && !now.Before(stableSince.Add(s.config.QuietPeriod))
	if dirty && len(scan.Eligible) > 0 && scan.Fingerprint != lastCommitted && (ahaReady || externalReady) {
		if !s.enabledNow() {
			return
		}
		reason := "external"
		if writeRequested {
			reason = "aha"
		}
		result, commitErr := git.AutoCommit(ctx, reason, scan.Fingerprint)
		switch {
		case errors.Is(commitErr, ErrAutoCommitStale):
			// The worktree changed while we waited; next cycle re-evaluates.
			return
		case commitErr != nil:
			s.noteError("commit_failed", commitErr)
			return
		}
		s.mu.Lock()
		s.state.lastCommittedFingerprint = scan.Fingerprint
		s.state.writeRequested = false
		if result.Committed {
			s.state.lastCommit = result.Commit
			s.state.lastCommitAt = now
		}
		s.state.pending = result.Pending
		s.state.blocked = result.Blocked
		s.state.blockedTotal = result.BlockedTotal
		s.mu.Unlock()
		s.saveState()
		if result.Committed {
			log.Printf("knowledge auto-sync committed %d file(s) (%s) at %s", len(result.Files), reason, result.Commit)
		}
		if after, scanErr := git.ScanAutoCommit(ctx); scanErr == nil {
			s.applyScan(after)
			dirty = !after.Clean
		}
	}

	// Refresh the visible pending count even when nothing was committed.
	latest := scan
	if after, scanErr := git.ScanAutoCommit(ctx); scanErr == nil {
		latest = after
		s.applyScan(after)
		dirty = !after.Clean
	}
	if latest.Unmerged {
		s.pause("unmerged", "知识库工作区存在未解决冲突")
		return
	}
	if dirty && len(latest.Eligible) > 0 {
		// A pending auto-committable change (quiet period or the next batch)
		// must land first; pulling over it is not useful yet.
		return
	}
	// Dirty here means only blocked files remain. They must not stop the local
	// commits from reaching the remote: push is unconditional and pull is
	// attempted with git's own overwrite protection.
	if _, err := git.remoteURL(ctx); err != nil {
		return
	}
	// Backoff applies to network work only: local auto-commits keep working
	// while an unreachable remote waits for its next retry.
	s.mu.Lock()
	nextAttempt := s.state.nextAttempt
	s.mu.Unlock()
	if !force && !nextAttempt.IsZero() && now.Before(nextAttempt) {
		return
	}
	if !force && !s.remoteDue(now) {
		return
	}
	if !s.enabledNow() {
		return
	}
	s.remoteSync(ctx, git, now)
}

func (s *Syncer) remoteDue(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.lastRemoteAttempt.IsZero() {
		return true
	}
	return now.Sub(s.state.lastRemoteAttempt) >= s.config.RemoteInterval
}

func (s *Syncer) remoteSync(ctx context.Context, git *Git, now time.Time) {
	s.mu.Lock()
	s.state.lastRemoteAttempt = now
	s.mu.Unlock()

	// One repository lock covers the whole pull+push pair so a manual sync or
	// auto-commit cannot interleave. A push can succeed even when a blocked
	// local file prevents the pull from fast-forwarding; the pull error stays
	// visible in the status for the next retry.
	unlock := git.lockRepo()
	defer unlock()
	pullBlocked := false
	if _, err := git.pullAllowDirty(ctx, false); err != nil {
		switch {
		case errors.Is(err, ErrGitDiverged):
			s.handleConflict(ctx, git)
			return
		case errors.Is(err, ErrGitRemoteBranchMissing), errors.Is(err, ErrGitNoCommit):
			// First push: the remote branch does not exist yet.
		case pullBlockedByWorktree(err):
			// A blocked file (or a local edit git refuses to overwrite) keeps
			// the pull from fast-forwarding. That is not a remote failure: keep
			// the local commits pushable and retry the pull next round.
			pullBlocked = true
			s.mu.Lock()
			s.state.lastError = sanitizeSyncDetail("拉取被本地文件阻止：" + err.Error())
			s.mu.Unlock()
		default:
			s.backoff("pull_failed", err)
			return
		}
	}
	if _, err := git.push(ctx, false); err != nil {
		if errors.Is(err, ErrGitPushRejected) {
			// A push can be rejected even though the pull just succeeded: the
			// remote may advance between the two commands (another writer, a
			// pre-push hook). Always fetch the latest ref before deciding, and
			// only report a divergence when both sides have commits.
			branch := git.configuredBranch(ctx)
			if fetchErr := git.fetchOrigin(ctx, branch); fetchErr != nil {
				s.backoff("push_rejected", fetchErr)
				return
			}
			ahead, behind, countsOK := git.aheadBehind(ctx, branch)
			if !countsOK {
				s.backoff("push_rejected", err)
				return
			}
			switch {
			case ahead > 0 && behind > 0:
				s.handleConflict(ctx, git)
			case ahead == 0 && behind > 0:
				// Local is an ancestor of the remote: nothing to send and no
				// divergence. Retry the pull on the next tick.
				message := "远端已前进；下次自动拉取"
				if pullBlocked {
					message = "拉取被本地文件阻止；" + message
				}
				s.mu.Lock()
				s.state.lastError = message
				s.state.lastRemoteAttempt = time.Time{}
				s.mu.Unlock()
			default:
				// A hook or server policy rejected an otherwise equal/forward
				// push. Back off and report it instead of inventing a conflict.
				s.backoff("push_rejected", err)
			}
			return
		}
		s.backoff("push_failed", err)
		return
	}
	s.mu.Lock()
	s.state.lastSyncAt = s.now()
	s.state.failures = 0
	s.state.nextAttempt = time.Time{}
	if !pullBlocked {
		s.state.lastError = ""
	}
	s.mu.Unlock()
	s.saveState()
	if err := git.recordSync(); err != nil {
		log.Printf("record knowledge sync timestamp: %v", err)
	}
}

// pullBlockedByWorktree recognizes git's safe refusal to overwrite local or
// untracked files. The caller still pushes and retries the pull later instead
// of entering a permanent backoff/pause.
func pullBlockedByWorktree(err error) bool {
	var commandErr *GitCommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	detail := strings.ToLower(commandErr.Detail)
	for _, marker := range []string{
		"would be overwritten",
		"untracked working tree files",
		"local changes to the following files",
		"please commit your changes or stash them",
	} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

func (s *Syncer) runPreflight(ctx context.Context) error {
	git := s.manager.Git()
	// 1. Commit every existing text change before touching the line-ending
	//    policy. Blocked files abort the migration instead of being discarded.
	for attempt := 0; attempt < 32; attempt++ {
		scan, err := git.ScanAutoCommit(ctx)
		if err != nil {
			return err
		}
		if scan.Clean {
			break
		}
		if scan.Unmerged {
			return ErrGitUnmerged
		}
		if len(scan.Eligible) == 0 {
			return fmt.Errorf("有 %d 个改动无法自动提交（二进制/密钥/超大文件）；preflight 已暂停并保留现场", scan.Pending)
		}
		result, err := git.AutoCommit(ctx, "preflight", scan.Fingerprint)
		if err != nil {
			return err
		}
		if !result.Committed && result.Pending >= scan.Pending {
			return fmt.Errorf("preflight 自动提交没有进展；保留现场并暂停")
		}
	}
	if clean, changed, err := git.isClean(ctx); err != nil {
		return err
	} else if !clean {
		return fmt.Errorf("%w：仍有 %d 个改动", ErrGitDirty, changed)
	}
	// 2. Policy file and repo-local autocrlf.
	if _, err := git.EnsureTextPolicy(ctx); err != nil {
		return err
	}
	// 3. One-time renormalization. It is idempotent: a second run stages
	//    nothing and creates no commit.
	renormalized, err := git.Renormalize(ctx)
	if err != nil {
		return err
	}
	if renormalized.Warning != "" {
		log.Printf("knowledge auto-sync preflight warning: %s", renormalized.Warning)
	}
	s.mu.Lock()
	// A warning is durable: a normal sync success must not hide a failed
	// backup-tag creation. A later clean preflight clears it.
	s.state.warning = sanitizeSyncDetail(renormalized.Warning)
	s.state.preflightVersion = AutoSyncPreflightVersion
	s.mu.Unlock()
	s.saveState()
	log.Printf("knowledge auto-sync preflight complete for %s", git.vaultPath)
	return nil
}

func (s *Syncer) handleConflict(ctx context.Context, git *Git) {
	branch := git.configuredBranch(ctx)
	local := strings.TrimSpace(git.commandOrEmpty(ctx, "rev-parse", "--verify", "HEAD"))
	remote := strings.TrimSpace(git.commandOrEmpty(ctx, "rev-parse", "--verify", "origin/"+branch))
	key := local + "|" + remote
	detail := conflictDetail(ctx, git, local, remote, branch)

	s.mu.Lock()
	s.state.paused = true
	s.state.pauseReason = "conflict"
	s.state.conflict = true
	s.state.conflictKey = key
	s.state.conflictDetail = detail
	emit := s.state.conflictEmittedKey != key
	if emit {
		s.state.conflictEmittedKey = key
	}
	s.mu.Unlock()
	s.saveState()
	if emit {
		s.emit(SyncEventConflict, detail)
		log.Printf("knowledge sync conflict: %s", detail)
	}
}

func conflictDetail(ctx context.Context, git *Git, local, remote, branch string) string {
	short := func(value string) string {
		value = strings.TrimSpace(value)
		if len(value) > 12 {
			return value[:12]
		}
		if value == "" {
			return "unknown"
		}
		return value
	}
	files := []string{}
	if local != "" && remote != "" {
		if output, err := git.commandRaw(ctx, "diff", "--name-only", local+"..."+remote); err == nil {
			for _, line := range strings.Split(output, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				files = append(files, sanitizeCommitPath(line))
				if len(files) >= 20 {
					break
				}
			}
		}
	}
	detail := fmt.Sprintf("本地 %s，远端 %s 已分叉；未合并、未 reset。", short(local), short(remote))
	if len(files) > 0 {
		detail += "差异文件：" + strings.Join(files, "、")
	}
	if len(detail) > 900 {
		runes := []rune(detail)
		if len(runes) > 900 {
			detail = string(runes[:900]) + "…"
		}
	}
	return detail
}

// blockedLogSummary renders at most ten blocked paths for one log line. Paths
// go through the same control-character sanitizer as commit messages so a
// newline in a file name cannot forge extra log records.
func blockedLogSummary(blocked []BlockedChange) string {
	names := make([]string, 0, len(blocked))
	for index, item := range blocked {
		if index >= 10 {
			names = append(names, "…")
			break
		}
		names = append(names, sanitizeCommitPath(item.Path)+"("+item.Reason+")")
	}
	return strings.Join(names, ", ")
}

func (s *Syncer) applyScan(scan AutoCommitScan) {
	s.mu.Lock()
	s.state.pending = scan.Pending
	s.state.blocked = append([]BlockedChange{}, scan.Blocked...)
	s.state.blockedTotal = scan.BlockedTotal
	s.mu.Unlock()
}

func (s *Syncer) pause(reason, detail string) {
	s.mu.Lock()
	changed := !s.state.paused || s.state.pauseReason != reason
	s.state.paused = true
	s.state.pauseReason = reason
	s.state.lastError = sanitizeSyncDetail(detail)
	s.mu.Unlock()
	if changed {
		log.Printf("knowledge auto-sync paused (%s): %s", reason, sanitizeSyncDetail(detail))
	}
	s.saveState()
}

func (s *Syncer) noteError(reason string, err error) {
	s.mu.Lock()
	s.state.lastError = sanitizeSyncDetail(reason + ": " + err.Error())
	s.mu.Unlock()
	s.saveState()
}

// backoff implements exponential retry and stops after MaxFailures instead of
// looping forever. The pause is reported in the settings status and once as a
// pet event.
func (s *Syncer) backoff(reason string, err error) {
	s.mu.Lock()
	s.state.failures++
	delay := s.config.RetryBase
	for index := 1; index < s.state.failures; index++ {
		delay *= 2
		if delay >= s.config.RetryMax {
			delay = s.config.RetryMax
			break
		}
	}
	s.state.nextAttempt = s.now().Add(delay)
	s.state.lastError = sanitizeSyncDetail(reason + ": " + err.Error())
	paused := s.state.failures >= s.config.MaxFailures
	if paused {
		s.state.paused = true
		s.state.pauseReason = reason
	}
	failures := s.state.failures
	s.mu.Unlock()
	s.saveState()
	if paused {
		s.emitOnce(fmt.Sprintf("paused:%s:%d", reason, failures), SyncEventPaused,
			fmt.Sprintf("知识库自动同步连续失败 %d 次，已暂停：%s", failures, sanitizeSyncDetail(err.Error())))
	}
}

func (s *Syncer) emitOnce(key, kind, detail string) {
	s.mu.Lock()
	if s.state.lastEventKey == key {
		s.mu.Unlock()
		return
	}
	s.state.lastEventKey = key
	s.mu.Unlock()
	s.emit(kind, detail)
}

func (s *Syncer) emit(kind, detail string) {
	if s.config.Emit == nil {
		return
	}
	defer func() {
		// An event sink must never take down the sync loop.
		_ = recover()
	}()
	s.config.Emit(SyncEvent{Kind: kind, Detail: sanitizeSyncDetail(detail)})
}

func sanitizeSyncDetail(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	runes := []rune(value)
	if len(runes) > 900 {
		value = string(runes[:900]) + "…"
	}
	return value
}

func (s *Syncer) statePath() string {
	return filepath.Join(s.manager.config.VaultPath, stateDirectory, AutoSyncStateFile)
}

func (s *Syncer) loadState() {
	if s.manager == nil || !VaultInitialized(s.manager.config.VaultPath) {
		return
	}
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		return
	}
	var file autoSyncStateFile
	if json.Unmarshal(data, &file) != nil {
		return
	}
	s.mu.Lock()
	s.state.preflightVersion = file.PreflightVersion
	s.state.lastSyncAt = parseSyncTime(file.LastSyncAt)
	s.state.lastCommitAt = parseSyncTime(file.LastCommitAt)
	s.state.lastCommit = file.LastCommit
	s.state.lastError = file.LastError
	s.state.warning = file.Warning
	s.state.pauseReason = file.PauseReason
	s.state.paused = strings.TrimSpace(file.PauseReason) != ""
	s.state.conflictKey = file.ConflictKey
	s.state.conflictDetail = file.ConflictDetail
	s.state.conflictEmittedKey = file.ConflictEmittedKey
	s.state.conflict = strings.TrimSpace(file.ConflictDetail) != ""
	// A divergence needs the owner, so it survives a restart. A transient
	// push/pull failure is retried with a fresh budget after the process comes
	// back; an unfinished preflight runs again on the first cycle.
	s.state.paused = file.PauseReason == "conflict" || file.PauseReason == "preflight_failed"
	s.mu.Unlock()
}

func (s *Syncer) saveState() {
	if s.manager == nil || !VaultInitialized(s.manager.config.VaultPath) {
		return
	}
	s.mu.Lock()
	file := autoSyncStateFile{
		PreflightVersion:   s.state.preflightVersion,
		LastSyncAt:         formatSyncTime(s.state.lastSyncAt),
		LastCommitAt:       formatSyncTime(s.state.lastCommitAt),
		LastCommit:         s.state.lastCommit,
		LastError:          s.state.lastError,
		Warning:            s.state.warning,
		PauseReason:        s.state.pauseReason,
		ConflictKey:        s.state.conflictKey,
		ConflictDetail:     s.state.conflictDetail,
		ConflictEmittedKey: s.state.conflictEmittedKey,
	}
	s.mu.Unlock()
	payload, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	payload = append(payload, '\n')
	if err := writeVaultFileIfChanged(s.manager.config.VaultPath, s.statePath(), payload, 0o644); err != nil {
		log.Printf("write knowledge auto-sync state: %v", err)
	}
}

func parseSyncTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// commandOrEmpty is a best-effort read used for conflict diagnostics. A
// failure yields an empty string; it never changes the conflict decision.
func (g *Git) commandOrEmpty(ctx context.Context, args ...string) string {
	output, err := g.command(ctx, args[0], args[1:]...)
	if err != nil {
		return ""
	}
	return output
}

// aheadBehind reports the local/remote commit counts against origin/<branch>.
func (g *Git) aheadBehind(ctx context.Context, branch string) (ahead, behind int, ok bool) {
	output, err := g.command(ctx, "rev-list", "--left-right", "--count", "origin/"+branch+"...HEAD")
	if err != nil {
		return 0, 0, false
	}
	if _, scanErr := fmt.Sscan(output, &behind, &ahead); scanErr != nil {
		return 0, 0, false
	}
	return ahead, behind, true
}

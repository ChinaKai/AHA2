package browser

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	DefaultHostID   = "local"
	DefaultGrantTTL = 30 * time.Minute
	MinGrantTTL     = time.Minute
	MaxGrantTTL     = 24 * time.Hour
)

// Service owns the browser profile/session/grant lifecycle. The host remains
// authoritative for whether a real browser is running; the database state is a
// cache plus the unique-session backstop.
type Service struct {
	store   *store.Store
	host    Host
	hostID  string
	now     func() time.Time
	mu      sync.Mutex
	grantMu sync.Mutex
}

func NewService(st *store.Store, host Host, hostID string) *Service {
	hostID = strings.TrimSpace(hostID)
	if hostID == "" {
		hostID = DefaultHostID
	}
	return &Service{store: st, host: host, hostID: hostID, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) HostID() string { return s.hostID }

func (s *Service) Enabled() bool { return s.host != nil }

func (s *Service) SetNowForTest(now func() time.Time) { s.now = now }

type HostView struct {
	Available bool   `json:"available"`
	HostID    string `json:"host_id"`
	Version   string `json:"version,omitempty"`
	Platform  string `json:"platform,omitempty"`
	Edge      bool   `json:"edge_available"`
	Error     string `json:"error,omitempty"`
	Code      string `json:"code,omitempty"`
}

func (s *Service) hostView(ctx context.Context) HostView {
	view := HostView{HostID: s.hostID}
	if s.host == nil {
		view.Error = "浏览器宿主未配置（AHA2_BROWSER_HOST_URL）"
		view.Code = "browser_host_unavailable"
		return view
	}
	healthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	health, err := s.host.Health(healthCtx)
	if err != nil {
		view.Error = PublicMessage(err)
		view.Code = Code(err)
		if view.Code == "" {
			view.Code = "browser_host_unavailable"
		}
		return view
	}
	view.Available = true
	view.Version = health.Version
	view.Platform = health.Platform
	view.Edge = health.EdgeAvailable
	return view
}

// HostStatus is the UI-facing host view. It never fails just because the host
// is down: the caller needs the explicit reason to show a fail-closed banner.
func (s *Service) HostStatus(ctx context.Context) HostView {
	return s.hostView(ctx)
}

type ProfileView struct {
	domain.BrowserProfile
	Session       *domain.BrowserSession `json:"session,omitempty"`
	HostAvailable bool                   `json:"host_available"`
}

type SessionView struct {
	domain.BrowserSession
	HostReachable bool `json:"host_reachable"`
}

type GrantView struct {
	domain.BrowserTaskGrant
	Status  string                 `json:"status"`
	Session *domain.BrowserSession `json:"session,omitempty"`
	Tabs    []domain.BrowserTab    `json:"tabs,omitempty"`
}

type TaskBrowserView struct {
	TaskID        string                 `json:"task_id"`
	Profiles      []ProfileView          `json:"profiles"`
	Grants        []GrantView            `json:"grants"`
	Tabs          []domain.BrowserTab    `json:"tabs"`
	Session       *domain.BrowserSession `json:"session,omitempty"`
	HostAvailable bool                   `json:"host_available"`
	Host          HostView               `json:"host"`
}

func (s *Service) profileView(ctx context.Context, profile domain.BrowserProfile, includeSession bool) ProfileView {
	view := ProfileView{BrowserProfile: profile, HostAvailable: s.host != nil}
	if includeSession {
		if session, err := s.store.ActiveBrowserSession(ctx, profile.ID); err == nil {
			view.Session = &session
		}
	}
	return view
}

func (s *Service) Profiles(ctx context.Context) ([]ProfileView, error) {
	profiles, err := s.store.BrowserProfiles(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]ProfileView, 0, len(profiles))
	for _, profile := range profiles {
		// The owner may have closed the visible window; reconcile profiles that
		// still claim a session so the resource sheet does not show a stale
		// "ready". A closed window also revokes grants through RefreshSession.
		if s.host != nil {
			if _, err := s.store.ActiveBrowserSession(ctx, profile.ID); err == nil {
				_, _ = s.RefreshSession(ctx, profile.ID)
				if refreshed, err := s.store.BrowserProfile(ctx, profile.ID); err == nil {
					profile = refreshed
				}
			}
		}
		result = append(result, s.profileView(ctx, profile, true))
	}
	return result, nil
}

func (s *Service) Profile(ctx context.Context, id string, refresh bool) (ProfileView, error) {
	profile, err := s.store.BrowserProfile(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileView{}, notFound("browser_profile_not_found", "浏览器 Profile 不存在")
	}
	if err != nil {
		return ProfileView{}, err
	}
	if refresh {
		_, _ = s.RefreshSession(ctx, profile.ID)
	}
	return s.profileView(ctx, profile, true), nil
}

func normalizeProfileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", badRequest("browser_profile_invalid", "请填写 Profile 名称")
	}
	if len([]rune(name)) > 120 {
		return "", badRequest("browser_profile_invalid", "Profile 名称不能超过 120 个字符")
	}
	return name, nil
}

func (s *Service) CreateProfile(ctx context.Context, actor, name, browserKind string) (ProfileView, error) {
	name, err := normalizeProfileName(name)
	if err != nil {
		return ProfileView{}, err
	}
	browserKind = strings.TrimSpace(browserKind)
	if browserKind == "" {
		browserKind = domain.BrowserKindEdge
	}
	if browserKind != domain.BrowserKindEdge {
		return ProfileView{}, badRequest("browser_profile_invalid",
			"POC 只支持 Windows Edge（browser_kind=edge），不会静默换用其他浏览器")
	}
	now := s.now()
	profile := domain.BrowserProfile{
		ID: domain.NewID("browser_profile"), Name: name, BrowserKind: browserKind,
		HostID: s.hostID, State: domain.BrowserProfileStateIdle, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.UpsertBrowserProfile(ctx, profile); err != nil {
		return ProfileView{}, err
	}
	s.audit(ctx, actor, "browser_profile.create", "browser_profile", profile.ID, map[string]any{
		"browser_kind": profile.BrowserKind, "host_id": profile.HostID,
	})
	return s.profileView(ctx, profile, false), nil
}

func (s *Service) RenameProfile(ctx context.Context, actor, id, name string) (ProfileView, error) {
	name, err := normalizeProfileName(name)
	if err != nil {
		return ProfileView{}, err
	}
	profile, err := s.store.BrowserProfile(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileView{}, notFound("browser_profile_not_found", "浏览器 Profile 不存在")
	}
	if err != nil {
		return ProfileView{}, err
	}
	profile.Name = name
	profile.UpdatedAt = s.now()
	if err := s.store.UpsertBrowserProfile(ctx, profile); err != nil {
		return ProfileView{}, err
	}
	s.audit(ctx, actor, "browser_profile.rename", "browser_profile", profile.ID, nil)
	return s.profileView(ctx, profile, true), nil
}

// DeleteProfile deletes the logical profile only. The host-owned user-data
// directory is deliberately retained: AHA never touches real browser data, and
// a failed delete must not destroy an owner's login state.
func (s *Service) DeleteProfile(ctx context.Context, actor, id string) error {
	profile, err := s.store.BrowserProfile(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return notFound("browser_profile_not_found", "浏览器 Profile 不存在")
	}
	if err != nil {
		return err
	}
	if session, err := s.store.ActiveBrowserSession(ctx, profile.ID); err == nil && session.Active() {
		return conflict("browser_profile_in_use", "该 Profile 还有活跃会话，请先关闭会话再删除")
	}
	grants, err := s.store.BrowserTaskGrantsForProfileActive(ctx, profile.ID, s.now())
	if err != nil {
		return err
	}
	if len(grants) > 0 {
		return conflict("browser_profile_in_use", "该 Profile 仍有有效 Task 授权，请先撤销授权或关闭会话")
	}
	if err := s.store.DeleteBrowserProfile(ctx, profile.ID); err != nil {
		return err
	}
	s.audit(ctx, actor, "browser_profile.delete", "browser_profile", profile.ID, map[string]any{
		"host_id": profile.HostID, "browser_kind": profile.BrowserKind,
	})
	return nil
}

func (s *Service) StartSession(ctx context.Context, actor, profileID string) (SessionView, bool, error) {
	session, reused, err := s.ensureSession(ctx, actor, strings.TrimSpace(profileID))
	if err != nil {
		return SessionView{}, false, err
	}
	view, err := s.sessionView(ctx, session)
	return view, reused, err
}

func (s *Service) ensureSession(ctx context.Context, actor, profileID string) (domain.BrowserSession, bool, error) {
	if s.host == nil {
		return domain.BrowserSession{}, false, newError(http.StatusServiceUnavailable,
			"browser_host_unavailable", "浏览器宿主未配置，请设置 AHA2_BROWSER_HOST_URL")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, err := s.store.BrowserProfile(ctx, profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BrowserSession{}, false, notFound("browser_profile_not_found", "浏览器 Profile 不存在")
	}
	if err != nil {
		return domain.BrowserSession{}, false, err
	}
	if profile.HostID != s.hostID {
		return domain.BrowserSession{}, false, conflict("browser_profile_host_mismatch",
			"该 Profile 绑定在浏览器宿主 %s 上，当前 AHA 只连接 %s", profile.HostID, s.hostID)
	}
	if profile.BrowserKind != domain.BrowserKindEdge {
		return domain.BrowserSession{}, false, badRequest("browser_profile_unsupported",
			"当前 POC 只支持 Edge Profile，不会静默换用其他浏览器")
	}

	active, activeErr := s.store.ActiveBrowserSession(ctx, profile.ID)
	if activeErr != nil && !errors.Is(activeErr, sql.ErrNoRows) {
		return domain.BrowserSession{}, false, activeErr
	}
	if activeErr == nil && active.Active() {
		hostSession, hostErr := s.host.Session(ctx, profile.ID)
		switch {
		case hostErr == nil:
			if hostSession.State == domain.BrowserSessionStateClosed {
				s.closeSessionLocked(ctx, actor, active, "宿主报告会话已关闭")
				break
			}
			s.adoptHostSession(ctx, &active, hostSession)
			if err := s.store.UpdateBrowserSession(ctx, active); err != nil {
				return domain.BrowserSession{}, false, err
			}
			s.syncProfileState(ctx, profile.ID, active.State, active.LastUsedAt)
			return active, true, nil
		case isHostUnavailable(hostErr):
			return domain.BrowserSession{}, false, sanitizeHostError(hostErr)
		case Code(hostErr) == "browser_session_not_found":
			s.closeSessionLocked(ctx, actor, active, "宿主已不再持有该会话")
		default:
			return domain.BrowserSession{}, false, sanitizeHostError(hostErr)
		}
	}

	now := s.now()
	session := domain.BrowserSession{
		ID: domain.NewID("browser_session"), ProfileID: profile.ID, HostID: profile.HostID,
		State: domain.BrowserSessionStateStarting, StartedAt: now, LastUsedAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateBrowserSession(ctx, session); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			// Another process won the database race; reuse its session rather
			// than starting a second browser on the same profile directory.
			existing, lookupErr := s.store.ActiveBrowserSession(ctx, profile.ID)
			if lookupErr != nil {
				return domain.BrowserSession{}, false, err
			}
			return existing, true, nil
		}
		return domain.BrowserSession{}, false, err
	}
	if err := s.store.UpdateBrowserProfileState(ctx, profile.ID, domain.BrowserProfileStateStarting, time.Time{}); err != nil {
		return domain.BrowserSession{}, false, err
	}

	hostSession, err := s.host.Open(ctx, profile.ID, profile.BrowserKind)
	if err != nil {
		if Code(err) == "browser_profile_locked" {
			// The host may already own a session from an earlier AHA run.
			if existing, hostErr := s.host.Session(ctx, profile.ID); hostErr == nil {
				s.adoptHostSession(ctx, &session, existing)
				session.HostSessionID = existing.ID
				if updateErr := s.store.UpdateBrowserSession(ctx, session); updateErr == nil {
					s.syncProfileState(ctx, profile.ID, session.State, session.LastUsedAt)
					return session, true, nil
				}
			}
		}
		session.State = domain.BrowserSessionStateError
		session.Error = PublicMessage(err)
		_ = s.store.UpdateBrowserSession(ctx, session)
		_ = s.store.UpdateBrowserProfileState(ctx, profile.ID, domain.BrowserProfileStateError, time.Time{})
		return domain.BrowserSession{}, false, sanitizeHostError(err)
	}
	if hostSession.State == "" {
		hostSession.State = domain.BrowserSessionStateReady
	}
	if !validSessionState(hostSession.State) || hostSession.State == domain.BrowserSessionStateClosed {
		hostSession.State = domain.BrowserSessionStateError
		hostSession.Error = "宿主返回了无效的会话状态"
	}
	session.HostSessionID = hostSession.ID
	session.State = hostSession.State
	session.Error = hostSessionErrorText(hostSession)
	session.LastUsedAt = s.now()
	if err := s.store.UpdateBrowserSession(ctx, session); err != nil {
		return domain.BrowserSession{}, false, err
	}
	s.syncProfileState(ctx, profile.ID, session.State, session.LastUsedAt)
	s.audit(ctx, actor, "browser_session.start", "browser_session", session.ID, map[string]any{
		"profile_id": profile.ID, "host_id": profile.HostID, "browser_kind": profile.BrowserKind,
		"reused": hostSession.Reused,
	})
	return session, hostSession.Reused, nil
}

func validSessionState(state string) bool {
	switch state {
	case domain.BrowserSessionStateStarting, domain.BrowserSessionStateReady,
		domain.BrowserSessionStateAuthRequired, domain.BrowserSessionStateBusy,
		domain.BrowserSessionStateClosed, domain.BrowserSessionStateError:
		return true
	}
	return false
}

func (s *Service) adoptHostSession(ctx context.Context, session *domain.BrowserSession, hostSession HostSession) {
	if hostSession.State == "" {
		session.State = domain.BrowserSessionStateReady
	} else if validSessionState(hostSession.State) {
		session.State = hostSession.State
	} else {
		session.State = domain.BrowserSessionStateError
	}
	session.HostSessionID = hostSession.ID
	session.Error = hostSessionErrorText(hostSession)
	session.LastUsedAt = s.now()
}

// hostSessionErrorText never persists the host payload: Playwright and host
// diagnostics can contain the real user-data directory. The owner only needs a
// stable state.
func hostSessionErrorText(hostSession HostSession) string {
	if strings.TrimSpace(hostSession.Error) == "" {
		return ""
	}
	return "浏览器会话不可用，请查看宿主日志"
}

func (s *Service) syncProfileState(ctx context.Context, profileID, sessionState string, lastUsed time.Time) {
	state := sessionState
	switch sessionState {
	case domain.BrowserSessionStateClosed:
		state = domain.BrowserProfileStateIdle
	case domain.BrowserSessionStateStarting, domain.BrowserSessionStateReady,
		domain.BrowserSessionStateAuthRequired, domain.BrowserSessionStateBusy,
		domain.BrowserSessionStateError:
	default:
		state = domain.BrowserProfileStateIdle
	}
	_ = s.store.UpdateBrowserProfileState(ctx, profileID, state, lastUsed)
}

func (s *Service) sessionView(ctx context.Context, session domain.BrowserSession) (SessionView, error) {
	view := SessionView{BrowserSession: session, HostReachable: s.host != nil}
	return view, nil
}

// RefreshSession reconciles the stored session with the host. A closed window
// marks the session closed and revokes its grants; an unreachable host is
// reported without inventing a new state.
func (s *Service) RefreshSession(ctx context.Context, profileID string) (*SessionView, error) {
	session, err := s.store.ActiveBrowserSession(ctx, profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.host == nil {
		view := SessionView{BrowserSession: session, HostReachable: false}
		return &view, nil
	}
	hostSession, hostErr := s.host.Session(ctx, profileID)
	if hostErr != nil {
		if isHostUnavailable(hostErr) {
			view := SessionView{BrowserSession: session, HostReachable: false}
			return &view, nil
		}
		if Code(hostErr) == "browser_session_not_found" {
			s.mu.Lock()
			s.closeSessionLocked(ctx, "system", session, "宿主已不再持有该会话")
			s.mu.Unlock()
			return nil, nil
		}
		return nil, sanitizeHostError(hostErr)
	}
	if hostSession.State == domain.BrowserSessionStateClosed {
		s.mu.Lock()
		s.closeSessionLocked(ctx, "system", session, "宿主报告会话已关闭")
		s.mu.Unlock()
		return nil, nil
	}
	s.mu.Lock()
	s.adoptHostSession(ctx, &session, hostSession)
	err = s.store.UpdateBrowserSession(ctx, session)
	if err == nil {
		s.syncProfileState(ctx, profileID, session.State, session.LastUsedAt)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	view := SessionView{BrowserSession: session, HostReachable: true}
	return &view, nil
}

func (s *Service) SessionStatus(ctx context.Context, profileID string) (*SessionView, error) {
	return s.RefreshSession(ctx, strings.TrimSpace(profileID))
}

func (s *Service) CloseSession(ctx context.Context, actor, sessionID string) (domain.BrowserSession, error) {
	session, err := s.store.BrowserSession(ctx, strings.TrimSpace(sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BrowserSession{}, notFound("browser_session_not_found", "浏览器会话不存在")
	}
	if err != nil {
		return domain.BrowserSession{}, err
	}
	if session.State == domain.BrowserSessionStateClosed {
		return session, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.host != nil {
		if err := s.host.Close(ctx, session.ProfileID); err != nil {
			// Closing is the owner's stop control. An unreachable host cannot
			// keep serving the session; any other failure leaves the record
			// active so the owner can retry.
			if !isHostUnavailable(err) && Code(err) != "browser_session_not_found" {
				return domain.BrowserSession{}, sanitizeHostError(err)
			}
		}
	}
	s.closeSessionLocked(ctx, actor, session, "owner_closed")
	closed, err := s.store.BrowserSession(ctx, session.ID)
	if err != nil {
		return domain.BrowserSession{}, err
	}
	return closed, nil
}

func (s *Service) CloseProfileSession(ctx context.Context, actor, profileID string) (*domain.BrowserSession, error) {
	session, err := s.store.ActiveBrowserSession(ctx, strings.TrimSpace(profileID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	closed, err := s.CloseSession(ctx, actor, session.ID)
	if err != nil {
		return nil, err
	}
	return &closed, nil
}

// closeSessionLocked marks the session closed and revokes every grant attached
// to it. Callers hold s.mu.
func (s *Service) closeSessionLocked(ctx context.Context, actor string, session domain.BrowserSession, reason string) {
	now := s.now()
	session.State = domain.BrowserSessionStateClosed
	session.Error = ""
	session.ClosedAt = now
	session.LastUsedAt = now
	_ = s.store.UpdateBrowserSession(ctx, session)
	_ = s.store.UpdateBrowserProfileState(ctx, session.ProfileID, domain.BrowserProfileStateIdle, now)
	s.revokeGrantsForSessionLocked(ctx, actor, session.ID, reason)
	s.audit(ctx, actor, "browser_session.close", "browser_session", session.ID, map[string]any{
		"profile_id": session.ProfileID, "reason": reason,
	})
}

func (s *Service) revokeGrantsForSessionLocked(ctx context.Context, actor, sessionID, reason string) {
	// Serialize with grant creation. The session-close path holds s.mu and then
	// takes grantMu; CreateGrant releases s.mu before taking grantMu, so the two
	// lock orders cannot deadlock.
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	grants, err := s.store.BrowserGrantsForSession(ctx, sessionID)
	if err != nil {
		return
	}
	now := s.now()
	for _, grant := range grants {
		if err := s.store.RevokeBrowserTaskGrant(ctx, grant.ID, now); err != nil {
			continue
		}
		s.audit(ctx, actor, "browser_grant.revoke", "browser_task_grant", grant.ID, map[string]any{
			"task_id": grant.TaskID, "profile_id": grant.ProfileID, "session_id": grant.SessionID,
			"reason": reason,
		})
	}
	if tabs, err := s.store.BrowserTabsForSession(ctx, sessionID); err == nil {
		for _, tab := range tabs {
			_ = s.store.DeleteBrowserTab(ctx, tab.ID)
		}
	}
}

func isUniqueConstraintError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func normalizeCapabilities(values []string) ([]string, error) {
	if len(values) == 0 {
		return append([]string(nil), domain.BrowserCapabilities...), nil
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		valid := false
		for _, allowed := range domain.BrowserCapabilities {
			if value == allowed {
				valid = true
				break
			}
		}
		if !valid {
			return nil, badRequest("browser_grant_invalid", "不支持的浏览器能力：%s", value)
		}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, badRequest("browser_grant_invalid", "至少需要一项浏览器能力")
	}
	sort.Strings(result)
	return result, nil
}

func (s *Service) CreateGrant(ctx context.Context, actor, taskID, profileID string, capabilities []string, ttl time.Duration) (GrantView, error) {
	taskID = strings.TrimSpace(taskID)
	profileID = strings.TrimSpace(profileID)
	if taskID == "" {
		return GrantView{}, badRequest("browser_grant_invalid", "缺少 Task")
	}
	if _, err := s.store.Task(ctx, taskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GrantView{}, notFound("browser_task_not_found", "Task 不存在")
		}
		return GrantView{}, err
	}
	caps, err := normalizeCapabilities(capabilities)
	if err != nil {
		return GrantView{}, err
	}
	if ttl <= 0 {
		ttl = DefaultGrantTTL
	}
	if ttl < MinGrantTTL || ttl > MaxGrantTTL {
		return GrantView{}, badRequest("browser_grant_invalid",
			"授权有效期必须在 1 分钟到 24 小时之间")
	}
	profile, err := s.store.BrowserProfile(ctx, profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return GrantView{}, notFound("browser_profile_not_found", "浏览器 Profile 不存在")
	}
	if err != nil {
		return GrantView{}, err
	}
	if profile.HostID != s.hostID {
		return GrantView{}, conflict("browser_profile_host_mismatch",
			"该 Profile 绑定在浏览器宿主 %s 上，当前 AHA 只连接 %s", profile.HostID, s.hostID)
	}
	session, reused, err := s.ensureSession(ctx, actor, profile.ID)
	if err != nil {
		return GrantView{}, err
	}
	switch session.State {
	case domain.BrowserSessionStateReady, domain.BrowserSessionStateBusy:
	case domain.BrowserSessionStateAuthRequired:
		return GrantView{}, conflict("browser_auth_required",
			"该 Profile 需要 Owner 在可见窗口中重新登录，请先打开登录窗口")
	case domain.BrowserSessionStateStarting:
		return GrantView{}, conflict("browser_session_not_ready", "浏览器会话正在启动，请稍后重试")
	default:
		return GrantView{}, conflict("browser_session_not_ready", "浏览器会话当前不可用（%s）", session.State)
	}

	// One active grant per Task/Profile keeps ownership unambiguous. The service
	// lock serializes creates/revokes in this process, and the store transaction
	// is the cross-process backstop: it revokes the previous active grant and
	// inserts the new one under one immediate write lock.
	now := s.now()
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	current, err := s.store.BrowserSession(ctx, session.ID)
	if err != nil || current.State == domain.BrowserSessionStateClosed {
		return GrantView{}, conflict("browser_session_changed", "浏览器会话已关闭，请重新附加")
	}
	active, activeErr := s.store.ActiveBrowserSession(ctx, profile.ID)
	if activeErr != nil || active.ID != session.ID {
		return GrantView{}, conflict("browser_session_changed", "Profile 已切换到新的会话，请重新授权")
	}
	grant := domain.BrowserTaskGrant{
		ID: domain.NewID("browser_grant"), TaskID: taskID, ProfileID: profile.ID,
		SessionID: session.ID, Capabilities: caps, ExpiresAt: now.Add(ttl),
		CreatedAt: now, UpdatedAt: now,
	}
	var revoked []domain.BrowserTaskGrant
	for attempt := 0; attempt < 2; attempt++ {
		revoked, err = s.store.CreateBrowserTaskGrantSuperseding(ctx, grant, now)
		if err == nil {
			break
		}
		// The immediate transaction should serialize writers. If a driver
		// still surfaces the unique active-grant index, retry once so the
		// transaction revokes the winner before inserting.
		if !isUniqueConstraintError(err) || attempt == 1 {
			return GrantView{}, err
		}
	}
	for _, previous := range revoked {
		s.closeGrantTabs(ctx, actor, previous, "superseded")
		s.audit(ctx, actor, "browser_grant.revoke", "browser_task_grant", previous.ID, map[string]any{
			"task_id": previous.TaskID, "profile_id": previous.ProfileID, "session_id": previous.SessionID,
			"reason": "superseded",
		})
	}
	s.audit(ctx, actor, "browser_grant.create", "browser_task_grant", grant.ID, map[string]any{
		"task_id": taskID, "profile_id": profile.ID, "session_id": session.ID,
		"capabilities": caps, "expires_at": grant.ExpiresAt, "reused_session": reused,
	})
	return s.grantView(ctx, grant), nil
}

func (s *Service) closeGrantTabs(ctx context.Context, actor string, grant domain.BrowserTaskGrant, reason string) {
	tabs, err := s.store.BrowserTabsForGrant(ctx, grant.ID)
	if err != nil {
		return
	}
	session, sessionErr := s.store.BrowserSession(ctx, grant.SessionID)
	for _, tab := range tabs {
		if sessionErr == nil && s.host != nil && tab.HostTabID != "" {
			_ = s.host.CloseTab(ctx, grant.ProfileID, session.HostSessionID, tab.HostTabID)
		}
		_ = s.store.DeleteBrowserTab(ctx, tab.ID)
		s.audit(ctx, actor, "browser_tab.close", "browser_tab", tab.ID, map[string]any{
			"task_id": tab.TaskID, "url": tab.URL, "reason": reason,
		})
	}
}

func (s *Service) grantView(ctx context.Context, grant domain.BrowserTaskGrant) GrantView {
	view := GrantView{BrowserTaskGrant: grant, Status: grant.Status(s.now())}
	if session, err := s.store.BrowserSession(ctx, grant.SessionID); err == nil {
		view.Session = &session
	}
	if tabs, err := s.store.BrowserTabsForGrant(ctx, grant.ID); err == nil {
		view.Tabs = tabs
	}
	return view
}

func (s *Service) Grants(ctx context.Context, taskID string) ([]GrantView, error) {
	if _, err := s.store.Task(ctx, strings.TrimSpace(taskID)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("browser_task_not_found", "Task 不存在")
		}
		return nil, err
	}
	grants, err := s.store.BrowserTaskGrants(ctx, taskID)
	if err != nil {
		return nil, err
	}
	result := make([]GrantView, 0, len(grants))
	for _, grant := range grants {
		result = append(result, s.grantView(ctx, grant))
	}
	return result, nil
}

func (s *Service) Grant(ctx context.Context, id string) (GrantView, error) {
	grant, err := s.store.BrowserTaskGrant(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return GrantView{}, notFound("browser_grant_not_found", "浏览器授权不存在")
	}
	if err != nil {
		return GrantView{}, err
	}
	return s.grantView(ctx, grant), nil
}

func (s *Service) RevokeGrant(ctx context.Context, actor, id string) (GrantView, error) {
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	grant, err := s.store.BrowserTaskGrant(ctx, strings.TrimSpace(id))
	if errors.Is(err, sql.ErrNoRows) {
		return GrantView{}, notFound("browser_grant_not_found", "浏览器授权不存在")
	}
	if err != nil {
		return GrantView{}, err
	}
	if grant.RevokedAt.IsZero() {
		// Revoke in the database before touching host tabs. While CloseTab is
		// slow, a concurrent OpenTab must already see the grant as revoked; the
		// guarded tab insert then refuses it. Closing tabs first left a window
		// where a new tab could be inserted after the tab list was read.
		if err := s.store.RevokeBrowserTaskGrant(ctx, grant.ID, s.now()); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return GrantView{}, err
			}
		} else {
			s.audit(ctx, actor, "browser_grant.revoke", "browser_task_grant", grant.ID, map[string]any{
				"task_id": grant.TaskID, "profile_id": grant.ProfileID, "session_id": grant.SessionID,
				"reason": "owner_revoked",
			})
		}
		grant, _ = s.store.BrowserTaskGrant(ctx, grant.ID)
	}
	// Clean up any tab records even when the grant was already revoked; this is
	// also the retry path after a host CloseTab failure.
	s.closeGrantTabs(ctx, actor, grant, "revoked")
	return s.grantView(ctx, grant), nil
}

// TaskBrowserState is the Task detail view: selectable profiles, current
// grants/session and this Task's own tabs. Hidden page content is never part of
// it.
func (s *Service) TaskBrowserState(ctx context.Context, taskID string) (TaskBrowserView, error) {
	if _, err := s.store.Task(ctx, strings.TrimSpace(taskID)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskBrowserView{}, notFound("browser_task_not_found", "Task 不存在")
		}
		return TaskBrowserView{}, err
	}
	profiles, err := s.Profiles(ctx)
	if err != nil {
		return TaskBrowserView{}, err
	}
	grants, err := s.Grants(ctx, taskID)
	if err != nil {
		return TaskBrowserView{}, err
	}
	tabs, err := s.store.BrowserTabsForTask(ctx, taskID)
	if err != nil {
		return TaskBrowserView{}, err
	}
	view := TaskBrowserView{
		TaskID: taskID, Profiles: profiles, Grants: grants, Tabs: tabs,
		HostAvailable: s.host != nil, Host: s.hostView(ctx),
	}
	for _, grant := range grants {
		if grant.Status != domain.BrowserGrantStatusActive || grant.Session == nil {
			continue
		}
		if grant.Session.State != domain.BrowserSessionStateClosed {
			session := *grant.Session
			view.Session = &session
			break
		}
	}
	return view, nil
}

func validateTargetURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", badRequest("browser_url_invalid", "请填写要打开的网址")
	}
	if len(raw) > 4096 {
		return "", badRequest("browser_url_invalid", "网址过长")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", badRequest("browser_url_invalid", "网址无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", badRequest("browser_url_invalid", "只允许 http 或 https 网址")
	}
	return parsed.String(), nil
}

// authorize validates the grant, the current session and (when supplied) the
// tab's ownership in one place. Any mismatch fails closed.
func (s *Service) authorize(ctx context.Context, grantID, capability, tabID string) (domain.BrowserTaskGrant, *domain.BrowserTab, error) {
	grant, err := s.store.BrowserTaskGrant(ctx, strings.TrimSpace(grantID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BrowserTaskGrant{}, nil, notFound("browser_grant_not_found", "浏览器授权不存在")
	}
	if err != nil {
		return domain.BrowserTaskGrant{}, nil, err
	}
	switch grant.Status(s.now()) {
	case domain.BrowserGrantStatusRevoked:
		return domain.BrowserTaskGrant{}, nil, forbidden("browser_grant_revoked", "浏览器授权已撤销")
	case domain.BrowserGrantStatusExpired:
		return domain.BrowserTaskGrant{}, nil, forbidden("browser_grant_expired", "浏览器授权已过期")
	}
	if !grant.Has(capability) {
		return domain.BrowserTaskGrant{}, nil, forbidden("browser_capability_denied",
			"当前授权不包含能力：%s", capability)
	}
	session, err := s.store.BrowserSession(ctx, grant.SessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BrowserTaskGrant{}, nil, conflict("browser_session_changed", "授权对应的会话已不存在")
	}
	if err != nil {
		return domain.BrowserTaskGrant{}, nil, err
	}
	if session.State == domain.BrowserSessionStateClosed {
		return domain.BrowserTaskGrant{}, nil, conflict("browser_session_closed", "浏览器会话已关闭，请重新附加")
	}
	if session.State != domain.BrowserSessionStateReady && session.State != domain.BrowserSessionStateBusy {
		return domain.BrowserTaskGrant{}, nil, conflict("browser_session_not_ready",
			"浏览器会话当前不可用（%s）", session.State)
	}
	active, err := s.store.ActiveBrowserSession(ctx, grant.ProfileID)
	if err != nil || active.ID != grant.SessionID {
		return domain.BrowserTaskGrant{}, nil, conflict("browser_session_changed", "Profile 已切换到新的会话，请重新授权")
	}
	if s.host == nil {
		return domain.BrowserTaskGrant{}, nil, newError(http.StatusServiceUnavailable,
			"browser_host_unavailable", "浏览器宿主未配置")
	}
	if strings.TrimSpace(tabID) == "" {
		return grant, nil, nil
	}
	tab, err := s.store.BrowserTab(ctx, strings.TrimSpace(tabID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BrowserTaskGrant{}, nil, notFound("browser_tab_not_found", "浏览器标签页不存在")
	}
	if err != nil {
		return domain.BrowserTaskGrant{}, nil, err
	}
	if tab.SessionID != grant.SessionID || tab.GrantID != grant.ID || tab.TaskID != grant.TaskID {
		return domain.BrowserTaskGrant{}, nil, forbidden("browser_tab_forbidden",
			"该标签页不属于当前 Task 的授权")
	}
	return grant, &tab, nil
}

func (s *Service) touchSession(ctx context.Context, session domain.BrowserSession) {
	now := s.now()
	// Touch-only writes: a slow host call must not overwrite a newer
	// state/closed_at or resurrect a session that was closed in parallel.
	_ = s.store.TouchBrowserSession(ctx, session.ID, now)
	_ = s.store.TouchBrowserProfile(ctx, session.ProfileID, now)
}

// inactiveAccessError explains why a guarded tab write failed after the host
// call. It always fails closed; the caller closes any newly created host tab.
func (s *Service) inactiveAccessError(ctx context.Context, grantID string) error {
	grant, err := s.store.BrowserTaskGrant(ctx, grantID)
	if err != nil {
		return conflict("browser_session_changed", "浏览器会话已换代，请重新授权")
	}
	switch grant.Status(s.now()) {
	case domain.BrowserGrantStatusRevoked:
		return forbidden("browser_grant_revoked", "浏览器授权已撤销")
	case domain.BrowserGrantStatusExpired:
		return forbidden("browser_grant_expired", "浏览器授权已过期")
	}
	session, err := s.store.BrowserSession(ctx, grant.SessionID)
	if err == nil && session.State == domain.BrowserSessionStateClosed {
		return conflict("browser_session_closed", "浏览器会话已关闭，请重新附加")
	}
	return conflict("browser_session_changed", "浏览器会话已换代，请重新授权")
}

func (s *Service) sessionForGrant(ctx context.Context, grant domain.BrowserTaskGrant) (domain.BrowserSession, error) {
	session, err := s.store.BrowserSession(ctx, grant.SessionID)
	if err != nil {
		return domain.BrowserSession{}, conflict("browser_session_changed", "授权对应的会话已不存在")
	}
	return session, nil
}

func (s *Service) OpenTab(ctx context.Context, actor, grantID, targetURL string) (domain.BrowserTab, error) {
	grant, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilityOpenTab, "")
	if err != nil {
		return domain.BrowserTab{}, err
	}
	targetURL, err = validateTargetURL(targetURL)
	if err != nil {
		return domain.BrowserTab{}, err
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return domain.BrowserTab{}, err
	}
	hostTab, err := s.host.OpenTab(ctx, grant.ProfileID, session.HostSessionID, targetURL)
	if err != nil {
		return domain.BrowserTab{}, sanitizeHostError(err)
	}
	tab := domain.BrowserTab{
		ID: domain.NewID("browser_tab"), SessionID: grant.SessionID, GrantID: grant.ID,
		TaskID: grant.TaskID, HostTabID: hostTab.ID, URL: hostTab.URL, Title: hostTab.Title,
	}
	if tab.URL == "" {
		tab.URL = targetURL
	}
	if err := s.store.CreateBrowserTabForActiveGrant(ctx, tab, s.now()); err != nil {
		_ = s.host.CloseTab(ctx, grant.ProfileID, session.HostSessionID, hostTab.ID)
		if errors.Is(err, store.ErrBrowserAccessInactive) {
			return domain.BrowserTab{}, s.inactiveAccessError(ctx, grant.ID)
		}
		return domain.BrowserTab{}, err
	}
	s.touchSession(ctx, session)
	s.audit(ctx, actor, "browser_tab.open", "browser_tab", tab.ID, map[string]any{
		"task_id": tab.TaskID, "profile_id": grant.ProfileID, "session_id": grant.SessionID, "url": tab.URL,
	})
	return tab, nil
}

func (s *Service) NavigateTab(ctx context.Context, actor, grantID, tabID, targetURL string) (domain.BrowserTab, error) {
	grant, tab, err := s.authorize(ctx, grantID, domain.BrowserCapabilityNavigate, tabID)
	if err != nil {
		return domain.BrowserTab{}, err
	}
	targetURL, err = validateTargetURL(targetURL)
	if err != nil {
		return domain.BrowserTab{}, err
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return domain.BrowserTab{}, err
	}
	hostTab, err := s.host.Navigate(ctx, grant.ProfileID, session.HostSessionID, tab.HostTabID, targetURL)
	if err != nil {
		return domain.BrowserTab{}, sanitizeHostError(err)
	}
	if _, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilityNavigate, tabID); err != nil {
		return domain.BrowserTab{}, err
	}
	if hostTab.URL != "" {
		tab.URL = hostTab.URL
	} else {
		tab.URL = targetURL
	}
	tab.Title = hostTab.Title
	if err := s.store.UpdateBrowserTab(ctx, *tab); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// A concurrent revoke/close removed the tab after the host call.
			return domain.BrowserTab{}, s.inactiveAccessError(ctx, grant.ID)
		}
		return domain.BrowserTab{}, err
	}
	s.touchSession(ctx, session)
	s.audit(ctx, actor, "browser_tab.navigate", "browser_tab", tab.ID, map[string]any{
		"task_id": tab.TaskID, "profile_id": grant.ProfileID, "url": tab.URL,
	})
	return *tab, nil
}

func (s *Service) SnapshotTab(ctx context.Context, actor, grantID, tabID string) (HostSnapshot, error) {
	grant, tab, err := s.authorize(ctx, grantID, domain.BrowserCapabilitySnapshot, tabID)
	if err != nil {
		return HostSnapshot{}, err
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return HostSnapshot{}, err
	}
	snapshot, err := s.host.Snapshot(ctx, grant.ProfileID, session.HostSessionID, tab.HostTabID)
	if err != nil {
		return HostSnapshot{}, sanitizeHostError(err)
	}
	if _, _, err := s.authorize(ctx, grantID, domain.BrowserCapabilitySnapshot, tabID); err != nil {
		return HostSnapshot{}, err
	}
	s.touchSession(ctx, session)
	s.audit(ctx, actor, "browser_tab.snapshot", "browser_tab", tab.ID, map[string]any{
		"task_id": tab.TaskID, "profile_id": grant.ProfileID, "url": snapshot.URL,
	})
	return snapshot, nil
}

func (s *Service) CloseTab(ctx context.Context, actor, grantID, tabID string) error {
	grant, tab, err := s.authorize(ctx, grantID, domain.BrowserCapabilityNavigate, tabID)
	if err != nil {
		return err
	}
	session, err := s.sessionForGrant(ctx, grant)
	if err != nil {
		return err
	}
	if err := s.host.CloseTab(ctx, grant.ProfileID, session.HostSessionID, tab.HostTabID); err != nil &&
		Code(err) != "browser_tab_not_found" {
		return sanitizeHostError(err)
	}
	if err := s.store.DeleteBrowserTab(ctx, tab.ID); err != nil {
		return err
	}
	s.audit(ctx, actor, "browser_tab.close", "browser_tab", tab.ID, map[string]any{
		"task_id": tab.TaskID, "profile_id": grant.ProfileID, "url": tab.URL, "reason": "owner_closed",
	})
	return nil
}

func (s *Service) Tabs(ctx context.Context, grantID string) ([]domain.BrowserTab, error) {
	grant, err := s.store.BrowserTaskGrant(ctx, strings.TrimSpace(grantID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("browser_grant_not_found", "浏览器授权不存在")
	}
	if err != nil {
		return nil, err
	}
	return s.store.BrowserTabsForGrant(ctx, grant.ID)
}

func (s *Service) audit(ctx context.Context, actor, action, resourceType, resourceID string, data map[string]any) {
	if actor = strings.TrimSpace(actor); actor == "" {
		actor = "system"
	}
	_ = s.store.AppendAudit(ctx, actor, action, resourceType, resourceID, data, s.now().Format(time.RFC3339Nano))
}

package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	sessionTTL     = 30 * time.Minute
	observationTTL = 30 * time.Second
	operationTTL   = 12 * time.Second
)

type Error struct{ Code string }

func (e *Error) Error() string  { return e.Code }
func failure(code string) error { return &Error{Code: "desktop_" + code} }

type observed struct {
	id           string
	stream       string
	at           time.Time
	elements     []Element
	surface      string
	width        int
	height       int
	inputActions []string
}

type sharedSession struct {
	Session
	expires        time.Time
	claimTurn      string
	claimPending   chan struct{}
	observed       map[string][]observed
	streams        map[string]string
	replayed       map[string]time.Time
	cancel         context.CancelFunc
	operationID    string
	captureCancel  context.CancelFunc
	captureID      string
	captureWaiters map[string]context.CancelFunc
	actionEpoch    uint64
}

// Manager holds explicitly granted, short-lived host access. Nothing is persisted:
// restarting the host must revoke access rather than revive an old HWND grant.
type Manager struct {
	mu                sync.Mutex
	provider          Provider
	sessions          map[string]*sharedSession
	running           map[string]string
	capturing         map[string]string
	pendingForeground map[string]string
	pendingCancel     map[string]context.CancelFunc
	runningForeground string
	now               func() time.Time
	closed            atomic.Bool
	captureQueue      captureQueue
}

func New(provider Provider) *Manager {
	return &Manager{provider: provider, sessions: make(map[string]*sharedSession), running: make(map[string]string), capturing: make(map[string]string),
		pendingForeground: make(map[string]string), pendingCancel: make(map[string]context.CancelFunc), now: time.Now}
}

func token() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("desktop: random source unavailable")
	}
	return hex.EncodeToString(b[:])
}

func (m *Manager) support() (bool, string) {
	if m == nil || m.provider == nil {
		return false, "desktop_unavailable"
	}
	if m.closed.Load() {
		return false, "desktop_unavailable"
	}
	return m.provider.Support()
}

// Close revokes grants before reaping resident native workers.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed.Store(true)
	for _, cancel := range m.pendingCancel {
		cancel()
	}
	for taskID, entry := range m.sessions {
		revokeOperation(entry)
		delete(m.sessions, taskID)
	}
	m.mu.Unlock()
	if provider, ok := m.provider.(io.Closer); ok {
		return provider.Close()
	}
	return nil
}

func (m *Manager) cleanLocked() {
	for taskID, entry := range m.sessions {
		if !m.now().Before(entry.expires) {
			revokeOperation(entry)
			delete(m.sessions, taskID)
		}
	}
}

func (m *Manager) statusLocked(taskID string) Status {
	supported, reason := m.support()
	result := Status{Supported: supported, Reason: reason, SharedControlSupported: supported}
	_, foreground := m.provider.(ForegroundProvider)
	result.ForegroundSupported = supported && foreground
	_, preview := m.provider.(PreviewProvider)
	result.StreamSupported = result.ForegroundSupported && preview
	_, targets := m.provider.(TargetProvider)
	result.TargetsSupported = supported && targets
	_, background := m.provider.(BackgroundTargetProvider)
	result.BackgroundDesktopSupported = supported && background
	if entry := m.sessions[taskID]; entry != nil {
		copy := entry.Session
		result.Session = &copy
	}
	return result
}

func (m *Manager) Status(taskID string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanLocked()
	return m.statusLocked(taskID)
}

func (m *Manager) Windows(ctx context.Context) ([]Window, error) {
	if supported, _ := m.support(); !supported {
		return nil, failure("unsupported")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTTL)
	defer cancel()
	release, err := m.captureQueue.acquire(ctx, true)
	if err != nil {
		return nil, err
	}
	defer release()
	if supported, _ := m.support(); !supported {
		return nil, failure("unsupported")
	}
	windows, err := m.provider.Windows(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if windows == nil && err == nil {
		windows = []Window{}
	}
	return windows, err
}

func (m *Manager) Open(ctx context.Context, taskID, windowID string) (Status, error) {
	return m.OpenWithMode(ctx, taskID, windowID, "background", false)
}

func (m *Manager) foregroundAvailableLocked() bool {
	if m.runningForeground != "" || len(m.pendingForeground) != 0 || len(m.capturing) != 0 {
		return false
	}
	for _, entry := range m.sessions {
		if entry.Mode == "foreground" {
			return false
		}
	}
	return true
}

func (m *Manager) OpenWithMode(ctx context.Context, taskID, windowID, mode string, confirmed bool) (Status, error) {
	return m.open(ctx, taskID, windowID, mode, confirmed, nil, false)
}

func (m *Manager) open(ctx context.Context, taskID, windowID, mode string, confirmed bool, selection *TargetSelection, shared bool) (Status, error) {
	if mode == "" {
		mode = "background"
	}
	if mode != "background" && mode != "foreground" {
		return Status{}, failure("invalid_mode")
	}
	if mode == "foreground" && !confirmed {
		return Status{}, failure("foreground_confirmation_required")
	}
	newDesktop := windowID == "new-desktop"
	if newDesktop && mode != "foreground" {
		return Status{}, failure("foreground_confirmation_required")
	}
	if taskID == "" || windowID == "" || len(windowID) > 512 {
		return Status{}, failure("invalid_target")
	}
	m.mu.Lock()
	pending := m.pendingCancel[taskID] != nil
	m.mu.Unlock()
	if pending {
		return Status{}, failure("busy")
	}
	var foreground ForegroundProvider
	if mode == "foreground" {
		var ok bool
		foreground, ok = m.provider.(ForegroundProvider)
		if supported, _ := m.support(); !ok || !supported {
			return Status{}, failure("unsupported")
		}
		m.mu.Lock()
		m.cleanLocked()
		if m.sessions[taskID] != nil {
			m.mu.Unlock()
			return Status{}, failure("session_exists")
		}
		if !m.foregroundAvailableLocked() {
			m.mu.Unlock()
			return Status{}, failure("foreground_in_use")
		}
		if len(m.sessions) >= 128 {
			m.mu.Unlock()
			return Status{}, failure("session_limit")
		}
		openCtx, cancel := context.WithTimeout(ctx, operationTTL)
		ctx = openCtx
		reservation := token()
		m.pendingForeground[taskID] = reservation
		m.pendingCancel[taskID] = cancel
		m.mu.Unlock()
		defer func() {
			cancel()
			m.mu.Lock()
			if m.pendingForeground[taskID] == reservation {
				delete(m.pendingForeground, taskID)
				delete(m.pendingCancel, taskID)
			}
			m.mu.Unlock()
		}()
	}
	target, err := m.resolveSelection(ctx, windowID, mode, foreground, selection)
	if err != nil {
		return Status{}, err
	}
	windowID = target.ID
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanLocked()
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	if m.closed.Load() {
		return Status{}, failure("unavailable")
	}
	if m.sessions[taskID] != nil {
		return Status{}, failure("session_exists")
	}
	if mode == "background" && m.pendingCancel[taskID] != nil {
		return Status{}, failure("busy")
	}
	if m.running[windowID] != "" || m.capturing[windowID] != "" {
		return Status{}, failure("busy")
	}
	for _, entry := range m.sessions {
		if entry.Window.ID == windowID {
			return Status{}, failure("window_in_use")
		}
	}
	if len(m.sessions) >= 128 {
		return Status{}, failure("session_limit")
	}
	expires := m.now().Add(sessionTTL)
	controller := "owner"
	if shared {
		controller = "shared"
	}
	m.sessions[taskID] = &sharedSession{
		Session: Session{ID: token(), TaskID: taskID, Window: target, Controller: controller, Revision: 1, Mode: mode,
			ExpiresAt: expires.UTC().Format(time.RFC3339)},
		expires: expires, observed: make(map[string][]observed), replayed: make(map[string]time.Time),
	}
	return m.statusLocked(taskID), nil
}

func (m *Manager) sessionLocked(taskID, sessionID string, revision uint64) (*sharedSession, error) {
	m.cleanLocked()
	entry := m.sessions[taskID]
	if entry == nil {
		return nil, failure("not_shared")
	}
	if entry.ID != sessionID || revision != 0 && entry.Revision != revision {
		return nil, failure("stale_session")
	}
	return entry, nil
}

func revokeOperation(entry *sharedSession) {
	if entry.cancel != nil {
		entry.cancel()
	}
	if entry.captureCancel != nil {
		entry.captureCancel()
	}
	for _, cancel := range entry.captureWaiters {
		cancel()
	}
	// Keep operationID until its worker exits. A control transfer cannot let a
	// second native action overlap with an already-dispatched target operation.
	entry.observed = make(map[string][]observed)
	if entry.Controller != "shared" {
		entry.replayed = make(map[string]time.Time)
	}
	entry.actionEpoch++
	entry.claimTurn = ""
	entry.Claimed = false
}

func (m *Manager) Control(taskID, sessionID string, revision uint64, controller string) (Status, error) {
	if revision == 0 || controller != "owner" && controller != "agent" {
		return Status{}, failure("invalid_control")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, err := m.sessionLocked(taskID, sessionID, revision)
	if err != nil {
		return Status{}, err
	}
	if entry.Switching {
		return Status{}, failure("switching")
	}
	if entry.Controller == "shared" {
		return Status{}, failure("shared_control")
	}
	revokeOperation(entry)
	entry.Revision++
	entry.Controller = controller
	return m.statusLocked(taskID), nil
}

func (m *Manager) Stop(taskID, sessionID string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, err := m.sessionLocked(taskID, sessionID, 0)
	if err != nil {
		return Status{}, err
	}
	if cancel := m.pendingCancel[taskID]; cancel != nil {
		cancel()
	}
	revokeOperation(entry)
	delete(m.sessions, taskID)
	return m.statusLocked(taskID), nil
}

// Revoke is used when a task becomes ineligible (terminal, retired or deleted).
func (m *Manager) Revoke(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel := m.pendingCancel[taskID]; cancel != nil {
		cancel()
	}
	if entry := m.sessions[taskID]; entry != nil {
		revokeOperation(entry)
		delete(m.sessions, taskID)
	}
}

func (m *Manager) Claim(ctx context.Context, taskID, sessionID string, revision uint64, turnID string, notice func(Window) error) (Status, error) {
	if revision == 0 || turnID == "" || notice == nil {
		return Status{}, failure("invalid_control")
	}
	var entry *sharedSession
	var err error
	// Concurrent Agent reads share one persisted notice, not several handoffs.
	for {
		m.mu.Lock()
		entry, err = m.sessionLocked(taskID, sessionID, revision)
		if err == nil && entry.Switching {
			err = failure("switching")
		}
		if err == nil && entry.Controller != "agent" && entry.Controller != "shared" {
			err = failure("owner_control")
		}
		if err != nil {
			m.mu.Unlock()
			return Status{}, err
		}
		if entry.claimTurn == turnID {
			status := m.statusLocked(taskID)
			m.mu.Unlock()
			return status, nil
		}
		if pending := entry.claimPending; pending != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return Status{}, ctx.Err()
			case <-pending:
				continue
			}
		}
		if entry.operationID != "" && entry.Controller != "shared" {
			m.mu.Unlock()
			return Status{}, failure("busy")
		}
		entry.claimPending = make(chan struct{})
		m.mu.Unlock()
		break
	}
	reserved := entry
	defer func() {
		m.mu.Lock()
		close(reserved.claimPending)
		reserved.claimPending = nil
		m.mu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	// Persist the user-visible software notice before granting any action.
	if err := notice(entry.Window); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, err = m.sessionLocked(taskID, sessionID, revision)
	if err != nil {
		return Status{}, err
	}
	if entry.Switching {
		return Status{}, failure("switching")
	}
	if entry.Controller != "agent" && entry.Controller != "shared" {
		return Status{}, failure("owner_control")
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	entry.claimTurn = turnID
	entry.Claimed = true
	return m.statusLocked(taskID), nil
}

func (m *Manager) begin(ctx context.Context, taskID, sessionID, actor string, revision uint64, action *ActionRequest, capture *Observation, foreground *bool) (context.Context, Window, string, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, err := m.sessionLocked(taskID, sessionID, revision)
	if err != nil {
		return nil, Window{}, "", 0, err
	}
	if entry.Switching {
		return nil, Window{}, "", 0, failure("switching")
	}
	*foreground = entry.Mode == "foreground"
	isAgent := strings.HasPrefix(actor, "agent:") && len(actor) > len("agent:")
	if !ownerActor(actor) && !isAgent {
		return nil, Window{}, "", 0, failure("invalid_actor")
	}
	if isAgent && entry.Controller != "agent" && entry.Controller != "shared" {
		return nil, Window{}, "", 0, failure("owner_control")
	}
	if action != nil {
		if ownerActor(actor) && entry.Controller != "owner" && entry.Controller != "shared" {
			return nil, Window{}, "", 0, failure("agent_control")
		}
		if isAgent && entry.claimTurn != strings.TrimPrefix(actor, "agent:") {
			return nil, Window{}, "", 0, failure("claim_required")
		}
		m.pruneFrames(entry)
		snapshot, ok := findSnapshot(entry.observed[actor], action.ObservationID)
		if !ok {
			return nil, Window{}, "", 0, failure("stale_observation")
		}
		var supported bool
		if entry.Mode == "foreground" {
			supported = action.ElementID == "$surface" && slices.Contains(snapshot.inputActions, action.Kind)
			if supported {
				if err := validateForegroundInput(action.Action, snapshot.width, snapshot.height); err != nil {
					return nil, Window{}, "", 0, err
				}
			}
		} else {
			for _, element := range snapshot.elements {
				if element.ID == action.ElementID && slices.Contains(element.Actions, action.Kind) {
					supported = true
					break
				}
			}
		}
		if !supported {
			return nil, Window{}, "", 0, failure("unsupported_action")
		}
		if action.ActionID != "" {
			if _, exists := entry.replayed[actor+":"+action.ActionID]; exists {
				return nil, Window{}, "", 0, failure("action_replayed")
			}
			if len(entry.replayed) >= 4096 {
				return nil, Window{}, "", 0, failure("action_limit")
			}
		}
		*capture = Observation{Window: entry.Window, Width: snapshot.width, Height: snapshot.height,
			Surface: snapshot.surface, InputActions: slices.Clone(snapshot.inputActions)}
	}
	if entry.operationID != "" || entry.Mode != "foreground" && entry.captureID != "" {
		return nil, Window{}, "", 0, failure("busy")
	}
	opID := token()
	deadline := m.now().Add(operationTTL)
	if entry.expires.Before(deadline) {
		deadline = entry.expires
	}
	opCtx, cancel := context.WithDeadline(ctx, deadline)
	entry.cancel, entry.operationID = cancel, opID
	m.running[entry.Window.ID] = opID
	if entry.Mode == "foreground" {
		m.runningForeground = opID
	}
	if action != nil {
		entry.actionEpoch++
		if entry.Controller == "shared" {
			// Assistance changes the target underneath other participants.
			// Their old screenshots may not authorize later input.
			for view := range entry.observed {
				if view != actor {
					delete(entry.observed, view)
				}
			}
		}
		if action.ActionID == "" {
			entry.observed[actor] = slices.DeleteFunc(entry.observed[actor], func(o observed) bool { return o.id == action.ObservationID })
		} else {
			// Retain IDs for the full grant, not just the frame TTL: replaying an ID
			// with a newer screenshot must never repeat an uncertain action.
			entry.replayed[actor+":"+action.ActionID] = entry.expires
		}
	}
	return opCtx, entry.Window, opID, entry.Revision, nil
}

func (m *Manager) finish(taskID, sessionID, opID string, revision uint64) (*sharedSession, error) {
	if m.runningForeground == opID {
		m.runningForeground = ""
	}
	for windowID, runningID := range m.running {
		if runningID == opID {
			delete(m.running, windowID)
		}
	}
	entry, err := m.sessionLocked(taskID, sessionID, 0)
	if err != nil {
		return nil, err
	}
	if entry.operationID == opID {
		if entry.cancel != nil {
			entry.cancel()
		}
		entry.operationID, entry.cancel = "", nil
	}
	if entry.Revision != revision {
		return nil, failure("stale_session")
	}
	return entry, nil
}

func (m *Manager) Observe(ctx context.Context, taskID, sessionID, actor string) (Observation, error) {
	return m.observe(ctx, taskID, sessionID, actor, nil, false)
}

func (m *Manager) ObservePreview(ctx context.Context, taskID, sessionID, actor string, options FrameOptions) (Observation, error) {
	options, err := NormalizeFrameOptions(options)
	if err != nil {
		return Observation{}, err
	}
	return m.observe(ctx, taskID, sessionID, actor, &options, false)
}

func (m *Manager) ObserveStream(ctx context.Context, taskID, sessionID, actor string, options FrameOptions) (Observation, error) {
	options, err := NormalizeFrameOptions(options)
	if err != nil {
		return Observation{}, err
	}
	return m.observe(ctx, taskID, sessionID, actor, &options, true)
}

func (m *Manager) observe(ctx context.Context, taskID, sessionID, actor string, options *FrameOptions, streaming bool) (Observation, error) {
	op, err := m.startCapture(ctx, taskID, sessionID, actor, streaming)
	if err != nil {
		return Observation{}, err
	}
	defer op.close()
	opCtx, window, opID, revision, epoch := op.ctx, op.window, op.id, op.revision, op.epoch
	foreground, streamID := op.foreground, op.streamID
	var observation Observation
	var nativeErr error
	if foreground {
		if preview, ok := m.provider.(PreviewProvider); ok && options != nil {
			observation, nativeErr = preview.ObservePreview(opCtx, window, *options)
		} else {
			observation, nativeErr = m.provider.(ForegroundProvider).ObserveForeground(opCtx, window)
		}
	} else {
		observation, nativeErr = m.provider.Observe(opCtx, window)
	}
	if nativeErr == nil {
		nativeErr = opCtx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.capturing[window.ID] == opID {
		delete(m.capturing, window.ID)
	}
	entry, err := m.sessionLocked(taskID, sessionID, 0)
	if err != nil {
		return Observation{}, err
	}
	if entry.captureID == opID {
		entry.captureID, entry.captureCancel = "", nil
	}
	if entry.Revision != revision {
		return Observation{}, failure("stale_session")
	}
	if entry.actionEpoch != epoch {
		return Observation{}, failure("frame_superseded")
	}
	if streaming && entry.streams[actor] != streamID {
		return Observation{}, failure("stream_closed")
	}
	if nativeErr != nil {
		if code := ErrorCode(nativeErr); code == "desktop_focus_side_effect" || code == "desktop_focus_unverifiable" ||
			code == "desktop_background_desktop_changed" || code == "desktop_background_context_changed" ||
			code == "desktop_background_control_changed" || code == "desktop_background_child_unverifiable" ||
			code == "desktop_browser_accessibility_unavailable" || code == "desktop_browser_scope_changed" ||
			code == "desktop_browser_page_unavailable" {
			revokeOperation(entry)
			if entry.Controller != "shared" {
				entry.Controller = "owner"
			}
			entry.Revision++
		}
		return Observation{}, nativeErr
	}
	if len(observation.Image) > 16*1024*1024 || len(observation.Elements) > 1000 ||
		len(observation.Surface) > 4096 || len(observation.InputActions) > 16 ||
		observation.Width < 0 || observation.Height < 0 ||
		observation.Width > 32768 || observation.Height > 32768 {
		return Observation{}, failure("observation_too_large")
	}
	observation.ID, observation.SessionID, observation.Revision = token(), sessionID, revision
	observation.Window = window
	if observation.Elements == nil {
		observation.Elements = []Element{}
	}
	if observation.ControlError != "" {
		observation.InputActions = nil
		observation.Elements = slices.Clone(observation.Elements)
		for i := range observation.Elements {
			observation.Elements[i].Actions = nil
		}
	}
	// Keep recent displayed frames per view, including frames still in transit.
	key := actor
	if strings.HasPrefix(actor, "agent:") {
		for existing := range entry.observed {
			if existing != actor && strings.HasPrefix(existing, "agent:") {
				delete(entry.observed, existing)
			}
		}
	}
	if !foreground {
		observation.InputActions, observation.Surface = nil, ""
	}
	elements := make([]Element, len(observation.Elements))
	for i, element := range observation.Elements {
		elements[i] = Element{ID: element.ID, Actions: slices.Clone(element.Actions)}
	}
	entry.observed[key] = append(entry.observed[key], observed{id: observation.ID, stream: streamID, at: m.now(), elements: elements,
		surface: observation.Surface, width: observation.Width, height: observation.Height,
		inputActions: slices.Clone(observation.InputActions)})
	limit := 4
	if foreground {
		limit = 64
	}
	if frames := entry.observed[key]; len(frames) > limit {
		entry.observed[key] = slices.Clone(frames[len(frames)-limit:])
	}
	return observation, nil
}

func (m *Manager) Act(ctx context.Context, taskID, actor string, request ActionRequest) (Status, error) {
	var err error
	actor, err = ViewActor(actor, request.ViewID)
	if err != nil {
		return Status{}, err
	}
	if request.ActionID != "" && !ValidViewID(request.ActionID) {
		return Status{}, failure("invalid_action")
	}
	if request.Revision == 0 || request.ObservationID == "" || request.ElementID == "" ||
		len(request.ElementID) > 2048 || !utf8.ValidString(request.Value) ||
		utf8.RuneCountInString(request.Value) > 8192 || strings.ContainsRune(request.Value, '\x00') {
		return Status{}, failure("invalid_action")
	}
	switch request.Kind {
	case "invoke", "set_value", "toggle", "select", "expand", "collapse",
		"click", "double_click", "drag", "scroll", "text", "key", "focus":
	default:
		return Status{}, failure("unsupported_action")
	}
	if request.Kind != "set_value" && request.Kind != "text" && request.Value != "" {
		return Status{}, failure("invalid_action")
	}
	if request.ElementID != "$surface" && (request.X != 0 || request.Y != 0 ||
		request.EndX != 0 || request.EndY != 0 || request.DeltaX != 0 || request.DeltaY != 0 ||
		request.Button != "" || len(request.Keys) != 0) {
		return Status{}, failure("invalid_action")
	}
	var capture Observation
	var foreground bool
	opCtx, window, opID, revision, err := m.begin(ctx, taskID, request.SessionID, actor, request.Revision, &request, &capture, &foreground)
	if err != nil {
		return Status{}, err
	}
	var nativeErr error
	if opCtx.Err() != nil {
		nativeErr = opCtx.Err()
	} else if foreground {
		nativeErr = m.provider.(ForegroundProvider).ActForeground(opCtx, window, request.Action, capture)
	} else {
		nativeErr = m.provider.Act(opCtx, window, request.Action)
	}
	if nativeErr == nil {
		nativeErr = opCtx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, err := m.finish(taskID, request.SessionID, opID, revision)
	if err != nil {
		return Status{}, err
	}
	if nativeErr != nil {
		// A timeout may mean the target accepted an operation without responding.
		// Pause on every native failure; do not automatically retry uncertain work.
		revokeOperation(entry)
		if entry.Controller != "shared" {
			entry.Controller = "owner"
		}
		entry.Revision++
		return Status{}, nativeErr
	}
	return m.statusLocked(taskID), nil
}

func validateForegroundInput(action Action, width, height int) error {
	for _, value := range []float64{action.X, action.Y, action.EndX, action.EndY, action.DeltaX, action.DeltaY} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return failure("invalid_action")
		}
	}
	pointValid := func(x, y float64) bool {
		return width > 0 && height > 0 && x >= 0 && y >= 0 && x < float64(width) && y < float64(height)
	}
	pointer := action.Kind == "click" || action.Kind == "double_click" || action.Kind == "drag" || action.Kind == "scroll"
	if !pointer && (action.X != 0 || action.Y != 0 || action.Button != "") ||
		action.Kind != "key" && len(action.Keys) != 0 ||
		action.Kind != "drag" && (action.EndX != 0 || action.EndY != 0) ||
		action.Kind != "scroll" && (action.DeltaX != 0 || action.DeltaY != 0) {
		return failure("invalid_action")
	}
	switch action.Kind {
	case "click", "double_click", "drag", "scroll":
		if !pointValid(action.X, action.Y) {
			return failure("invalid_coordinates")
		}
		if action.Button != "" && action.Button != "left" && action.Button != "right" && action.Button != "middle" {
			return failure("invalid_action")
		}
		if action.Kind == "drag" && !pointValid(action.EndX, action.EndY) {
			return failure("invalid_coordinates")
		}
		if math.Abs(action.DeltaX) > 2400 || math.Abs(action.DeltaY) > 2400 {
			return failure("invalid_action")
		}
		if action.Kind == "scroll" && (action.Button != "" || action.DeltaX == 0 && action.DeltaY == 0) {
			return failure("invalid_action")
		}
	case "key":
		if len(action.Keys) == 0 || len(action.Keys) > 4 {
			return failure("invalid_key")
		}
		seen := map[string]bool{}
		ordinary := 0
		for _, key := range action.Keys {
			if seen[key] || !foregroundKeyAllowed(key) {
				return failure("invalid_key")
			}
			seen[key] = true
			if key != "CTRL" && key != "ALT" && key != "SHIFT" && key != "WIN" {
				ordinary++
			}
		}
		if ordinary > 1 {
			return failure("invalid_key")
		}
		if seen["CTRL"] && seen["ALT"] && seen["DELETE"] ||
			seen["WIN"] && seen["CTRL"] && (seen["F4"] || seen["D"] || seen["LEFT"] || seen["RIGHT"]) ||
			seen["WIN"] && (seen["L"] || seen["U"]) {
			return failure("invalid_key")
		}
	case "text":
		if action.Value == "" || len(action.Value) > 4096 {
			return failure("invalid_action")
		}
		for _, character := range action.Value {
			if character < 32 && character != '\n' && character != '\r' && character != '\t' || character == 127 {
				return failure("invalid_action")
			}
		}
	case "focus":
	default:
		return failure("unsupported_action")
	}
	return nil
}

func foregroundKeyAllowed(key string) bool {
	if len(key) == 1 && (key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9') {
		return true
	}
	switch key {
	case "CTRL", "ALT", "SHIFT", "WIN", "ENTER", "TAB", "ESC", "ESCAPE", "BACKSPACE", "DELETE", "INSERT",
		"HOME", "END", "PAGEUP", "PAGEDOWN", "LEFT", "RIGHT", "UP", "DOWN", "SPACE",
		"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12":
		return true
	}
	return false
}

func ErrorCode(err error) string {
	var coded *Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "desktop_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "desktop_cancelled"
	}
	switch err.Error() {
	case "desktop background_desktop_changed", "desktop background_context_changed",
		"desktop background_control_changed", "desktop background_child_unverifiable",
		"desktop browser_accessibility_unavailable", "desktop browser_scope_changed", "desktop browser_page_unavailable":
		return "desktop_" + strings.TrimPrefix(err.Error(), "desktop ")
	case "desktop unsupported_action", "desktop unsupported_target", "desktop stale_target",
		"desktop password_element", "desktop element_unavailable", "desktop element_disabled",
		"desktop element_read_only", "desktop focus_side_effect", "desktop focus_unverifiable",
		"desktop element_limit", "desktop invalid_request", "desktop native_unavailable":
		return "desktop_" + strings.TrimPrefix(err.Error(), "desktop ")
	case "desktop secure_desktop", "desktop elevated_target", "desktop foreground_denied",
		"desktop foreground_changed", "desktop input_busy", "desktop input_blocked", "desktop input_partial",
		"desktop refresh_required",
		"desktop unsafe_key_chord", "desktop point_obscured", "desktop caption_unavailable",
		"desktop foreground_activation_unconfirmed", "desktop target_disabled":
		return "desktop_" + strings.TrimPrefix(err.Error(), "desktop ")
	case "desktop monitor_changed", "desktop monitor_required", "desktop monitor_unavailable",
		"desktop foreground_outside_monitor":
		return "desktop_" + strings.TrimPrefix(err.Error(), "desktop ")
	case "desktop target_state_unavailable", "desktop target_process_unavailable", "desktop target_token_unavailable",
		"desktop target_foreign_session", "desktop target_foreign_user", "desktop foreground_unavailable":
		return "desktop_" + strings.TrimPrefix(err.Error(), "desktop ")
	case "desktop desktop_identity_unavailable", "desktop desktop_creation_unverified", "desktop desktop_changed":
		return strings.TrimPrefix(err.Error(), "desktop ")
	case "desktop desktop_enumeration_unavailable", "desktop desktop_state_inconsistent",
		"desktop desktop_switch_unverified", "desktop desktop_switch_limit":
		return strings.TrimPrefix(err.Error(), "desktop ")
	}
	// Provider error text may contain native window contents or paths.
	return "desktop_native_failed"
}

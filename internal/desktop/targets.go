package desktop

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

func validateSelection(target TargetSelection, mode string, confirmed bool) error {
	if mode != "foreground" && mode != "background" {
		return failure("invalid_mode")
	}
	if mode == "foreground" && !confirmed {
		return failure("foreground_confirmation_required")
	}
	if len(target.WindowID) > 128 || len(target.DesktopID) > 64 || len(target.MonitorID) > 128 {
		return failure("invalid_target")
	}
	for _, id := range []string{target.WindowID, target.DesktopID, target.MonitorID} {
		if len(id) > 512 || !utf8.ValidString(id) || strings.ContainsAny(id, "\x00\r\n\t") {
			return failure("invalid_target")
		}
	}
	switch target.Kind {
	case "window":
		if target.WindowID == "" || target.WindowID == "new-desktop" || target.DesktopID != "" || target.MonitorID != "" {
			return failure("invalid_target")
		}
	case "desktop":
		if target.DesktopID == "" || target.WindowID != "" {
			return failure("invalid_target")
		}
	case "new-desktop":
		if target.DesktopID != "" || target.WindowID != "" {
			return failure("invalid_target")
		}
	default:
		return failure("invalid_target")
	}
	if mode != "foreground" && target.Kind != "window" {
		return failure("foreground_confirmation_required")
	}
	return nil
}

func (m *Manager) Targets(ctx context.Context) (Targets, error) {
	result := Targets{Windows: []Window{}, Monitors: []Monitor{}, Desktops: []VirtualDesktop{}}
	if supported, _ := m.support(); !supported {
		return result, failure("unsupported")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTTL)
	defer cancel()
	provider, supported := m.provider.(TargetProvider)
	if supported {
		release, err := m.captureQueue.acquire(ctx, true)
		if err != nil {
			return result, err
		}
		defer release()
		if supported, _ := m.support(); !supported {
			return result, failure("unsupported")
		}
		value, err := provider.Targets(ctx)
		if err != nil {
			return result, err
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if len(value.Windows) > 512 || len(value.Monitors) > 64 || len(value.Desktops) > 128 {
			return result, failure("target_limit")
		}
		result = value
	} else {
		windows, err := m.Windows(ctx)
		if err != nil {
			return result, err
		}
		result.Windows = windows
		result.DesktopReason = "desktop_target_selection_unsupported"
	}
	if result.Windows == nil {
		result.Windows = []Window{}
	}
	if result.Monitors == nil {
		result.Monitors = []Monitor{}
	}
	if result.Desktops == nil {
		result.Desktops = []VirtualDesktop{}
	}
	return result, nil
}

func selectionWindowID(target TargetSelection) string {
	if target.Kind == "window" {
		return target.WindowID
	}
	if target.Kind == "new-desktop" {
		return "new-desktop"
	}
	return target.DesktopID
}

func (m *Manager) OpenTarget(ctx context.Context, taskID string, target TargetSelection, mode string, confirmed bool) (Status, error) {
	if mode == "" {
		mode = "background"
	}
	if err := validateSelection(target, mode, confirmed); err != nil {
		return Status{}, err
	}
	return m.open(ctx, taskID, selectionWindowID(target), mode, confirmed, &target, false)
}

// OpenSharedTarget is called only after the Owner explicitly consents to joint access.
func (m *Manager) OpenSharedTarget(ctx context.Context, taskID string, target TargetSelection, mode string, confirmed bool) (Status, error) {
	if mode == "" {
		mode = "background"
	}
	if err := validateSelection(target, mode, confirmed); err != nil {
		return Status{}, err
	}
	return m.open(ctx, taskID, selectionWindowID(target), mode, confirmed, &target, true)
}

func (m *Manager) resolveSelection(ctx context.Context, windowID, mode string, foreground ForegroundProvider, selection *TargetSelection) (Window, error) {
	if provider, ok := m.provider.(BackgroundTargetProvider); ok && mode == "background" {
		release, err := m.captureQueue.acquire(ctx, true)
		if err != nil {
			return Window{}, err
		}
		defer release()
		if supported, _ := m.support(); !supported {
			return Window{}, failure("unsupported")
		}
		window, err := provider.SelectBackgroundWindow(ctx, windowID)
		if err != nil {
			return Window{}, err
		}
		if err := ctx.Err(); err != nil {
			return Window{}, err
		}
		if window.ID != windowID || window.ID == "" || window.Kind == "desktop" ||
			window.DesktopID == "" || len(window.DesktopID) > 64 {
			return Window{}, failure("target_mismatch")
		}
		return window, nil
	}
	if provider, ok := m.provider.(TargetProvider); ok && selection != nil && mode == "foreground" {
		window, err := provider.SelectTarget(ctx, *selection)
		if err != nil {
			return Window{}, err
		}
		if err := ctx.Err(); err != nil {
			return Window{}, err
		}
		if window.ID == "" || len(window.ID) > 512 ||
			selection.Kind != "window" && (window.Kind != "desktop" || window.DesktopID == "" || window.MonitorID == "") ||
			selection.Kind == "window" && (window.ID != selection.WindowID || window.Kind == "desktop") ||
			selection.DesktopID != "" && window.DesktopID != selection.DesktopID ||
			selection.MonitorID != "" && window.MonitorID != selection.MonitorID {
			return Window{}, failure("target_mismatch")
		}
		return window, nil
	}
	if selection != nil && (selection.Kind == "desktop" || selection.MonitorID != "") {
		return Window{}, failure("target_selection_unsupported")
	}
	if windowID == "new-desktop" {
		if foreground == nil {
			return Window{}, failure("unsupported")
		}
		window, err := foreground.NewDesktop(ctx)
		if err != nil {
			return Window{}, err
		}
		if err := ctx.Err(); err != nil {
			return Window{}, err
		}
		if window.ID == "" || window.Kind != "desktop" {
			return Window{}, failure("creation_failed")
		}
		return window, nil
	}
	windows, err := m.Windows(ctx)
	if err != nil {
		return Window{}, err
	}
	for _, window := range windows {
		if window.ID == windowID {
			return window, nil
		}
	}
	return Window{}, failure("window_gone")
}

// SwitchTarget leaves the old session present only as a cancelable transition.
// Its revision/frames/claim are revoked before any native target can change.
func (m *Manager) SwitchTarget(ctx context.Context, taskID, sessionID string, revision uint64, target TargetSelection, mode string, confirmed bool) (Status, error) {
	return m.switchTarget(ctx, taskID, sessionID, revision, target, mode, confirmed, false)
}

func (m *Manager) SwitchSharedTarget(ctx context.Context, taskID, sessionID string, revision uint64, target TargetSelection, mode string, confirmed bool) (Status, error) {
	return m.switchTarget(ctx, taskID, sessionID, revision, target, mode, confirmed, true)
}

func (m *Manager) switchTarget(ctx context.Context, taskID, sessionID string, revision uint64, target TargetSelection, mode string, confirmed, shared bool) (Status, error) {
	if err := validateSelection(target, mode, confirmed); err != nil {
		return Status{}, err
	}
	if revision == 0 {
		return Status{}, failure("invalid_control")
	}
	var foreground ForegroundProvider
	if mode == "foreground" {
		var ok bool
		foreground, ok = m.provider.(ForegroundProvider)
		if supported, _ := m.support(); !ok || !supported {
			return Status{}, failure("unsupported")
		}
	}
	if target.Kind == "desktop" || target.MonitorID != "" {
		if _, ok := m.provider.(TargetProvider); !ok {
			return Status{}, failure("target_selection_unsupported")
		}
	}
	m.mu.Lock()
	entry, err := m.sessionLocked(taskID, sessionID, revision)
	if err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	if entry.Switching || m.pendingCancel[taskID] != nil {
		m.mu.Unlock()
		return Status{}, failure("switching")
	}
	if mode == "foreground" {
		for otherTask, other := range m.sessions {
			if otherTask != taskID && other.Mode == "foreground" {
				m.mu.Unlock()
				return Status{}, failure("foreground_in_use")
			}
		}
		for otherTask := range m.pendingForeground {
			if otherTask != taskID {
				m.mu.Unlock()
				return Status{}, failure("foreground_in_use")
			}
		}
		if m.runningForeground != "" && m.runningForeground != entry.operationID {
			m.mu.Unlock()
			return Status{}, failure("foreground_in_use")
		}
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	opCtx, cancel := context.WithDeadline(ctx, minTime(m.now().Add(operationTTL), entry.expires))
	reservation := token()
	m.pendingCancel[taskID] = cancel
	if mode == "foreground" || entry.Mode == "foreground" {
		m.pendingForeground[taskID] = reservation
	}
	revokeOperation(entry)
	entry.Switching = true
	entry.Controller = "owner"
	entry.Revision++
	m.mu.Unlock()
	committed := false
	defer func() {
		cancel()
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.pendingCancel, taskID)
		if m.pendingForeground[taskID] == reservation {
			delete(m.pendingForeground, taskID)
		}
		if !committed && m.sessions[taskID] == entry {
			revokeOperation(entry)
			delete(m.sessions, taskID)
		}
	}()

	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		m.mu.Lock()
		current, err := m.sessionLocked(taskID, sessionID, 0)
		idle := current == entry && entry.operationID == "" && entry.captureID == ""
		m.mu.Unlock()
		if err != nil || current != entry {
			return Status{}, failure("not_shared")
		}
		if err := opCtx.Err(); err != nil {
			return Status{}, err
		}
		if idle {
			break
		}
		select {
		case <-opCtx.Done():
			return Status{}, opCtx.Err()
		case <-tick.C:
		}
	}
	window, err := m.resolveSelection(opCtx, selectionWindowID(target), mode, foreground, &target)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := opCtx.Err(); err != nil {
		return Status{}, err
	}
	if m.closed.Load() || m.sessions[taskID] != entry {
		return Status{}, failure("not_shared")
	}
	if m.running[window.ID] != "" || m.capturing[window.ID] != "" {
		return Status{}, failure("busy")
	}
	for otherTask, other := range m.sessions {
		if otherTask != taskID && other.Window.ID == window.ID {
			return Status{}, failure("window_in_use")
		}
	}
	expires := m.now().Add(sessionTTL)
	controller := "owner"
	if shared {
		controller = "shared"
	}
	m.sessions[taskID] = &sharedSession{
		Session: Session{ID: token(), TaskID: taskID, Window: window, Mode: mode, Controller: controller, Revision: 1,
			ExpiresAt: expires.UTC().Format(time.RFC3339)},
		expires: expires, observed: make(map[string][]observed), replayed: make(map[string]time.Time),
	}
	committed = true
	return m.statusLocked(taskID), nil
}

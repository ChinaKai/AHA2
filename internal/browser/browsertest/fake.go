// Package browsertest provides an in-memory Host for tests. It never launches a
// browser; it enforces the same single-session contract as the real host.
package browsertest

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/browser"
)

type session struct {
	id        string
	profileID string
	state     string
	tabs      map[string]browser.HostTab
}

type FakeHost struct {
	mu                sync.Mutex
	HostID            string
	EdgeAvailable     bool
	Unavailable       bool
	Locked            map[string]bool
	FailOpen          error
	FailOpenTab       error
	FailListTabs      error
	OpenState         string
	OpenError         string
	OpenTabStarted    chan struct{}
	OpenTabRelease    chan struct{}
	NavigateStarted   chan struct{}
	NavigateRelease   chan struct{}
	CloseTabStarted   chan struct{}
	CloseTabRelease   chan struct{}
	CloseTabGateTabID string
	CloseTabCalls     int
	FailCloseTab      error
	OpenCalls         int
	CloseCalls        int
	Fences            map[string]string
	FrameCalls        int
	ActivateCalls     int
	BookmarkCalls     int
	FailActivate      error
	FailBookmarks     error
	BookmarkList      []browser.HostBookmark
	Sessions          map[string]*session
	nextID            int
	Now               func() time.Time
}

func New() *FakeHost {
	return &FakeHost{
		HostID: "local", EdgeAvailable: true, Locked: map[string]bool{}, Sessions: map[string]*session{},
		Fences: map[string]string{}, Now: func() time.Time { return time.Now().UTC() },
	}
}

func (f *FakeHost) unavailable() error {
	return &browser.Error{Code: "browser_host_unavailable", Message: "fake host is down", Status: http.StatusServiceUnavailable}
}

func (f *FakeHost) fenceLocked(profileID, fence string) error {
	if current := f.Fences[profileID]; current != "" && fence != current {
		return &browser.Error{Code: "browser_fence_stale", Message: "lease fence changed", Status: http.StatusConflict}
	}
	return nil
}

func (f *FakeHost) SetFence(_ context.Context, profileID, sessionID, expectedFence, fence string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.Sessions[profileID]
	if !ok {
		return &browser.Error{Code: "browser_session_not_found", Message: "no session", Status: http.StatusNotFound}
	}
	if sessionID != "" && sessionID != value.id {
		return &browser.Error{Code: "browser_session_changed", Message: "session id mismatch", Status: http.StatusConflict}
	}
	if f.Fences[profileID] != expectedFence {
		return &browser.Error{Code: "browser_fence_stale", Message: "lease fence changed", Status: http.StatusConflict}
	}
	f.Fences[profileID] = fence
	return nil
}

func (f *FakeHost) healthLocked() browser.Health {
	return browser.Health{HostID: f.HostID, Version: "fake-1", Platform: "test", EdgeAvailable: f.EdgeAvailable}
}

func (f *FakeHost) Health(context.Context) (browser.Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Unavailable {
		return browser.Health{}, f.unavailable()
	}
	return f.healthLocked(), nil
}

func (f *FakeHost) Open(_ context.Context, profileID, browserKind string) (browser.HostSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.OpenCalls++
	if f.Unavailable {
		return browser.HostSession{}, f.unavailable()
	}
	if browserKind != "edge" {
		return browser.HostSession{}, &browser.Error{Code: "browser_kind_unsupported", Message: "only edge", Status: http.StatusBadRequest}
	}
	if !f.EdgeAvailable {
		return browser.HostSession{}, &browser.Error{Code: "browser_edge_unavailable", Message: "Edge is not installed", Status: http.StatusServiceUnavailable}
	}
	if f.FailOpen != nil {
		return browser.HostSession{}, f.FailOpen
	}
	if value, ok := f.Sessions[profileID]; ok {
		return browser.HostSession{ID: value.id, ProfileID: profileID, State: value.state, Fence: f.Fences[profileID], Reused: true}, nil
	}
	if f.Locked[profileID] {
		return browser.HostSession{}, &browser.Error{Code: "browser_profile_locked", Message: "profile directory is locked", Status: http.StatusConflict}
	}
	f.nextID++
	state := f.OpenState
	if state == "" {
		state = browserSessionReady
	}
	value := &session{
		id: fmt.Sprintf("host-session-%d", f.nextID), profileID: profileID,
		state: state, tabs: map[string]browser.HostTab{},
	}
	f.Sessions[profileID] = value
	return browser.HostSession{ID: value.id, ProfileID: profileID, State: state, Error: f.OpenError, Fence: f.Fences[profileID]}, nil
}

const browserSessionReady = "ready"

func (f *FakeHost) Session(_ context.Context, profileID string) (browser.HostSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Unavailable {
		return browser.HostSession{}, f.unavailable()
	}
	value, ok := f.Sessions[profileID]
	if !ok {
		return browser.HostSession{}, &browser.Error{Code: "browser_session_not_found", Message: "no session", Status: http.StatusNotFound}
	}
	return browser.HostSession{ID: value.id, ProfileID: profileID, State: value.state, Fence: f.Fences[profileID]}, nil
}

func (f *FakeHost) Close(_ context.Context, profileID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CloseCalls++
	if f.Unavailable {
		return f.unavailable()
	}
	value, ok := f.Sessions[profileID]
	if !ok {
		return &browser.Error{Code: "browser_session_not_found", Message: "no session", Status: http.StatusNotFound}
	}
	_ = value
	delete(f.Sessions, profileID)
	delete(f.Fences, profileID)
	return nil
}

func (f *FakeHost) sessionLocked(profileID, sessionID string) (*session, error) {
	if f.Unavailable {
		return nil, f.unavailable()
	}
	value, ok := f.Sessions[profileID]
	if !ok {
		return nil, &browser.Error{Code: "browser_session_not_found", Message: "no session", Status: http.StatusNotFound}
	}
	if sessionID != "" && sessionID != value.id {
		return nil, &browser.Error{Code: "browser_session_changed", Message: "session id mismatch", Status: http.StatusConflict}
	}
	return value, nil
}

func (f *FakeHost) OpenTab(_ context.Context, profileID, sessionID, targetURL, fence string) (browser.HostTab, error) {
	f.mu.Lock()
	if f.FailOpenTab != nil {
		err := f.FailOpenTab
		f.mu.Unlock()
		return browser.HostTab{}, err
	}
	value, err := f.sessionLocked(profileID, sessionID)
	started, release := f.OpenTabStarted, f.OpenTabRelease
	f.mu.Unlock()
	if err != nil {
		return browser.HostTab{}, err
	}
	// A deterministic hook for TOCTOU tests: the host call is in flight while
	// the test closes the session or revokes the grant. The captured session
	// still succeeds, so only AHA's post-call guard can prevent an orphan tab.
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fenceLocked(profileID, fence); err != nil {
		return browser.HostTab{}, err
	}
	f.nextID++
	for id, candidate := range value.tabs {
		candidate.Active = false
		value.tabs[id] = candidate
	}
	tab := browser.HostTab{ID: fmt.Sprintf("host-tab-%d", f.nextID), URL: targetURL, Title: "Tab", Active: true}
	value.tabs[tab.ID] = tab
	return tab, nil
}

// SetCurrentPages replaces the Owner-opened pages visible in a session. These
// pages have host tab ids but no AHA browser_tabs row until a Task binds them.
func (f *FakeHost) SetCurrentPages(profileID string, pages []browser.HostTab) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.Sessions[profileID]
	if !ok {
		return
	}
	// Owner pages are real host pages: snapshot/navigate/frame/close must work
	// on them exactly like on a tab AHA opened itself.
	for _, page := range pages {
		value.tabs[page.ID] = page
	}
}

func (f *FakeHost) ListTabs(_ context.Context, profileID, sessionID string) ([]browser.HostTab, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailListTabs != nil {
		return nil, f.FailListTabs
	}
	value, err := f.sessionLocked(profileID, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]browser.HostTab, 0, len(value.tabs))
	for _, tab := range value.tabs {
		result = append(result, tab)
	}
	return result, nil
}

func (f *FakeHost) Activate(_ context.Context, profileID, sessionID, tabID, fence string) (browser.HostTab, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailActivate != nil {
		return browser.HostTab{}, f.FailActivate
	}
	value, err := f.sessionLocked(profileID, sessionID)
	if err != nil {
		return browser.HostTab{}, err
	}
	if err := f.fenceLocked(profileID, fence); err != nil {
		return browser.HostTab{}, err
	}
	tab, ok := value.tabs[tabID]
	if !ok {
		return browser.HostTab{}, &browser.Error{Code: "browser_tab_not_found", Message: "no tab", Status: http.StatusNotFound}
	}
	f.ActivateCalls++
	for id, candidate := range value.tabs {
		candidate.Active = id == tabID
		value.tabs[id] = candidate
	}
	tab.Active = true
	return tab, nil
}

func (f *FakeHost) Bookmarks(_ context.Context, profileID, sessionID string) ([]browser.HostBookmark, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailBookmarks != nil {
		return nil, f.FailBookmarks
	}
	if _, err := f.sessionLocked(profileID, sessionID); err != nil {
		return nil, err
	}
	f.BookmarkCalls++
	return append([]browser.HostBookmark(nil), f.BookmarkList...), nil
}

func (f *FakeHost) Navigate(_ context.Context, profileID, sessionID, tabID, targetURL, fence string) (browser.HostTab, error) {
	f.mu.Lock()
	value, err := f.sessionLocked(profileID, sessionID)
	if err != nil {
		f.mu.Unlock()
		return browser.HostTab{}, err
	}
	if _, ok := value.tabs[tabID]; !ok {
		f.mu.Unlock()
		return browser.HostTab{}, &browser.Error{Code: "browser_tab_not_found", Message: "no tab", Status: http.StatusNotFound}
	}
	started, release := f.NavigateStarted, f.NavigateRelease
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fenceLocked(profileID, fence); err != nil {
		return browser.HostTab{}, err
	}
	tab, ok := value.tabs[tabID]
	if !ok {
		return browser.HostTab{}, &browser.Error{Code: "browser_tab_not_found", Message: "no tab", Status: http.StatusNotFound}
	}
	tab.URL = targetURL
	tab.Title = "Tab"
	value.tabs[tabID] = tab
	return tab, nil
}

func (f *FakeHost) Snapshot(_ context.Context, profileID, sessionID, tabID string) (browser.HostSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, err := f.sessionLocked(profileID, sessionID)
	if err != nil {
		return browser.HostSnapshot{}, err
	}
	tab, ok := value.tabs[tabID]
	if !ok {
		return browser.HostSnapshot{}, &browser.Error{Code: "browser_tab_not_found", Message: "no tab", Status: http.StatusNotFound}
	}
	return browser.HostSnapshot{URL: tab.URL, Title: tab.Title, Text: "page text"}, nil
}

func (f *FakeHost) Frame(_ context.Context, profileID, sessionID, tabID string) (browser.HostFrame, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, err := f.sessionLocked(profileID, sessionID)
	if err != nil {
		return browser.HostFrame{}, err
	}
	if _, ok := value.tabs[tabID]; !ok {
		return browser.HostFrame{}, &browser.Error{Code: "browser_tab_not_found", Message: "no tab", Status: http.StatusNotFound}
	}
	f.FrameCalls++
	return browser.HostFrame{ContentType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff, 0xd9}}, nil
}

func (f *FakeHost) CloseTab(_ context.Context, profileID, sessionID, tabID, fence string) error {
	f.mu.Lock()
	value, err := f.sessionLocked(profileID, sessionID)
	if err != nil {
		f.mu.Unlock()
		return err
	}
	if _, ok := value.tabs[tabID]; !ok {
		f.mu.Unlock()
		return &browser.Error{Code: "browser_tab_not_found", Message: "no tab", Status: http.StatusNotFound}
	}
	f.CloseTabCalls++
	gated := f.CloseTabGateTabID == "" || f.CloseTabGateTabID == tabID
	started, release, failure := f.CloseTabStarted, f.CloseTabRelease, f.FailCloseTab
	f.mu.Unlock()
	if gated && started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if gated && release != nil {
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if failure != nil {
		return failure
	}
	if err := f.fenceLocked(profileID, fence); err != nil {
		return err
	}
	delete(value.tabs, tabID)
	return nil
}

// LockProfile simulates another browser process holding the user-data
// directory before the host has a session.
func (f *FakeHost) LockProfile(profileID string, locked bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Locked[profileID] = locked
}

// MarkSessionClosed makes the host report a closed window without deleting the
// record, exercising the AHA reconciliation path.
func (f *FakeHost) MarkSessionClosed(profileID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.Sessions[profileID]; ok {
		value.state = "closed"
	}
}

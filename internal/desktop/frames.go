package desktop

import (
	"slices"
	"strings"
	"time"
)

func ValidViewID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// Actor authority comes from the authenticated handler. Client view IDs only
// partition that actor's observations; they never select a different actor.
func ViewActor(actor, viewID string) (string, error) {
	if viewID == "" {
		return actor, nil
	}
	if actor != "owner" || !ValidViewID(viewID) {
		return "", failure("invalid_view")
	}
	return "owner:" + viewID, nil
}

func ownerActor(actor string) bool {
	return actor == "owner" || strings.HasPrefix(actor, "owner:") && ValidViewID(strings.TrimPrefix(actor, "owner:"))
}

func NormalizeFrameOptions(o FrameOptions) (FrameOptions, error) {
	if o.MaxWidth == 0 {
		o.MaxWidth = 1280
	}
	if o.MaxHeight == 0 {
		o.MaxHeight = 720
	}
	if o.Quality == 0 {
		o.Quality = 65
	}
	if o.MaxWidth < 320 || o.MaxWidth > 1920 || o.MaxHeight < 180 || o.MaxHeight > 1080 || o.Quality < 35 || o.Quality > 85 {
		return FrameOptions{}, failure("invalid_preview")
	}
	return o, nil
}

func (m *Manager) pruneFrames(entry *sharedSession) {
	now := m.now()
	for actor, frames := range entry.observed {
		frames = slices.DeleteFunc(frames, func(o observed) bool { return now.Sub(o.at) >= observationTTL })
		if len(frames) == 0 {
			delete(entry.observed, actor)
		} else {
			entry.observed[actor] = frames
		}
	}
}

func findSnapshot(frames []observed, id string) (observed, bool) {
	for _, frame := range frames {
		if frame.id == id {
			return frame, true
		}
	}
	return observed{}, false
}

func ownerViewCount(entry *sharedSession) int {
	count := 0
	for actor := range entry.observed {
		if ownerActor(actor) {
			count++
		}
	}
	for actor := range entry.streams {
		if _, exists := entry.observed[actor]; !exists {
			count++
		}
	}
	return count
}

func (m *Manager) RegisterStream(taskID, sessionID, actor string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, err := m.sessionLocked(taskID, sessionID, 0)
	if err != nil {
		return nil, err
	}
	if entry.Switching {
		return nil, failure("switching")
	}
	if !strings.HasPrefix(actor, "owner:") || !ownerActor(actor) {
		return nil, failure("invalid_view")
	}
	m.pruneFrames(entry)
	if entry.streams[actor] != "" {
		return nil, failure("view_in_use")
	}
	if _, exists := entry.observed[actor]; !exists && ownerViewCount(entry) >= 8 {
		return nil, failure("view_limit")
	}
	if entry.streams == nil {
		entry.streams = make(map[string]string)
	}
	id := token()
	entry.streams[actor] = id
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if current := m.sessions[taskID]; current == entry && entry.streams[actor] == id {
			delete(entry.streams, actor)
			// HTTP fallback may already have published a fresh frame while this
			// socket's close notification was in transit. Only revoke our frames.
			frames := slices.DeleteFunc(entry.observed[actor], func(o observed) bool { return o.stream == id })
			if len(frames) == 0 {
				delete(entry.observed, actor)
			} else {
				entry.observed[actor] = frames
			}
		}
	}, nil
}

// DropView revokes only the disconnected viewer's frame metadata. Replay IDs
// survive disconnection so reconnecting cannot repeat an already sent action.
func (m *Manager) DropView(taskID, sessionID, actor string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry := m.sessions[taskID]; entry != nil && entry.ID == sessionID {
		delete(entry.observed, actor)
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

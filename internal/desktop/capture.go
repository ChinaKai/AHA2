package desktop

import (
	"context"
	"strings"
)

type captureOperation struct {
	ctx             context.Context
	window          Window
	id              string
	revision, epoch uint64
	foreground      bool
	streamID        string
	close           func()
}

func (m *Manager) observationEntryLocked(taskID, sessionID, actor string, streaming bool) (*sharedSession, string, error) {
	entry, err := m.sessionLocked(taskID, sessionID, 0)
	if err != nil {
		return nil, "", err
	}
	if entry.Switching {
		return nil, "", failure("switching")
	}
	isAgent := strings.HasPrefix(actor, "agent:") && len(actor) > len("agent:")
	if !ownerActor(actor) && !isAgent {
		return nil, "", failure("invalid_actor")
	}
	if isAgent && entry.Controller != "agent" && entry.Controller != "shared" {
		return nil, "", failure("owner_control")
	}
	streamID := ""
	if streaming {
		streamID = entry.streams[actor]
		if streamID == "" || !ownerActor(actor) {
			return nil, "", failure("stream_closed")
		}
	}
	m.pruneFrames(entry)
	if _, exists := entry.observed[actor]; !exists && entry.streams[actor] == "" && ownerActor(actor) && ownerViewCount(entry) >= 8 {
		return nil, "", failure("view_limit")
	}
	if entry.operationID != "" {
		return nil, "", failure("busy")
	}
	return entry, streamID, nil
}

func (m *Manager) startCapture(ctx context.Context, taskID, sessionID, actor string, streaming bool) (*captureOperation, error) {
	m.mu.Lock()
	entry, streamID, err := m.observationEntryLocked(taskID, sessionID, actor, streaming)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if len(entry.captureWaiters) >= 16 {
		m.mu.Unlock()
		return nil, failure("busy")
	}
	opCtx, cancel := context.WithDeadline(ctx, minTime(m.now().Add(operationTTL), entry.expires))
	op := &captureOperation{ctx: opCtx, window: entry.Window, id: token(), revision: entry.Revision, streamID: streamID}
	if entry.captureWaiters == nil {
		entry.captureWaiters = make(map[string]context.CancelFunc)
	}
	entry.captureWaiters[op.id] = cancel
	m.mu.Unlock()
	cleanup := func() {
		cancel()
		m.mu.Lock()
		delete(entry.captureWaiters, op.id)
		m.mu.Unlock()
	}
	release, err := m.captureQueue.acquire(opCtx, !streaming)
	if err != nil {
		cleanup()
		return nil, err
	}
	op.close = func() { cleanup(); release() }

	// Scope may have changed while waiting. Revalidate before starting native work,
	// and never carry a queued request into a replacement session or view.
	m.mu.Lock()
	current, currentStream, err := m.observationEntryLocked(taskID, sessionID, actor, streaming)
	if err == nil && (current != entry || current.Revision != op.revision) {
		err = failure("stale_session")
	}
	if err == nil && currentStream != streamID {
		err = failure("stream_closed")
	}
	if err == nil {
		err = opCtx.Err()
	}
	if err == nil && entry.captureID != "" {
		err = failure("busy")
	}
	if err != nil {
		m.mu.Unlock()
		op.close()
		return nil, err
	}
	delete(entry.captureWaiters, op.id)
	op.epoch, op.foreground = entry.actionEpoch, entry.Mode == "foreground"
	entry.captureID, entry.captureCancel = op.id, cancel
	m.capturing[entry.Window.ID] = op.id
	m.mu.Unlock()
	return op, nil
}

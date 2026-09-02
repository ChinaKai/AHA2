package app

import (
	"sync"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type EventHub struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan domain.Event]struct{}
	all         map[chan domain.Event]struct{}
}

func NewEventHub() *EventHub {
	return &EventHub{subscribers: map[string]map[chan domain.Event]struct{}{}, all: map[chan domain.Event]struct{}{}}
}

func (h *EventHub) Subscribe(taskID string) (<-chan domain.Event, func()) {
	channel := make(chan domain.Event, 64)
	h.mu.Lock()
	if h.subscribers[taskID] == nil {
		h.subscribers[taskID] = map[chan domain.Event]struct{}{}
	}
	h.subscribers[taskID][channel] = struct{}{}
	h.mu.Unlock()
	return channel, func() {
		h.mu.Lock()
		if _, ok := h.subscribers[taskID][channel]; ok {
			delete(h.subscribers[taskID], channel)
			close(channel)
		}
		if len(h.subscribers[taskID]) == 0 {
			delete(h.subscribers, taskID)
		}
		h.mu.Unlock()
	}
}

// SubscribeAll registers a channel that receives every published event,
// regardless of task. Used by the global list-refresh stream.
func (h *EventHub) SubscribeAll() (<-chan domain.Event, func()) {
	channel := make(chan domain.Event, 256)
	h.mu.Lock()
	h.all[channel] = struct{}{}
	h.mu.Unlock()
	return channel, func() {
		h.mu.Lock()
		if _, ok := h.all[channel]; ok {
			delete(h.all, channel)
			close(channel)
		}
		h.mu.Unlock()
	}
}

func (h *EventHub) Publish(taskID string, event domain.Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for channel := range h.subscribers[taskID] {
		select {
		case channel <- event:
		default:
		}
	}
	for channel := range h.all {
		select {
		case channel <- event:
		default:
		}
	}
}

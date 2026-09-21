package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var ErrConflict = errors.New("sync conflict")

type ConflictError struct {
	LocalPayload json.RawMessage
	LocalVersion string
	Cause        error
}

func (e *ConflictError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return ErrConflict.Error()
}
func (e *ConflictError) Unwrap() error { return ErrConflict }

type dependencyError struct{ cause error }

func (e *dependencyError) Error() string { return "sync dependency is not ready: " + e.cause.Error() }
func (e *dependencyError) Unwrap() []error {
	return []error{e.cause}
}

func waitForDependency(err error) error { return &dependencyError{cause: err} }

type Repository interface {
	SyncState(context.Context, string) (domain.SyncState, error)
	UpdateSyncState(context.Context, domain.SyncState) error
	PendingSync(context.Context, string, int, time.Time) ([]domain.SyncOutboxItem, error)
	AckSync(context.Context, string, []string, time.Time) error
	FailSync(context.Context, string, string, time.Time) error
	SyncWasApplied(context.Context, string) (bool, error)
	MarkSyncApplied(context.Context, string, domain.SyncObject, time.Time) error
	AddSyncConflict(context.Context, domain.SyncConflict) error
}
type Transport interface {
	Push(context.Context, PushRequest) (PushResponse, error)
	Pull(context.Context, string, string, string, int) (PullResponse, error)
}
type ApplyFunc func(context.Context, domain.SyncObject) error

type Engine struct {
	Store     Repository
	Remote    Transport
	Scope     string
	DeviceID  string
	BatchSize int
	Now       func() time.Time
	handlers  map[string]ApplyFunc
}

func (e *Engine) Register(objectType string, handler ApplyFunc) {
	if e.handlers == nil {
		e.handlers = map[string]ApplyFunc{}
	}
	e.handlers[objectType] = handler
}
func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}
func (e *Engine) limit() int {
	if e.BatchSize < 1 || e.BatchSize > 500 {
		return 100
	}
	return e.BatchSize
}

func (e *Engine) Push(ctx context.Context) error {
	items, err := e.Store.PendingSync(ctx, e.Scope, e.limit(), e.now())
	if err != nil || len(items) == 0 {
		return err
	}
	objects := make([]domain.SyncObject, len(items))
	byKey := map[string]string{}
	for i, item := range items {
		objects[i] = item.Object
		byKey[item.Object.IdempotencyKey] = item.ID
	}
	response, err := e.Remote.Push(ctx, PushRequest{Scope: e.Scope, DeviceID: e.DeviceID, Objects: objects})
	if err != nil {
		retry := e.now().Add(time.Minute)
		for _, item := range items {
			_ = e.Store.FailSync(ctx, item.ID, err.Error(), retry)
		}
		return err
	}
	ids := make([]string, 0, len(response.AckedKeys))
	for _, key := range response.AckedKeys {
		if id, ok := byKey[key]; ok {
			ids = append(ids, id)
			for _, object := range objects {
				if object.IdempotencyKey == key {
					if err := e.Store.MarkSyncApplied(ctx, e.Scope, object, e.now()); err != nil {
						return err
					}
					break
				}
			}
		}
	}
	for _, conflict := range response.Conflicts {
		if conflict.Scope == "" {
			conflict.Scope = e.Scope
		}
		if err := e.Store.AddSyncConflict(ctx, conflict); err != nil {
			return err
		}
	}
	return e.Store.AckSync(ctx, e.Scope, ids, e.now())
}

func (e *Engine) Pull(ctx context.Context) error {
	state, err := e.Store.SyncState(ctx, e.Scope)
	if err != nil {
		return err
	}
	cursor := state.Cursor
	pending := []domain.SyncObject{}
	seenCursors := map[string]bool{cursor: true}
	for {
		response, err := e.Remote.Pull(ctx, e.Scope, cursor, e.DeviceID, e.limit())
		if err != nil {
			return err
		}
		if response.HasMore && seenCursors[response.Cursor] {
			return fmt.Errorf("remote sync cursor did not advance")
		}
		cursor = response.Cursor
		seenCursors[cursor] = true
		pending = append(pending, response.Objects...)
		var dependencyErr error
		pending, dependencyErr, err = e.applyPulledObjects(ctx, pending)
		if err != nil {
			return err
		}
		if !response.HasMore {
			// The center has nothing further to offer, so a dependency still
			// waiting here can never be satisfied by a later object: this client
			// has now seen the entire backlog. Holding the cursor for it would be
			// permanent -- the same page gets re-pulled, the same object blocks
			// it, and everything behind it stays unreachable for as long as the
			// remote keeps offering it.
			pending = e.quarantineUnresolvable(ctx, pending)
		}
		if len(pending) == 0 {
			state.Cursor = cursor
			state.LastPullAt = e.now()
			state.LastError = ""
			state.UpdatedAt = e.now()
			if err := e.Store.UpdateSyncState(ctx, state); err != nil {
				return err
			}
		} else if !response.HasMore {
			return dependencyErr
		}
		if !response.HasMore {
			return nil
		}
	}
}

// quarantineUnresolvable records objects that could not be applied even after
// the whole backlog was read, and reports them as consumed.
//
// Skipping is deliberate: the alternative is that one unusable record -- a
// reference to something deleted on every device, a payload this build cannot
// parse -- freezes inbound sync indefinitely, which is a far worse outcome than
// one object not arriving. The record is kept as an open conflict so the
// operator can see what was left behind rather than losing it silently.
func (e *Engine) quarantineUnresolvable(ctx context.Context, pending []domain.SyncObject) []domain.SyncObject {
	if len(pending) == 0 {
		return pending
	}
	remaining := make([]domain.SyncObject, 0, len(pending))
	for _, object := range pending {
		if err := e.quarantineObject(ctx, object); err != nil {
			remaining = append(remaining, object)
		}
	}
	return remaining
}

// quarantineObject records one unappliable object as an open conflict and marks
// its delivery consumed, so the same broken record cannot block every later run.
func (e *Engine) quarantineObject(ctx context.Context, object domain.SyncObject) error {
	if err := e.Store.AddSyncConflict(ctx, domain.SyncConflict{
		Scope: e.Scope, ObjectType: object.Type, ObjectID: object.ID,
		RemotePayload: object.Payload, RemoteVersion: object.RemoteVersion,
	}); err != nil {
		// Without the record there would be no trace of what was skipped, which
		// is worse than leaving it for the operator to see failing.
		return err
	}
	delivered := object
	delivered.IdempotencyKey = e.deliveryKey(object)
	delivered.EventID = ""
	return e.Store.MarkSyncApplied(ctx, e.Scope, delivered, e.now())
}

// deliveryKey is the identity an object is recorded under once it is applied.
// It prefers the center's event id, which is what makes a re-delivered object
// recognizable as already applied.
func (e *Engine) deliveryKey(object domain.SyncObject) string {
	if object.EventID != "" {
		return object.EventID
	}
	if object.RemoteVersion != "" {
		return fmt.Sprintf("center-object:%s:%s:%s", object.Type, object.ID, object.RemoteVersion)
	}
	return object.IdempotencyKey
}

// applyPulledObjects applies what it can and reports what is left waiting.
//
// The third result is a fatal error, the second a dependency that may still be
// satisfied by a later object. Only a dependency is ever returned as waiting:
// a handler that fails for its own reason must not be retried on every run
// behind a cursor that never advances, so it is recorded and skipped rather
// than left to block everything behind it.
func (e *Engine) applyPulledObjects(ctx context.Context, pending []domain.SyncObject) ([]domain.SyncObject, error, error) {
	for len(pending) > 0 {
		blocked := map[string]bool{}
		next := make([]domain.SyncObject, 0, len(pending))
		progress := false
		var dependencyErr error
		for _, object := range pending {
			objectKey := object.Type + "\x00" + object.ID
			if blocked[objectKey] {
				next = append(next, object)
				continue
			}
			err := e.applyPulledObject(ctx, object)
			var waiting *dependencyError
			if errors.As(err, &waiting) {
				blocked[objectKey] = true
				next = append(next, object)
				if dependencyErr == nil {
					dependencyErr = err
				}
				continue
			}
			if err != nil {
				if quarantineErr := e.quarantineObject(ctx, object); quarantineErr != nil {
					return nil, nil, err
				}
				continue
			}
			progress = true
		}
		if len(next) == 0 {
			return nil, nil, nil
		}
		if !progress {
			return next, dependencyErr, nil
		}
		pending = next
	}
	return nil, nil, nil
}

func (e *Engine) applyPulledObject(ctx context.Context, object domain.SyncObject) error {
	if object.IdempotencyKey == "" {
		return fmt.Errorf("remote %s/%s has no idempotency key", object.Type, object.ID)
	}
	deliveryKey := e.deliveryKey(object)
	applied, err := e.Store.SyncWasApplied(ctx, deliveryKey)
	if err != nil {
		return err
	}
	if applied {
		return nil
	}
	handler := e.handlers[object.Type]
	if handler == nil {
		return fmt.Errorf("no sync handler for object type %q", object.Type)
	}
	if err := handler(ctx, object); err != nil {
		var conflict *ConflictError
		if errors.As(err, &conflict) {
			if addErr := e.Store.AddSyncConflict(ctx, domain.SyncConflict{Scope: e.Scope, ObjectType: object.Type, ObjectID: object.ID, LocalPayload: conflict.LocalPayload, RemotePayload: object.Payload, LocalVersion: conflict.LocalVersion, RemoteVersion: object.RemoteVersion}); addErr != nil {
				return addErr
			}
			if deliveryKey != object.IdempotencyKey {
				delivered := object
				delivered.IdempotencyKey = deliveryKey
				delivered.EventID = ""
				if markErr := e.Store.MarkSyncApplied(ctx, e.Scope, delivered, e.now()); markErr != nil {
					return markErr
				}
			}
			return nil
		}
		return err
	}
	if deliveryKey != object.IdempotencyKey {
		if err := e.Store.MarkSyncApplied(ctx, e.Scope, object, e.now()); err != nil {
			return err
		}
	}
	delivered := object
	delivered.IdempotencyKey = deliveryKey
	delivered.EventID = ""
	if err := e.Store.MarkSyncApplied(ctx, e.Scope, delivered, e.now()); err != nil {
		return err
	}
	return nil
}

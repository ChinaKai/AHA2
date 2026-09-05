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
	for {
		response, err := e.Remote.Pull(ctx, e.Scope, state.Cursor, e.DeviceID, e.limit())
		if err != nil {
			return err
		}
		for _, object := range response.Objects {
			if object.IdempotencyKey == "" {
				return fmt.Errorf("remote %s/%s has no idempotency key", object.Type, object.ID)
			}
			applied, err := e.Store.SyncWasApplied(ctx, object.IdempotencyKey)
			if err != nil {
				return err
			}
			if applied {
				continue
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
					continue
				}
				return err
			}
			if err := e.Store.MarkSyncApplied(ctx, e.Scope, object, e.now()); err != nil {
				return err
			}
		}
		state.Cursor = response.Cursor
		state.LastPullAt = e.now()
		state.LastError = ""
		state.UpdatedAt = e.now()
		if err := e.Store.UpdateSyncState(ctx, state); err != nil {
			return err
		}
		if !response.HasMore {
			return nil
		}
	}
}

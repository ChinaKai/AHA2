package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

var ErrPetStateConflict = errors.New("pet state version conflict")

func (s *Store) EnsurePetState(ctx context.Context, now time.Time) (domain.PetState, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO pet_states(id,mood,action,zone,x,y,facing,sleeping,quiet,version,updated_at)
		VALUES('owner_pet','neutral','idle','dock',0.82,0.78,1,0,0,1,?)`, timeString(now))
	if err != nil {
		return domain.PetState{}, err
	}
	return s.PetState(ctx)
}

func (s *Store) PetState(ctx context.Context) (domain.PetState, error) {
	var item domain.PetState
	var updated string
	var sleeping, quiet int
	err := s.db.QueryRowContext(ctx, `
		SELECT id,mood,action,zone,x,y,facing,sleeping,quiet,version,updated_at
		FROM pet_states WHERE id='owner_pet'`).
		Scan(&item.ID, &item.Mood, &item.Action, &item.Zone, &item.X, &item.Y, &item.Facing, &sleeping, &quiet, &item.Version, &updated)
	item.Sleeping, item.Quiet = sleeping != 0, quiet != 0
	item.UpdatedAt = parseTime(updated)
	return item, err
}

func (s *Store) SavePetState(ctx context.Context, item domain.PetState, expectedVersion int64) (domain.PetState, error) {
	sleeping, quiet := 0, 0
	if item.Sleeping {
		sleeping = 1
	}
	if item.Quiet {
		quiet = 1
	}
	if expectedVersion <= 0 {
		expectedVersion = item.Version
	}
	var result sql.Result
	var err error
	if expectedVersion > 0 {
		result, err = s.db.ExecContext(ctx, `
			UPDATE pet_states SET mood=?,action=?,zone=?,x=?,y=?,facing=?,sleeping=?,quiet=?,version=version+1,updated_at=?
			WHERE id='owner_pet' AND version=?`,
			item.Mood, item.Action, item.Zone, item.X, item.Y, item.Facing, sleeping, quiet, timeString(item.UpdatedAt), expectedVersion)
	} else {
		result, err = s.db.ExecContext(ctx, `
			UPDATE pet_states SET mood=?,action=?,zone=?,x=?,y=?,facing=?,sleeping=?,quiet=?,version=version+1,updated_at=?
			WHERE id='owner_pet'`,
			item.Mood, item.Action, item.Zone, item.X, item.Y, item.Facing, sleeping, quiet, timeString(item.UpdatedAt))
	}
	if err != nil {
		return domain.PetState{}, err
	}
	if expectedVersion > 0 {
		changed, err := result.RowsAffected()
		if err != nil {
			return domain.PetState{}, err
		}
		if changed == 0 {
			current, currentErr := s.PetState(ctx)
			if currentErr != nil {
				return domain.PetState{}, currentErr
			}
			return current, ErrPetStateConflict
		}
	}
	return s.PetState(ctx)
}

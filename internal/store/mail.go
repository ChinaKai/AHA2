package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Mail timestamps use fixed-width UTC RFC3339 with nine fractional digits.
// Lease and availability checks compare these strings, so varying widths (as
// time.RFC3339Nano produces) would break same-second ordering.
const mailTimeLayout = "2006-01-02T15:04:05.000000000Z"

var (
	ErrMailAccountInUse            = errors.New("mail account has messages or bindings")
	ErrMailAddressInUse            = errors.New("mail address already in use")
	ErrMailTemplateVersionConflict = errors.New("mail template version conflict")
	ErrMailTemplateArchived        = errors.New("mail template is archived")
	ErrAttachmentInUse             = errors.New("attachment is referenced by a build artifact")
)

// isUniqueConstraintError and isForeignKeyConstraintError inspect the driver's
// typed result code instead of matching message text, which is localized and
// version-dependent.
func isUniqueConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	// SQLite reports a composite PRIMARY KEY collision separately from a
	// UNIQUE index collision; both mean "a competing version already exists".
	return sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE ||
		sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

func isForeignKeyConstraintError(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}

func mailTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(mailTimeLayout)
}

func parseMailTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(mailTimeLayout, value); err == nil {
		return parsed.UTC()
	}
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed.UTC()
}

// nullIfEmpty keeps optional foreign keys nullable; an empty string would point
// at a nonexistent row instead of meaning "unset".
func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func mailListLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

// mailDBTX is satisfied by both *sql.DB and *sql.Tx so the same conditional
// write can run standalone or inside a transaction composed by the caller.
type mailDBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// WithMailTx runs fn in one transaction. Approval and worker code use it to
// keep Mail Message and Mail Delivery state changes atomic; the InTx methods in
// this package accept the transaction it provides.
func (s *Store) WithMailTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if fn == nil {
		return fmt.Errorf("mail transaction callback is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

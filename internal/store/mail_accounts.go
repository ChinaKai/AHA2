package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// mailAccountColumns deliberately omits credential_ref. Normal Store results
// must never carry the Secret reference; use MailAccountCredentialRef when a
// worker or connection test needs it.
const mailAccountColumns = `id,name,address,display_name,provider,smtp_host,smtp_port,
	smtp_security,smtp_username,credential_configured,status,last_test_at,
	last_test_error_code,imap_host,imap_port,imap_enabled,created_at,updated_at`

func scanMailAccount(scanner interface{ Scan(...any) error }) (domain.MailAccount, error) {
	var item domain.MailAccount
	var configured, imapEnabled int
	var lastTestAt, createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.Name, &item.Address, &item.DisplayName, &item.Provider,
		&item.SMTPHost, &item.SMTPPort, &item.SMTPSecurity, &item.SMTPUsername,
		&configured, &item.Status, &lastTestAt, &item.LastTestErrorCode,
		&item.IMAPHost, &item.IMAPPort, &imapEnabled, &createdAt, &updatedAt,
	)
	item.CredentialConfigured = configured != 0
	item.IMAPEnabled = imapEnabled != 0
	item.LastTestAt = parseMailTime(lastTestAt)
	item.CreatedAt = parseMailTime(createdAt)
	item.UpdatedAt = parseMailTime(updatedAt)
	return item, err
}

func normalizeMailAccount(item domain.MailAccount, create bool) (domain.MailAccount, error) {
	item.ID = strings.TrimSpace(item.ID)
	item.Name = strings.TrimSpace(item.Name)
	item.Address = strings.TrimSpace(item.Address)
	item.DisplayName = strings.TrimSpace(item.DisplayName)
	item.Provider = strings.TrimSpace(item.Provider)
	item.SMTPHost = strings.TrimSpace(item.SMTPHost)
	item.SMTPSecurity = strings.TrimSpace(item.SMTPSecurity)
	item.SMTPUsername = strings.TrimSpace(item.SMTPUsername)
	item.LastTestErrorCode = strings.TrimSpace(item.LastTestErrorCode)
	item.IMAPHost = strings.TrimSpace(item.IMAPHost)
	item.CredentialRef = strings.TrimSpace(item.CredentialRef)
	if item.Address == "" {
		return item, fmt.Errorf("mail account address is required")
	}
	if create && item.ID == "" {
		item.ID = domain.NewID("mail_account")
	}
	if item.CredentialRef != "" {
		// The Secret key format is fixed by the design; never accept an
		// arbitrary reference that could point at another account's secret.
		item.CredentialRef = domain.MailAccountCredentialRef(item.ID)
	}
	if item.Provider == "" {
		item.Provider = domain.MailProviderNeteaseQiye
	}
	if item.SMTPHost == "" {
		item.SMTPHost = domain.MailDefaultSMTPHost
	}
	if item.SMTPPort <= 0 {
		item.SMTPPort = domain.MailDefaultSMTPPort
	}
	if item.SMTPSecurity == "" {
		item.SMTPSecurity = domain.MailSMTPSecuritySSL
	}
	if item.SMTPUsername == "" {
		item.SMTPUsername = item.Address
	}
	if item.Status == "" {
		item.Status = domain.MailAccountUnverified
	}
	if !item.Status.Valid() {
		return item, fmt.Errorf("invalid mail account status %q", item.Status)
	}
	if create {
		// A stored Secret reference is the only source of truth for the
		// write-only flag; a caller cannot mark an account configured without
		// one.
		item.CredentialConfigured = item.CredentialRef != ""
	}
	if item.IMAPPort < 0 {
		item.IMAPPort = 0
	}
	aliases := make([]domain.MailAccountAlias, 0, len(item.Aliases))
	seen := map[string]bool{}
	for _, alias := range item.Aliases {
		address := strings.TrimSpace(alias.Address)
		if address == "" {
			continue
		}
		key := strings.ToLower(address)
		if seen[key] {
			continue
		}
		if strings.EqualFold(address, item.Address) {
			return item, ErrMailAddressInUse
		}
		seen[key] = true
		aliases = append(aliases, domain.MailAccountAlias{
			AccountID:   item.ID,
			Address:     address,
			DisplayName: strings.TrimSpace(alias.DisplayName),
			Position:    len(aliases),
		})
	}
	item.Aliases = aliases
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	return item, nil
}

func mailAddressAvailableTx(ctx context.Context, tx *sql.Tx, accountID, address string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM mail_accounts WHERE lower(address)=lower(?) AND id<>?)
		    OR EXISTS(SELECT 1 FROM mail_account_aliases WHERE lower(address)=lower(?) AND account_id<>?)`,
		address, accountID, address, accountID).Scan(&exists)
	return !exists, err
}

func (s *Store) CreateMailAccount(ctx context.Context, item domain.MailAccount) (domain.MailAccount, error) {
	item, err := normalizeMailAccount(item, true)
	if err != nil {
		return domain.MailAccount{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MailAccount{}, err
	}
	defer tx.Rollback()
	if available, err := mailAddressAvailableTx(ctx, tx, item.ID, item.Address); err != nil {
		return domain.MailAccount{}, err
	} else if !available {
		return domain.MailAccount{}, ErrMailAddressInUse
	}
	for _, alias := range item.Aliases {
		if available, err := mailAddressAvailableTx(ctx, tx, item.ID, alias.Address); err != nil {
			return domain.MailAccount{}, err
		} else if !available {
			return domain.MailAccount{}, ErrMailAddressInUse
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mail_accounts(
			id,name,address,display_name,provider,smtp_host,smtp_port,smtp_security,
			smtp_username,credential_ref,credential_configured,status,last_test_at,
			last_test_error_code,imap_host,imap_port,imap_enabled,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.Name, item.Address, item.DisplayName, item.Provider,
		item.SMTPHost, item.SMTPPort, item.SMTPSecurity, item.SMTPUsername,
		item.CredentialRef, boolInt(item.CredentialConfigured), item.Status,
		mailTimeString(item.LastTestAt), item.LastTestErrorCode, item.IMAPHost,
		item.IMAPPort, boolInt(item.IMAPEnabled), mailTimeString(item.CreatedAt),
		mailTimeString(item.UpdatedAt),
	); err != nil {
		return domain.MailAccount{}, err
	}
	if err := insertMailAccountAliasesTx(ctx, tx, item.Aliases); err != nil {
		return domain.MailAccount{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MailAccount{}, err
	}
	item.CredentialRef = ""
	return item, nil
}

func insertMailAccountAliasesTx(ctx context.Context, tx *sql.Tx, aliases []domain.MailAccountAlias) error {
	for position, alias := range aliases {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mail_account_aliases(account_id,address,display_name,position)
			VALUES(?,?,?,?)`, alias.AccountID, alias.Address, alias.DisplayName, position); err != nil {
			return err
		}
	}
	return nil
}

// UpdateMailAccount updates account settings and replaces the alias set. It
// never changes credential_ref or credential_configured; use
// SetMailAccountCredential for rotation.
func (s *Store) UpdateMailAccount(ctx context.Context, item domain.MailAccount) (domain.MailAccount, error) {
	if strings.TrimSpace(item.ID) == "" {
		return domain.MailAccount{}, fmt.Errorf("mail account id is required")
	}
	item, err := normalizeMailAccount(item, false)
	if err != nil {
		return domain.MailAccount{}, err
	}
	item.UpdatedAt = time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MailAccount{}, err
	}
	defer tx.Rollback()
	if available, err := mailAddressAvailableTx(ctx, tx, item.ID, item.Address); err != nil {
		return domain.MailAccount{}, err
	} else if !available {
		return domain.MailAccount{}, ErrMailAddressInUse
	}
	for _, alias := range item.Aliases {
		if available, err := mailAddressAvailableTx(ctx, tx, item.ID, alias.Address); err != nil {
			return domain.MailAccount{}, err
		} else if !available {
			return domain.MailAccount{}, ErrMailAddressInUse
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE mail_accounts SET
			name=?,address=?,display_name=?,provider=?,smtp_host=?,smtp_port=?,
			smtp_security=?,smtp_username=?,status=?,last_test_at=?,
			last_test_error_code=?,imap_host=?,imap_port=?,imap_enabled=?,updated_at=?
		WHERE id=?`,
		item.Name, item.Address, item.DisplayName, item.Provider, item.SMTPHost,
		item.SMTPPort, item.SMTPSecurity, item.SMTPUsername, item.Status,
		mailTimeString(item.LastTestAt), item.LastTestErrorCode, item.IMAPHost,
		item.IMAPPort, boolInt(item.IMAPEnabled), mailTimeString(item.UpdatedAt), item.ID,
	)
	if err != nil {
		return domain.MailAccount{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.MailAccount{}, sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mail_account_aliases WHERE account_id=?`, item.ID); err != nil {
		return domain.MailAccount{}, err
	}
	if err := insertMailAccountAliasesTx(ctx, tx, item.Aliases); err != nil {
		return domain.MailAccount{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MailAccount{}, err
	}
	// Re-read so CredentialConfigured and aliases come from the database rather
	// than from caller-supplied fields.
	return s.MailAccount(ctx, item.ID)
}

func (s *Store) MailAccount(ctx context.Context, id string) (domain.MailAccount, error) {
	item, err := scanMailAccount(s.db.QueryRowContext(ctx,
		`SELECT `+mailAccountColumns+` FROM mail_accounts WHERE id=?`, id))
	if err != nil {
		return domain.MailAccount{}, err
	}
	aliases, err := s.mailAccountAliases(ctx, id)
	if err != nil {
		return domain.MailAccount{}, err
	}
	item.Aliases = aliases
	return item, nil
}

func (s *Store) mailAccountAliases(ctx context.Context, accountID string) ([]domain.MailAccountAlias, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT account_id,address,display_name,position
		FROM mail_account_aliases WHERE account_id=? ORDER BY position,address`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailAccountAlias, 0)
	for rows.Next() {
		var alias domain.MailAccountAlias
		if err := rows.Scan(&alias.AccountID, &alias.Address, &alias.DisplayName, &alias.Position); err != nil {
			return nil, err
		}
		result = append(result, alias)
	}
	return result, rows.Err()
}

func (s *Store) MailAccounts(ctx context.Context, limit int) ([]domain.MailAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mailAccountColumns+`
		FROM mail_accounts ORDER BY created_at DESC,id DESC LIMIT ?`, mailListLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.MailAccount, 0)
	for rows.Next() {
		item, err := scanMailAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for index := range result {
		aliases, err := s.mailAccountAliases(ctx, result[index].ID)
		if err != nil {
			return nil, err
		}
		result[index].Aliases = aliases
	}
	return result, nil
}

// MailAccountCredentialRef returns the Secret Store key for the SMTP
// authorization code. Only connection test and the SMTP worker should call it.
func (s *Store) MailAccountCredentialRef(ctx context.Context, id string) (string, error) {
	var ref string
	if err := s.db.QueryRowContext(ctx, `SELECT credential_ref FROM mail_accounts WHERE id=?`, id).Scan(&ref); err != nil {
		return "", err
	}
	return ref, nil
}

// SetMailAccountCredential stores a rotated Secret reference, marks the
// credential configured, clears the last test result and requires a new test.
func (s *Store) SetMailAccountCredential(ctx context.Context, id string, now time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("mail account id is required")
	}
	ref := domain.MailAccountCredentialRef(id)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_accounts SET credential_ref=?,credential_configured=1,
			status='unverified',last_test_at='',last_test_error_code='',updated_at=?
		WHERE id=?`, ref, mailTimeString(now), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// SetMailAccountTestResult records the outcome of an Owner-triggered
// connection test. The error code is a stable SMTP code, never a transcript or
// credential.
func (s *Store) SetMailAccountTestResult(ctx context.Context, id string, status domain.MailAccountStatus, errorCode string, now time.Time) error {
	if !status.Valid() {
		return fmt.Errorf("invalid mail account status %q", status)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE mail_accounts SET status=?,last_test_at=?,last_test_error_code=?,updated_at=?
		WHERE id=?`,
		status, mailTimeString(now), strings.TrimSpace(errorCode), mailTimeString(now), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SetMailAccountStatus(ctx context.Context, id string, status domain.MailAccountStatus, now time.Time) error {
	if !status.Valid() {
		return fmt.Errorf("invalid mail account status %q", status)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE mail_accounts SET status=?,updated_at=? WHERE id=?`,
		status, mailTimeString(now), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DisableMailAccount(ctx context.Context, id string, now time.Time) error {
	return s.SetMailAccountStatus(ctx, id, domain.MailAccountDisabled, now)
}

// DeleteMailAccount physically deletes only accounts that never produced a
// message and are not bound anywhere. Otherwise it returns ErrMailAccountInUse
// so history and audit survive. The caller should disable/archive instead.
func (s *Store) DeleteMailAccount(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var inUse bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM mail_messages WHERE account_id=?)
		    OR EXISTS(SELECT 1 FROM mail_bindings WHERE account_id=?)`, id, id).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return ErrMailAccountInUse
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM mail_accounts WHERE id=?`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

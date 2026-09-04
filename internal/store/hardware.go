package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) HardwareGroups(ctx context.Context, taskID string) ([]domain.HardwareGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id,id,position,description,mode,serial_device,serial_baudrate,
		       network_host,network_port,network_protocol,ssh_auth,username,credential_ref,
		       password_configured,access,created_at,updated_at
		FROM hardware_groups WHERE task_id=? ORDER BY position,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.HardwareGroup, 0)
	for rows.Next() {
		item, err := scanHardwareGroup(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) HardwareGroup(ctx context.Context, taskID, hardwareID string) (domain.HardwareGroup, error) {
	return scanHardwareGroup(s.db.QueryRowContext(ctx, `
		SELECT task_id,id,position,description,mode,serial_device,serial_baudrate,
		       network_host,network_port,network_protocol,ssh_auth,username,credential_ref,
		       password_configured,access,created_at,updated_at
		FROM hardware_groups WHERE task_id=? AND id=?`, taskID, hardwareID))
}

func scanHardwareGroup(scanner interface{ Scan(...any) error }) (domain.HardwareGroup, error) {
	var item domain.HardwareGroup
	var passwordConfigured int
	var createdAt, updatedAt string
	err := scanner.Scan(
		&item.TaskID, &item.ID, &item.Position, &item.Description, &item.Mode,
		&item.Serial.Device, &item.Serial.Baudrate,
		&item.Network.Host, &item.Network.Port, &item.Network.Protocol, &item.Network.SSHAuth,
		&item.Username, &item.CredentialRef, &passwordConfigured, &item.Access,
		&createdAt, &updatedAt,
	)
	item.PasswordConfigured = passwordConfigured != 0
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) ReplaceHardwareGroups(ctx context.Context, taskID string, groups []domain.HardwareGroup) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM hardware_groups WHERE task_id=?`, taskID); err != nil {
		return err
	}
	for _, item := range groups {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO hardware_groups(
				task_id,id,position,description,mode,serial_device,serial_baudrate,
				network_host,network_port,network_protocol,ssh_auth,username,credential_ref,
				password_configured,access,created_at,updated_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			taskID, item.ID, item.Position, item.Description, item.Mode,
			item.Serial.Device, item.Serial.Baudrate,
			item.Network.Host, item.Network.Port, item.Network.Protocol, item.Network.SSHAuth,
			item.Username, item.CredentialRef, boolInt(item.PasswordConfigured), item.Access,
			timeString(item.CreatedAt), timeString(item.UpdatedAt),
		); err != nil {
			return fmt.Errorf("insert hardware group %s: %w", item.ID, err)
		}
	}
	return tx.Commit()
}

func (s *Store) AppendHardwareIO(ctx context.Context, item domain.HardwareIOEvent) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO hardware_io(
			id,task_id,hardware_id,transport,direction,data,encoding,source,created_at
		) VALUES(?,?,?,?,?,?,?,?,?)`,
		item.ID, item.TaskID, item.HardwareID, item.Transport, item.Direction,
		item.Data, item.Encoding, item.Source, timeString(item.CreatedAt),
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) HardwareIOPage(
	ctx context.Context,
	taskID, hardwareID, transport string,
	after int64,
	limit int,
) (domain.HardwareIOPage, error) {
	if limit < 1 {
		limit = 500
	}
	if limit > 2000 {
		limit = 2000
	}
	var rows *sql.Rows
	var err error
	if after > 0 {
		rows, err = s.db.QueryContext(ctx, `
			SELECT sequence,id,task_id,hardware_id,transport,direction,data,encoding,source,created_at
			FROM hardware_io
			WHERE task_id=? AND hardware_id=? AND transport=? AND sequence>?
			ORDER BY sequence LIMIT ?`, taskID, hardwareID, transport, after, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT sequence,id,task_id,hardware_id,transport,direction,data,encoding,source,created_at
			FROM (
				SELECT sequence,id,task_id,hardware_id,transport,direction,data,encoding,source,created_at
				FROM hardware_io
				WHERE task_id=? AND hardware_id=? AND transport=?
				ORDER BY sequence DESC LIMIT ?
			) ORDER BY sequence`, taskID, hardwareID, transport, limit)
	}
	if err != nil {
		return domain.HardwareIOPage{}, err
	}
	defer rows.Close()
	page := domain.HardwareIOPage{Items: make([]domain.HardwareIOEvent, 0)}
	for rows.Next() {
		var item domain.HardwareIOEvent
		var createdAt string
		if err := rows.Scan(
			&item.Sequence, &item.ID, &item.TaskID, &item.HardwareID, &item.Transport,
			&item.Direction, &item.Data, &item.Encoding, &item.Source, &createdAt,
		); err != nil {
			return domain.HardwareIOPage{}, err
		}
		item.CreatedAt = parseTime(createdAt)
		page.Items = append(page.Items, item)
		page.LatestSequence = item.Sequence
	}
	if err := rows.Err(); err != nil {
		return domain.HardwareIOPage{}, err
	}
	var maximum sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT MAX(sequence) FROM hardware_io
		WHERE task_id=? AND hardware_id=? AND transport=?`,
		taskID, hardwareID, transport,
	).Scan(&maximum); err != nil {
		return domain.HardwareIOPage{}, err
	}
	if maximum.Valid {
		if page.LatestSequence == 0 {
			page.LatestSequence = maximum.Int64
		}
		page.HasMore = maximum.Int64 > page.LatestSequence
	}
	return page, nil
}

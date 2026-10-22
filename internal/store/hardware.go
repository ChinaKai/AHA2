package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const hardwareResourceColumns = `id,description,mode,serial_device,serial_baudrate,
	network_host,network_port,network_protocol,ssh_auth,username,credential_ref,
	password_configured,adb_paired_at,access,created_at,updated_at`

func scanHardwareResource(scanner interface{ Scan(...any) error }) (domain.HardwareResource, error) {
	var item domain.HardwareResource
	var passwordConfigured int
	var createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.Description, &item.Mode, &item.Serial.Device, &item.Serial.Baudrate,
		&item.Network.Host, &item.Network.Port, &item.Network.Protocol, &item.Network.SSHAuth,
		&item.Username, &item.CredentialRef, &passwordConfigured, &item.ADBPairedAt, &item.Access, &createdAt, &updatedAt,
	)
	item.PasswordConfigured = passwordConfigured != 0
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, err
}

func (s *Store) HardwareResources(ctx context.Context) ([]domain.HardwareResource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id,r.description,r.mode,r.serial_device,r.serial_baudrate,
		       r.network_host,r.network_port,r.network_protocol,r.ssh_auth,r.username,
		       r.credential_ref,r.password_configured,r.adb_paired_at,r.access,r.created_at,r.updated_at,
		       COUNT(b.task_id)
		FROM hardware_resources r
		LEFT JOIN task_hardware_bindings b ON b.resource_id=r.id
		GROUP BY r.id
		ORDER BY r.description,r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.HardwareResource, 0)
	for rows.Next() {
		var item domain.HardwareResource
		var passwordConfigured int
		var createdAt, updatedAt string
		if err := rows.Scan(
			&item.ID, &item.Description, &item.Mode, &item.Serial.Device, &item.Serial.Baudrate,
			&item.Network.Host, &item.Network.Port, &item.Network.Protocol, &item.Network.SSHAuth,
			&item.Username, &item.CredentialRef, &passwordConfigured, &item.ADBPairedAt, &item.Access,
			&createdAt, &updatedAt, &item.BindingCount,
		); err != nil {
			return nil, err
		}
		item.PasswordConfigured = passwordConfigured != 0
		item.CreatedAt = parseTime(createdAt)
		item.UpdatedAt = parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) HardwareResource(ctx context.Context, id string) (domain.HardwareResource, error) {
	item, err := scanHardwareResource(s.db.QueryRowContext(ctx, `
		SELECT `+hardwareResourceColumns+` FROM hardware_resources WHERE id=?`, id))
	if err != nil {
		return domain.HardwareResource{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_hardware_bindings WHERE resource_id=?`, id).Scan(&item.BindingCount); err != nil {
		return domain.HardwareResource{}, err
	}
	return item, nil
}

func (s *Store) HardwareCredentialReferenced(ctx context.Context, ref, excludeResourceID string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	var found bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM hardware_resources
			WHERE credential_ref=? AND id<>?
		)`, ref, excludeResourceID).Scan(&found)
	return found, err
}

func (s *Store) UpsertHardwareResource(ctx context.Context, item domain.HardwareResource) error {
	if strings.TrimSpace(item.ID) == "" {
		return fmt.Errorf("hardware resource id is required")
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO hardware_resources(
			id,description,mode,serial_device,serial_baudrate,network_host,network_port,
			network_protocol,ssh_auth,username,credential_ref,password_configured,adb_paired_at,access,
			created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			description=excluded.description,mode=excluded.mode,
			serial_device=excluded.serial_device,serial_baudrate=excluded.serial_baudrate,
			network_host=excluded.network_host,network_port=excluded.network_port,
			network_protocol=excluded.network_protocol,ssh_auth=excluded.ssh_auth,
			username=excluded.username,credential_ref=excluded.credential_ref,
			password_configured=excluded.password_configured,adb_paired_at=excluded.adb_paired_at,access=excluded.access,
			updated_at=excluded.updated_at`,
		item.ID, item.Description, item.Mode, item.Serial.Device, item.Serial.Baudrate,
		item.Network.Host, item.Network.Port, item.Network.Protocol, item.Network.SSHAuth,
		item.Username, item.CredentialRef, boolInt(item.PasswordConfigured), item.ADBPairedAt, item.Access,
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	)
	return err
}

func (s *Store) DeleteHardwareResource(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM hardware_resources WHERE id=?`, id)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) TaskHardwareBindings(ctx context.Context, taskID string) ([]domain.TaskHardwareBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id,id,resource_id,position,created_at,updated_at
		FROM task_hardware_bindings WHERE task_id=? ORDER BY position,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.TaskHardwareBinding, 0)
	for rows.Next() {
		var item domain.TaskHardwareBinding
		var createdAt, updatedAt string
		if err := rows.Scan(&item.TaskID, &item.ID, &item.ResourceID, &item.Position, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(createdAt)
		item.UpdatedAt = parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) HardwareResourceBindings(ctx context.Context, resourceID string) ([]domain.TaskHardwareBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id,id,resource_id,position,created_at,updated_at
		FROM task_hardware_bindings WHERE resource_id=? ORDER BY task_id,position,id`, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.TaskHardwareBinding, 0)
	for rows.Next() {
		var item domain.TaskHardwareBinding
		var createdAt, updatedAt string
		if err := rows.Scan(&item.TaskID, &item.ID, &item.ResourceID, &item.Position, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(createdAt)
		item.UpdatedAt = parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func writeTaskHardwareBindings(ctx context.Context, tx *sql.Tx, taskID string, bindings []domain.TaskHardwareBinding) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_hardware_bindings WHERE task_id=?`, taskID); err != nil {
		return err
	}
	now := time.Now().UTC()
	for index, binding := range bindings {
		if binding.ResourceID == "" {
			return fmt.Errorf("hardware resource id is required")
		}
		if binding.ID == "" {
			return fmt.Errorf("hardware binding id is required")
		}
		var resourceAccess string
		if err := tx.QueryRowContext(ctx, `SELECT access FROM hardware_resources WHERE id=?`, binding.ResourceID).Scan(&resourceAccess); err != nil {
			return fmt.Errorf("load hardware resource %s: %w", binding.ResourceID, err)
		}
		createdAt := binding.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		updatedAt := binding.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = now
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO task_hardware_bindings(
				task_id,id,resource_id,position,access,created_at,updated_at
			) VALUES(?,?,?,?,?,?,?)`,
			taskID, binding.ID, binding.ResourceID, index, resourceAccess,
			timeString(createdAt), timeString(updatedAt),
		); err != nil {
			return fmt.Errorf("insert hardware binding %s: %w", binding.ID, err)
		}
	}
	return nil
}

func (s *Store) ReplaceTaskHardwareBindings(ctx context.Context, taskID string, bindings []domain.TaskHardwareBinding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeTaskHardwareBindings(ctx, tx, taskID, bindings); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) HardwareGroups(ctx context.Context, taskID string) ([]domain.HardwareGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.task_id,b.id,b.resource_id,b.position,r.description,r.mode,
		       r.serial_device,r.serial_baudrate,r.network_host,r.network_port,
		       r.network_protocol,r.ssh_auth,r.username,r.credential_ref,
		       r.password_configured,r.access,b.created_at,b.updated_at
		FROM task_hardware_bindings b
		JOIN hardware_resources r ON r.id=b.resource_id
		WHERE b.task_id=?
		ORDER BY b.position,b.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.HardwareGroup, 0)
	for rows.Next() {
		var item domain.HardwareGroup
		var passwordConfigured int
		var createdAt, updatedAt string
		if err := rows.Scan(
			&item.TaskID, &item.ID, &item.ResourceID, &item.Position, &item.Description,
			&item.Mode, &item.Serial.Device, &item.Serial.Baudrate, &item.Network.Host,
			&item.Network.Port, &item.Network.Protocol, &item.Network.SSHAuth, &item.Username,
			&item.CredentialRef, &passwordConfigured, &item.Access, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		item.PasswordConfigured = passwordConfigured != 0
		item.CreatedAt = parseTime(createdAt)
		item.UpdatedAt = parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) HardwareGroup(ctx context.Context, taskID, hardwareID string) (domain.HardwareGroup, error) {
	groups, err := s.HardwareGroups(ctx, taskID)
	if err != nil {
		return domain.HardwareGroup{}, err
	}
	for _, item := range groups {
		if item.ID == hardwareID {
			return item, nil
		}
	}
	return domain.HardwareGroup{}, sql.ErrNoRows
}

// ReplaceHardwareGroups is the compatibility path for older callers. New API
// code should bind an existing system-level resource instead of creating a
// resource from task-local fields.
func (s *Store) ReplaceHardwareGroups(ctx context.Context, taskID string, groups []domain.HardwareGroup) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	bindings := make([]domain.TaskHardwareBinding, 0, len(groups))
	insertResource := func(resource domain.HardwareResource) error {
		if resource.CreatedAt.IsZero() {
			resource.CreatedAt = now
		}
		if resource.UpdatedAt.IsZero() {
			resource.UpdatedAt = now
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO hardware_resources(
				id,description,mode,serial_device,serial_baudrate,network_host,network_port,
				network_protocol,ssh_auth,username,credential_ref,password_configured,adb_paired_at,access,
				created_at,updated_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			resource.ID, resource.Description, resource.Mode, resource.Serial.Device,
			resource.Serial.Baudrate, resource.Network.Host, resource.Network.Port,
			resource.Network.Protocol, resource.Network.SSHAuth, resource.Username,
			resource.CredentialRef, boolInt(resource.PasswordConfigured), resource.ADBPairedAt, resource.Access,
			timeString(resource.CreatedAt), timeString(resource.UpdatedAt),
		)
		if err != nil {
			return fmt.Errorf("insert hardware resource %s: %w", resource.ID, err)
		}
		return nil
	}
	for index, group := range groups {
		resourceID := group.ResourceID
		if resourceID == "" {
			if group.Mode == domain.HardwareModeBoth {
				resources := []domain.HardwareResource{}
				if group.Serial.Device != "" {
					resources = append(resources, domain.HardwareResource{
						ID: domain.NewID("hardware_resource"), Description: group.Description + " · 串口",
						Mode: domain.HardwareModeSerial, Serial: group.Serial, Username: group.Username,
						CredentialRef: group.CredentialRef, PasswordConfigured: group.PasswordConfigured,
						Access: group.Access, CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt,
					})
				}
				if group.Network.Host != "" {
					resources = append(resources, domain.HardwareResource{
						ID: domain.NewID("hardware_resource"), Description: group.Description + " · 网络",
						Mode: domain.HardwareModeNetwork, Network: group.Network, Username: group.Username,
						CredentialRef: group.CredentialRef, PasswordConfigured: group.PasswordConfigured,
						Access: group.Access, CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt,
					})
				}
				for offset, resource := range resources {
					if err := insertResource(resource); err != nil {
						return err
					}
					bindingID := group.ID
					if offset > 0 || bindingID == "" {
						bindingID = resource.ID
					}
					bindings = append(bindings, domain.TaskHardwareBinding{
						TaskID: taskID, ID: bindingID, ResourceID: resource.ID, Position: index*2 + offset,
						CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt,
					})
				}
				continue
			}
			resourceID = domain.NewID("hardware_resource")
			credentialRef := group.CredentialRef
			if credentialRef == "" && group.PasswordConfigured {
				credentialRef = "hardware-resource/" + resourceID + "/credential"
			}
			if err := insertResource(domain.HardwareResource{
				ID: resourceID, Description: group.Description, Mode: group.Mode,
				Serial: group.Serial, Network: group.Network, Username: group.Username,
				CredentialRef: credentialRef, PasswordConfigured: group.PasswordConfigured,
				Access: group.Access, CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt,
			}); err != nil {
				return err
			}
		}
		bindingID := group.ID
		if bindingID == "" {
			bindingID = resourceID
		}
		createdAt := group.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		bindings = append(bindings, domain.TaskHardwareBinding{
			TaskID: taskID, ID: bindingID, ResourceID: resourceID, Position: index,
			CreatedAt: createdAt, UpdatedAt: group.UpdatedAt,
		})
	}
	if err := writeTaskHardwareBindings(ctx, tx, taskID, bindings); err != nil {
		return err
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

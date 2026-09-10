package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func channelLifecycleRefs(ctx context.Context, tx *sql.Tx, instance domain.ChannelInstance) ([]string, error) {
	refs := []string{}
	if instance.CredentialRef != "" {
		refs = append(refs, instance.CredentialRef)
	}
	rows, err := tx.QueryContext(ctx, `SELECT verification_url_secret_ref,secret_stage_ref FROM channel_onboarding_sessions WHERE instance_id=?`, instance.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var verification, stage string
		if err := rows.Scan(&verification, &stage); err != nil {
			rows.Close()
			return nil, err
		}
		if verification != "" {
			refs = append(refs, verification)
		}
		if stage != "" {
			refs = append(refs, stage)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT token_secret_ref FROM channel_service_capabilities WHERE instance_id=? AND token_secret_ref<>''`, instance.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Close()
}

func (s *Store) resetChannelLifecycle(ctx context.Context, id, ownerID string, expectedRevision int, retire bool, at time.Time) (domain.ChannelInstance, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelInstance{}, nil, err
	}
	defer tx.Rollback()
	instance, err := scanChannelInstance(tx.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE id=? AND owner_id=?`, id, ownerID))
	if err != nil {
		return domain.ChannelInstance{}, nil, err
	}
	if instance.Revision != expectedRevision {
		return domain.ChannelInstance{}, nil, ErrChannelRevision
	}
	refs, err := channelLifecycleRefs(ctx, tx, instance)
	if err != nil {
		return domain.ChannelInstance{}, nil, err
	}
	if instance.Retired {
		if retire {
			return instance, refs, nil
		}
		return domain.ChannelInstance{}, nil, ErrChannelRevision
	}
	retiredAt := ""
	if retire {
		retiredAt = timeString(at)
	}
	status := "draft"
	if retire {
		status = "disabled"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_instances SET status=?,app_id='',provider_tenant_id='',credential_configured=0,retired_at=?,revision=revision+1,updated_at=? WHERE id=? AND owner_id=? AND revision=? AND retired_at=''`, status, retiredAt, timeString(at), id, ownerID, expectedRevision); err != nil {
		return domain.ChannelInstance{}, nil, err
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`UPDATE channel_onboarding_sessions SET status='cancelled',step='channel_binding_reset',consumed_at=?,updated_at=? WHERE instance_id=? AND status IN ('pending','qr_ready')`, []any{timeString(at), timeString(at), id}},
		{`UPDATE channel_plugin_commands SET state='cancelled',last_error_code='channel_binding_reset',lease_id='',lease_until='',completed_at=? WHERE instance_id=? AND state IN ('pending','leased')`, []any{timeString(at), id}},
		{`UPDATE channel_service_capabilities SET status='revoked',revoked_at=? WHERE instance_id=? AND status='active'`, []any{timeString(at), id}},
		{`UPDATE channel_identity_links SET status='revoked',revoked_at=? WHERE instance_id=? AND status='active'`, []any{timeString(at), id}},
		{`UPDATE channel_task_routes SET state='revoked',exited_at=?,exit_reason='channel binding reset' WHERE instance_id=? AND state IN ('pending','active')`, []any{timeString(at), id}},
		{`UPDATE channel_subscriptions SET state='revoked',updated_at=? WHERE instance_id=? AND state='active'`, []any{timeString(at), id}},
		{`UPDATE channel_sessions SET status='closed',closed_at=? WHERE status='active' AND conversation_id IN (SELECT id FROM channel_conversations WHERE instance_id=?)`, []any{timeString(at), id}},
		{`UPDATE channel_conversations SET status='closed',updated_at=? WHERE instance_id=? AND status='active'`, []any{timeString(at), id}},
		{`UPDATE channel_delivery_outbox SET state='skipped',lease_id='',lease_until='',last_error_code='channel_retired',updated_at=? WHERE instance_id=? AND state IN ('pending','leased')`, []any{timeString(at), id}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return domain.ChannelInstance{}, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.ChannelInstance{}, nil, err
	}
	updated, err := s.ChannelInstance(ctx, id)
	return updated, refs, err
}

func (s *Store) ClearChannelSecretRefs(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`UPDATE channel_instances SET credential_ref='' WHERE id=?`,
		`UPDATE channel_onboarding_sessions SET verification_url_secret_ref='',secret_stage_ref='' WHERE instance_id=?`,
		`UPDATE channel_service_capabilities SET token_secret_ref='' WHERE instance_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ResetChannelBinding(ctx context.Context, id, ownerID string, expectedRevision int, at time.Time) (domain.ChannelInstance, []string, error) {
	return s.resetChannelLifecycle(ctx, id, ownerID, expectedRevision, false, at)
}

func (s *Store) RetireChannelInstance(ctx context.Context, id, ownerID string, expectedRevision int, at time.Time) (domain.ChannelInstance, []string, error) {
	return s.resetChannelLifecycle(ctx, id, ownerID, expectedRevision, true, at)
}

func (s *Store) ChannelPurgePreview(ctx context.Context, id, ownerID string) (domain.ChannelPurgePreview, error) {
	instance, err := s.ChannelInstance(ctx, id)
	if err != nil || instance.OwnerID != ownerID {
		if err == nil {
			err = sql.ErrNoRows
		}
		return domain.ChannelPurgePreview{}, err
	}
	if !instance.Retired {
		return domain.ChannelPurgePreview{}, ErrChannelNotRetired
	}
	preview := domain.ChannelPurgePreview{InstanceID: id, Name: instance.Name}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=?`, instance.HostProjectID).Scan(&preview.Tasks)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_conversations WHERE instance_id=?`, id).Scan(&preview.Conversations)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversation_items WHERE task_id IN (SELECT id FROM tasks WHERE project_id=?)`, instance.HostProjectID).Scan(&preview.Messages)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE task_id IN (SELECT id FROM tasks WHERE project_id=?)`, instance.HostProjectID).Scan(&preview.Attachments)
	return preview, nil
}

func (s *Store) ChannelInstanceHasActiveTurn(ctx context.Context, id string) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM turns turn_item JOIN tasks task ON task.id=turn_item.task_id
		JOIN channel_instances instance ON instance.host_project_id=task.project_id
		WHERE instance.id=? AND turn_item.status IN ('queued','preparing','starting','running','waiting')
	)`, id).Scan(&active)
	return active, err
}

func (s *Store) PurgeRetiredChannelInstance(ctx context.Context, id, ownerID string, expectedRevision int) (domain.ChannelInstance, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelInstance{}, err
	}
	defer tx.Rollback()
	instance, err := scanChannelInstance(tx.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE id=? AND owner_id=?`, id, ownerID))
	if err != nil || instance.OwnerID != ownerID {
		if err == nil {
			err = sql.ErrNoRows
		}
		return domain.ChannelInstance{}, err
	}
	if !instance.Retired {
		return domain.ChannelInstance{}, ErrChannelNotRetired
	}
	if instance.Revision != expectedRevision {
		return domain.ChannelInstance{}, ErrChannelRevision
	}
	taskRows, err := tx.QueryContext(ctx, `SELECT id,runtime_config_snapshot_id FROM tasks WHERE project_id=?`, instance.HostProjectID)
	if err != nil {
		return domain.ChannelInstance{}, err
	}
	snapshotIDs := []string{}
	for taskRows.Next() {
		var taskID, snapshotID string
		if err := taskRows.Scan(&taskID, &snapshotID); err != nil {
			taskRows.Close()
			return domain.ChannelInstance{}, err
		}
		if snapshotID != "" {
			snapshotIDs = append(snapshotIDs, snapshotID)
		}
	}
	if err := taskRows.Close(); err != nil {
		return domain.ChannelInstance{}, err
	}
	hashRows, err := tx.QueryContext(ctx, `SELECT DISTINCT attachment.sha256 FROM attachments attachment JOIN tasks task ON task.id=attachment.task_id WHERE task.project_id=?`, instance.HostProjectID)
	if err != nil {
		return domain.ChannelInstance{}, err
	}
	hashes := []string{}
	for hashRows.Next() {
		var hash string
		if err := hashRows.Scan(&hash); err != nil {
			hashRows.Close()
			return domain.ChannelInstance{}, err
		}
		hashes = append(hashes, hash)
	}
	if err := hashRows.Close(); err != nil {
		return domain.ChannelInstance{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM channel_instances WHERE id=? AND owner_id=? AND revision=? AND retired_at<>''`, id, ownerID, expectedRevision); err != nil {
		return domain.ChannelInstance{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE project_id=?`, instance.HostProjectID); err != nil {
		return domain.ChannelInstance{}, err
	}
	for _, snapshotID := range snapshotIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM runtime_config_snapshots WHERE id=?`, snapshotID); err != nil {
			return domain.ChannelInstance{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id=?`, instance.HostWorkspaceID); err != nil {
		return domain.ChannelInstance{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, instance.HostProjectID); err != nil {
		return domain.ChannelInstance{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ChannelInstance{}, err
	}
	for _, hash := range hashes {
		if len(hash) != 64 {
			continue
		}
		var references int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE sha256=?`, hash).Scan(&references)
		if references == 0 {
			_ = os.Remove(filepath.Join(s.dataDir, "attachments", "blobs", hash[:2], hash))
		}
	}
	return instance, nil
}

func IsChannelNotRetired(err error) bool { return errors.Is(err, ErrChannelNotRetired) }

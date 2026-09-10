package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const channelOnboardingColumns = `id,instance_id,owner_session_id,mode,registration_command_id,verification_url_secret_ref,status,step,secret_stage_ref,scanner_external_user_id,expires_at,consumed_at,created_at,updated_at`

func scanChannelOnboarding(scanner interface{ Scan(...any) error }) (domain.ChannelOnboardingSession, error) {
	var item domain.ChannelOnboardingSession
	var ownerSession sql.NullString
	var expires, consumed, created, updated string
	err := scanner.Scan(&item.ID, &item.InstanceID, &ownerSession, &item.Mode, &item.RegistrationCommandID, &item.VerificationURLRef,
		&item.Status, &item.Step, &item.SecretStageRef, &item.ScannerExternalUserID, &expires, &consumed, &created, &updated)
	if ownerSession.Valid {
		item.OwnerSessionID = ownerSession.String
	}
	item.ExpiresAt, item.ConsumedAt, item.CreatedAt, item.UpdatedAt = parseTime(expires), parseTime(consumed), parseTime(created), parseTime(updated)
	return item, err
}

func (s *Store) CreateChannelOnboarding(ctx context.Context, item domain.ChannelOnboardingSession) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO channel_onboarding_sessions(id,instance_id,owner_session_id,mode,registration_command_id,verification_url_secret_ref,status,step,secret_stage_ref,scanner_external_user_id,expires_at,consumed_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.InstanceID, nullableString(item.OwnerSessionID), item.Mode, item.RegistrationCommandID, item.VerificationURLRef,
		item.Status, item.Step, item.SecretStageRef, item.ScannerExternalUserID, timeString(item.ExpiresAt), timeString(item.ConsumedAt), timeString(item.CreatedAt), timeString(item.UpdatedAt))
	return err
}

func (s *Store) CancelActiveChannelOnboardings(ctx context.Context, instanceID string, at time.Time) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT registration_command_id,verification_url_secret_ref,secret_stage_ref FROM channel_onboarding_sessions WHERE instance_id=? AND status IN ('pending','qr_ready')`, instanceID)
	if err != nil {
		return nil, err
	}
	commands, refs := []string{}, []string{}
	for rows.Next() {
		var commandID, verificationRef, stageRef string
		if err := rows.Scan(&commandID, &verificationRef, &stageRef); err != nil {
			rows.Close()
			return nil, err
		}
		commands = append(commands, commandID)
		if verificationRef != "" {
			refs = append(refs, verificationRef)
		}
		if stageRef != "" {
			refs = append(refs, stageRef)
		}
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `UPDATE channel_onboarding_sessions SET status='cancelled',step='superseded',consumed_at=?,updated_at=? WHERE instance_id=? AND status IN ('pending','qr_ready')`, timeString(at), timeString(at), instanceID); err != nil {
		return nil, err
	}
	for _, commandID := range commands {
		if _, err := tx.ExecContext(ctx, `UPDATE channel_plugin_commands SET state='cancelled',lease_id='',lease_until='',completed_at=? WHERE id=? AND state IN ('pending','leased')`, timeString(at), commandID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_plugin_commands SET state='cancelled',last_error_code='menu_initialization_cancelled_by_reauthorization',lease_id='',lease_until='',completed_at=? WHERE instance_id=? AND kind='initialize_menu' AND state IN ('pending','leased')`, timeString(at), instanceID); err != nil {
		return nil, err
	}
	return refs, tx.Commit()
}

func (s *Store) ChannelOnboarding(ctx context.Context, id string) (domain.ChannelOnboardingSession, error) {
	return scanChannelOnboarding(s.db.QueryRowContext(ctx, `SELECT `+channelOnboardingColumns+` FROM channel_onboarding_sessions WHERE id=?`, id))
}

func (s *Store) ActiveChannelOnboarding(ctx context.Context, instanceID string) (domain.ChannelOnboardingSession, error) {
	return scanChannelOnboarding(s.db.QueryRowContext(ctx, `SELECT `+channelOnboardingColumns+` FROM channel_onboarding_sessions WHERE instance_id=? AND status IN ('pending','qr_ready') ORDER BY created_at DESC LIMIT 1`, instanceID))
}

func (s *Store) UpdateChannelOnboardingQR(ctx context.Context, id, commandID, verificationRef string, expiresAt, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE channel_onboarding_sessions SET verification_url_secret_ref=?,status='qr_ready',step='awaiting_scan',expires_at=?,updated_at=? WHERE id=? AND registration_command_id=? AND status='pending'`,
		verificationRef, timeString(expiresAt), timeString(at), id, commandID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrChannelRevision
	}
	return nil
}

func (s *Store) CancelChannelOnboarding(ctx context.Context, id, ownerSessionID string, at time.Time) (domain.ChannelOnboardingSession, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE channel_onboarding_sessions SET status='cancelled',step='cancelled',consumed_at=?,updated_at=? WHERE id=? AND owner_session_id=? AND status IN ('pending','qr_ready')`, timeString(at), timeString(at), id, ownerSessionID)
	if err != nil {
		return domain.ChannelOnboardingSession{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		item, lookupErr := s.ChannelOnboarding(ctx, id)
		if lookupErr == nil && item.OwnerSessionID == ownerSessionID && item.Status == "cancelled" {
			return item, nil
		}
		return domain.ChannelOnboardingSession{}, ErrChannelRevision
	}
	item, err := s.ChannelOnboarding(ctx, id)
	if err == nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE channel_plugin_commands SET state='cancelled',lease_id='',lease_until='',completed_at=? WHERE id=? AND state IN ('pending','leased')`, timeString(at), item.RegistrationCommandID)
	}
	return item, err
}

func (s *Store) CompleteChannelRegistration(ctx context.Context, onboardingID, commandID, appID, credentialRef, scannerExternalUserID, tenantBrand string, at time.Time) (domain.ChannelInstance, domain.ChannelIdentityLink, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
	}
	defer tx.Rollback()
	onboarding, err := scanChannelOnboarding(tx.QueryRowContext(ctx, `SELECT `+channelOnboardingColumns+` FROM channel_onboarding_sessions WHERE id=? AND registration_command_id=?`, onboardingID, commandID))
	if err != nil {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
	}
	if onboarding.Status == "succeeded" {
		instance, instanceErr := scanChannelInstance(tx.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE id=?`, onboarding.InstanceID))
		if instanceErr != nil {
			return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, instanceErr
		}
		identity, identityErr := scanChannelIdentity(tx.QueryRowContext(ctx, `SELECT id,instance_id,owner_id,provider_tenant_id,external_user_id,union_id,role,display_name,status,linked_at,revoked_at FROM channel_identity_links WHERE instance_id=? AND role='owner' AND status='active'`, onboarding.InstanceID))
		return instance, identity, identityErr
	}
	if onboarding.Status != "pending" && onboarding.Status != "qr_ready" {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, ErrChannelRevision
	}
	if !onboarding.ExpiresAt.After(at) {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, errors.New("channel onboarding expired")
	}
	instance, err := scanChannelInstance(tx.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE id=?`, onboarding.InstanceID))
	if err != nil {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
	}
	if onboarding.Mode == "existing_app" && (instance.AppID == "" || instance.AppID != appID) {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, errors.New("channel reauthorization app mismatch")
	}
	initializeMenu := onboarding.Mode == "register_app" && !instance.CredentialConfigured && instance.AppID == ""
	var existingExternal string
	err = tx.QueryRowContext(ctx, `SELECT external_user_id FROM channel_identity_links WHERE instance_id=? AND role='owner' AND status='active'`, instance.ID).Scan(&existingExternal)
	if err == nil && existingExternal != scannerExternalUserID {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, errors.New("channel instance already has another owner")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
	}
	identityID := domain.NewID("channel_identity")
	if _, err := tx.ExecContext(ctx, `INSERT INTO channel_identity_links(id,instance_id,owner_id,provider_tenant_id,external_user_id,union_id,role,display_name,status,linked_at,revoked_at) VALUES(?,?,?,'',?,'','owner','','active',?,'') ON CONFLICT(instance_id,external_user_id) DO UPDATE SET owner_id=excluded.owner_id,role='owner',status='active',linked_at=excluded.linked_at,revoked_at=''`,
		identityID, instance.ID, instance.OwnerID, scannerExternalUserID, timeString(at)); err != nil {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_instances SET app_id=?,credential_ref=?,credential_configured=1,status='onboarding',config_json=json_patch(config_json,?),revision=revision+1,updated_at=? WHERE id=?`, appID, credentialRef, encodeJSON(map[string]any{"tenant_brand": tenantBrand}), timeString(at), instance.ID); err != nil {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channel_onboarding_sessions SET status='succeeded',step='runtime_starting',secret_stage_ref=?,scanner_external_user_id=?,consumed_at=?,updated_at=? WHERE id=?`, credentialRef, scannerExternalUserID, timeString(at), timeString(at), onboarding.ID); err != nil {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
	}
	if initializeMenu {
		payload := encodeJSON(map[string]any{"app_id": appID, "onboarding_id": onboarding.ID, "new_app": true})
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_plugin_commands(id,instance_id,kind,idempotency_key,payload_json,progress_json,state,attempts,available_at,lease_id,lease_until,result_json,last_error_code,created_at,completed_at) VALUES(?,?,'initialize_menu',?,?,'{}','pending',0,?,'','','{}','',?,'')`,
			domain.NewID("channel_command"), instance.ID, "initialize_menu:"+onboarding.ID, payload, timeString(at), timeString(at)); err != nil {
			return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE channel_plugin_commands SET state='failed',last_error_code='menu_initialization_cancelled_by_reauthorization',lease_id='',lease_until='',completed_at=? WHERE instance_id=? AND kind='initialize_menu' AND state IN ('pending','leased')`, timeString(at), instance.ID); err != nil {
			return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
		}
	}
	instance, err = scanChannelInstance(tx.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE id=?`, instance.ID))
	if err != nil {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
	}
	identity, err := scanChannelIdentity(tx.QueryRowContext(ctx, `SELECT id,instance_id,owner_id,provider_tenant_id,external_user_id,union_id,role,display_name,status,linked_at,revoked_at FROM channel_identity_links WHERE instance_id=? AND external_user_id=?`, instance.ID, scannerExternalUserID))
	if err != nil {
		return domain.ChannelInstance{}, domain.ChannelIdentityLink{}, err
	}
	return instance, identity, tx.Commit()
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const channelPluginColumns = `id,provider_key,display_name,manifest_version,package_version,protocol_min,protocol_max,executable_path,executable_sha256,manifest_json,install_state,enabled,revision,last_error,discovered_at,updated_at`

func scanChannelPlugin(scanner interface{ Scan(...any) error }) (domain.ChannelPlugin, error) {
	var item domain.ChannelPlugin
	var manifest, discoveredAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.ProviderKey, &item.DisplayName, &item.ManifestVersion, &item.PackageVersion,
		&item.ProtocolMin, &item.ProtocolMax, &item.ExecutablePath, &item.ExecutableSHA256, &manifest,
		&item.InstallState, &item.Enabled, &item.Revision, &item.LastError, &discoveredAt, &updatedAt,
	)
	item.Manifest = decodeJSON(manifest, map[string]any{})
	item.DiscoveredAt, item.UpdatedAt = parseTime(discoveredAt), parseTime(updatedAt)
	item.Available = item.Enabled && item.InstallState == "installed"
	return item, err
}

func (s *Store) UpsertChannelPlugin(ctx context.Context, item domain.ChannelPlugin) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO channel_plugins(id,provider_key,display_name,manifest_version,package_version,protocol_min,protocol_max,executable_path,executable_sha256,manifest_json,install_state,enabled,revision,last_error,discovered_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(provider_key) DO UPDATE SET
			display_name=excluded.display_name,manifest_version=excluded.manifest_version,package_version=excluded.package_version,
			protocol_min=excluded.protocol_min,protocol_max=excluded.protocol_max,executable_path=excluded.executable_path,
			executable_sha256=excluded.executable_sha256,manifest_json=excluded.manifest_json,install_state=excluded.install_state,
			last_error=excluded.last_error,discovered_at=excluded.discovered_at,updated_at=excluded.updated_at,
			revision=channel_plugins.revision+1
		WHERE channel_plugins.display_name<>excluded.display_name
		   OR channel_plugins.manifest_version<>excluded.manifest_version
		   OR channel_plugins.package_version<>excluded.package_version
		   OR channel_plugins.protocol_min<>excluded.protocol_min
		   OR channel_plugins.protocol_max<>excluded.protocol_max
		   OR channel_plugins.executable_path<>excluded.executable_path
		   OR channel_plugins.executable_sha256<>excluded.executable_sha256
		   OR channel_plugins.manifest_json<>excluded.manifest_json
		   OR channel_plugins.install_state<>excluded.install_state
		   OR channel_plugins.last_error<>excluded.last_error`,
		item.ID, item.ProviderKey, item.DisplayName, item.ManifestVersion, item.PackageVersion,
		item.ProtocolMin, item.ProtocolMax, item.ExecutablePath, item.ExecutableSHA256, encodeJSON(item.Manifest),
		item.InstallState, boolInt(item.Enabled), maxInt(item.Revision, 1), item.LastError,
		timeString(item.DiscoveredAt), timeString(item.UpdatedAt),
	)
	return err
}

func (s *Store) MarkMissingChannelPlugins(ctx context.Context, seen []string, at time.Time) error {
	query := `UPDATE channel_plugins SET install_state='missing',last_error='plugin manifest was not discovered',revision=revision+1,updated_at=? WHERE (install_state<>'missing' OR last_error<>'plugin manifest was not discovered')`
	args := []any{timeString(at)}
	if len(seen) > 0 {
		query += ` AND provider_key NOT IN (` + placeholders(len(seen)) + `)`
		for _, value := range seen {
			args = append(args, value)
		}
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	value := "?"
	for index := 1; index < count; index++ {
		value += ",?"
	}
	return value
}

func maxInt(value, fallback int) int {
	if value < fallback {
		return fallback
	}
	return value
}

func (s *Store) ChannelPlugins(ctx context.Context) ([]domain.ChannelPlugin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+channelPluginColumns+` FROM channel_plugins ORDER BY display_name,provider_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ChannelPlugin{}
	for rows.Next() {
		item, scanErr := scanChannelPlugin(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ChannelPlugin(ctx context.Context, id string) (domain.ChannelPlugin, error) {
	return scanChannelPlugin(s.db.QueryRowContext(ctx, `SELECT `+channelPluginColumns+` FROM channel_plugins WHERE id=?`, id))
}

func (s *Store) ChannelPluginByProvider(ctx context.Context, providerKey string) (domain.ChannelPlugin, error) {
	return scanChannelPlugin(s.db.QueryRowContext(ctx, `SELECT `+channelPluginColumns+` FROM channel_plugins WHERE provider_key=?`, providerKey))
}

func (s *Store) UpdateChannelPluginEnabled(ctx context.Context, id string, enabled bool, expectedRevision int, at time.Time) (domain.ChannelPlugin, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE channel_plugins SET enabled=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, boolInt(enabled), timeString(at), id, expectedRevision)
	if err != nil {
		return domain.ChannelPlugin{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.ChannelPlugin{}, ErrChannelRevision
	}
	return s.ChannelPlugin(ctx, id)
}

const channelInstanceColumns = `id,plugin_id,owner_id,runtime_device_id,name,status,revision,app_id,provider_tenant_id,credential_ref,credential_configured,host_project_id,host_workspace_id,config_json,last_seen_at,retired_at,created_at,updated_at`

func scanChannelInstance(scanner interface{ Scan(...any) error }) (domain.ChannelInstance, error) {
	var item domain.ChannelInstance
	var config, lastSeenAt, retiredAt, createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.PluginID, &item.OwnerID, &item.RuntimeDeviceID, &item.Name, &item.Status, &item.Revision,
		&item.AppID, &item.ProviderTenantID, &item.CredentialRef, &item.CredentialConfigured,
		&item.HostProjectID, &item.HostWorkspaceID, &config, &lastSeenAt, &retiredAt, &createdAt, &updatedAt,
	)
	item.Config = decodeJSON(config, map[string]any{})
	item.LastSeenAt, item.RetiredAt, item.CreatedAt, item.UpdatedAt = parseTime(lastSeenAt), parseTime(retiredAt), parseTime(createdAt), parseTime(updatedAt)
	item.Retired = !item.RetiredAt.IsZero()
	if item.Retired {
		item.Status = "retired"
	}
	return item, err
}

func (s *Store) CreateChannelInstance(ctx context.Context, item domain.ChannelInstance, endpoints []domain.ChannelEndpoint) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO channel_instances(id,plugin_id,owner_id,runtime_device_id,name,status,revision,app_id,provider_tenant_id,credential_ref,credential_configured,host_project_id,host_workspace_id,config_json,last_seen_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.PluginID, item.OwnerID, item.RuntimeDeviceID, item.Name, item.Status, maxInt(item.Revision, 1),
		item.AppID, item.ProviderTenantID, item.CredentialRef, boolInt(item.CredentialConfigured),
		item.HostProjectID, item.HostWorkspaceID, encodeJSON(item.Config), timeString(item.LastSeenAt), timeString(item.CreatedAt), timeString(item.UpdatedAt),
	); err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		if _, err = tx.ExecContext(ctx, `INSERT INTO channel_endpoints(id,instance_id,kind,enabled,config_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			endpoint.ID, item.ID, endpoint.Kind, boolInt(endpoint.Enabled), encodeJSON(endpoint.Config), timeString(endpoint.CreatedAt), timeString(endpoint.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateChannelInstanceWithHost(ctx context.Context, project domain.Project, workspace domain.Workspace, item domain.ChannelInstance, endpoints []domain.ChannelEndpoint) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO projects(id,name,description,project_type,repository_identity,default_workspace_id,default_branch,knowledge_policy,knowledge_revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		project.ID, project.Name, project.Description, project.ProjectType, project.RepositoryIdentity, project.DefaultWorkspaceID,
		project.DefaultBranch, project.KnowledgePolicy, project.KnowledgeRevision, timeString(project.CreatedAt), timeString(project.UpdatedAt)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO workspaces(
			id,project_id,name,locality,transport,root_path,ssh_host,ssh_user,ssh_port,ssh_auth,
			ssh_credential_ref,ssh_password_configured,distro,platform,health,
			capabilities_json,repository_json,last_detected_at,created_at,updated_at,
			owner_device_id,read_only,source_workspace_id,agent_api_mode,agent_api_url,
			agent_api_resolved_url,agent_api_status,agent_api_error,agent_api_last_checked_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		workspace.ID, workspace.ProjectID, workspace.Name, workspace.Locality, workspace.Transport, workspace.RootPath,
		workspace.SSHHost, workspace.SSHUser, workspace.SSHPort, workspace.SSHAuth, workspace.SSHCredentialRef,
		boolInt(workspace.SSHPasswordConfigured), workspace.Distro, workspace.Platform, workspace.Health,
		encodeJSON(workspace.Capabilities), encodeJSON(workspace.Repository), timeString(workspace.LastDetectedAt),
		timeString(workspace.CreatedAt), timeString(workspace.UpdatedAt), workspace.OwnerDeviceID, boolInt(workspace.ReadOnly), workspace.SourceWorkspaceID,
		normalizeWorkspaceAgentAPIMode(workspace.AgentAPIMode), workspace.AgentAPIURL, workspace.AgentAPIResolvedURL,
		workspaceAgentAPIStatus(workspace.AgentAPIStatus), workspace.AgentAPIError, timeString(workspace.AgentAPILastCheckedAt)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO channel_instances(id,plugin_id,owner_id,runtime_device_id,name,status,revision,app_id,provider_tenant_id,credential_ref,credential_configured,host_project_id,host_workspace_id,config_json,last_seen_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.PluginID, item.OwnerID, item.RuntimeDeviceID, item.Name, item.Status, maxInt(item.Revision, 1),
		item.AppID, item.ProviderTenantID, item.CredentialRef, boolInt(item.CredentialConfigured),
		item.HostProjectID, item.HostWorkspaceID, encodeJSON(item.Config), timeString(item.LastSeenAt), timeString(item.CreatedAt), timeString(item.UpdatedAt)); err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		if _, err = tx.ExecContext(ctx, `INSERT INTO channel_endpoints(id,instance_id,kind,enabled,config_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			endpoint.ID, item.ID, endpoint.Kind, boolInt(endpoint.Enabled), encodeJSON(endpoint.Config), timeString(endpoint.CreatedAt), timeString(endpoint.UpdatedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ChannelInstance(ctx context.Context, id string) (domain.ChannelInstance, error) {
	item, err := scanChannelInstance(s.db.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE id=?`, id))
	if err != nil {
		return item, err
	}
	if plugin, pluginErr := s.ChannelPlugin(ctx, item.PluginID); pluginErr == nil {
		item.ProviderKey = plugin.ProviderKey
		if plugin.Available {
			item.EffectiveAvailability = "available"
		} else {
			item.EffectiveAvailability = "unavailable"
		}
	}
	if _, ownerErr := s.ChannelOwnerIdentity(ctx, item.ID); ownerErr == nil {
		item.OwnerBound = true
	}
	return item, nil
}

func (s *Store) ChannelInstances(ctx context.Context, ownerID string) ([]domain.ChannelInstance, error) {
	query := `SELECT ` + channelInstanceColumns + ` FROM channel_instances`
	args := []any{}
	if ownerID != "" {
		query += ` WHERE owner_id=?`
		args = append(args, ownerID)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ChannelInstance{}
	for rows.Next() {
		item, scanErr := scanChannelInstance(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		item := &result[index]
		plugin, pluginErr := s.ChannelPlugin(ctx, item.PluginID)
		if pluginErr == nil {
			item.ProviderKey = plugin.ProviderKey
			if plugin.Available {
				item.EffectiveAvailability = "available"
			} else {
				item.EffectiveAvailability = "unavailable"
			}
		}
		if _, ownerErr := s.ChannelOwnerIdentity(ctx, item.ID); ownerErr == nil {
			item.OwnerBound = true
		}
	}
	return result, nil
}

func (s *Store) UpdateChannelInstance(ctx context.Context, item domain.ChannelInstance, expectedRevision int) (domain.ChannelInstance, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE channel_instances SET name=?,status=?,app_id=?,provider_tenant_id=?,credential_ref=?,credential_configured=?,config_json=?,last_seen_at=?,revision=revision+1,updated_at=?
		WHERE id=? AND owner_id=? AND revision=? AND retired_at=''`,
		item.Name, item.Status, item.AppID, item.ProviderTenantID, item.CredentialRef, boolInt(item.CredentialConfigured),
		encodeJSON(item.Config), timeString(item.LastSeenAt), timeString(item.UpdatedAt), item.ID, item.OwnerID, expectedRevision,
	)
	if err != nil {
		return domain.ChannelInstance{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.ChannelInstance{}, ErrChannelRevision
	}
	return s.ChannelInstance(ctx, item.ID)
}

func (s *Store) UpdateChannelInstanceHealth(ctx context.Context, id, status, errorCode string, at time.Time) error {
	configPatch := map[string]any{"runtime_error_code": errorCode}
	_, err := s.db.ExecContext(ctx, `UPDATE channel_instances SET status=?,last_seen_at=?,config_json=json_patch(config_json,?),updated_at=? WHERE id=? AND status<>'disabled' AND retired_at=''`,
		status, timeString(at), encodeJSON(configPatch), timeString(at), id)
	return err
}

func (s *Store) ChannelEndpoints(ctx context.Context, instanceID string) ([]domain.ChannelEndpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,instance_id,kind,enabled,config_json,created_at,updated_at FROM channel_endpoints WHERE instance_id=? ORDER BY kind`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ChannelEndpoint{}
	for rows.Next() {
		var item domain.ChannelEndpoint
		var config, createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.InstanceID, &item.Kind, &item.Enabled, &config, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.Config = decodeJSON(config, map[string]any{})
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) IsManagedChannelProject(ctx context.Context, id string) bool {
	var exists bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_instances WHERE host_project_id=?)`, id).Scan(&exists)
	return exists
}

func (s *Store) IsManagedChannelWorkspace(ctx context.Context, id string) bool {
	var exists bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_instances WHERE host_workspace_id=?)`, id).Scan(&exists)
	return exists
}

func (s *Store) IsManagedChannelTask(ctx context.Context, id string) bool {
	var exists bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM channel_conversations WHERE host_task_id=?)`, id).Scan(&exists)
	return exists
}

func (s *Store) RetiredChannelInstanceForProject(ctx context.Context, id string) (domain.ChannelInstance, error) {
	return scanChannelInstance(s.db.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE host_project_id=? AND retired_at<>''`, id))
}

func (s *Store) RetiredChannelInstanceForWorkspace(ctx context.Context, id string) (domain.ChannelInstance, error) {
	return scanChannelInstance(s.db.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE host_workspace_id=? AND retired_at<>''`, id))
}

func (s *Store) RetiredChannelInstanceForTask(ctx context.Context, id string) (domain.ChannelInstance, error) {
	return scanChannelInstance(s.db.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE host_project_id=(SELECT project_id FROM tasks WHERE id=?) AND retired_at<>''`, id))
}

// ChannelInstancesForHostProjects returns channel markers for all requested
// host projects in one query, including active instances.
func (s *Store) ChannelInstancesForHostProjects(ctx context.Context, projectIDs []string) (map[string]domain.ChannelInstance, error) {
	return s.channelInstancesByHost(ctx, "host_project_id", projectIDs, false)
}

// RetiredChannelInstancesForHostProjects returns only archived channel markers
// keyed by host project ID.
func (s *Store) RetiredChannelInstancesForHostProjects(ctx context.Context, projectIDs []string) (map[string]domain.ChannelInstance, error) {
	return s.channelInstancesByHost(ctx, "host_project_id", projectIDs, true)
}

// RetiredChannelInstancesForHostWorkspaces returns archived channel markers
// keyed by host workspace ID.
func (s *Store) RetiredChannelInstancesForHostWorkspaces(ctx context.Context, workspaceIDs []string) (map[string]domain.ChannelInstance, error) {
	return s.channelInstancesByHost(ctx, "host_workspace_id", workspaceIDs, true)
}

func (s *Store) channelInstancesByHost(ctx context.Context, column string, ids []string, retiredOnly bool) (map[string]domain.ChannelInstance, error) {
	result := make(map[string]domain.ChannelInstance, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	query := `SELECT ` + channelInstanceColumns + ` FROM channel_instances WHERE ` + column + ` IN (` + placeholders(len(ids)) + `)`
	if retiredOnly {
		query += ` AND retired_at<>''`
	}
	query += ` ORDER BY updated_at DESC,id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanChannelInstance(rows)
		if err != nil {
			return nil, err
		}
		key := item.HostProjectID
		if column == "host_workspace_id" {
			key = item.HostWorkspaceID
		}
		if _, exists := result[key]; !exists {
			result[key] = item
		}
	}
	return result, rows.Err()
}

func (s *Store) ManagedChannelProjectIDs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT host_project_id FROM channel_instances`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func (s *Store) ChannelInstanceForHostProject(ctx context.Context, projectID string) (domain.ChannelInstance, error) {
	item, err := scanChannelInstance(s.db.QueryRowContext(ctx, `SELECT `+channelInstanceColumns+` FROM channel_instances WHERE host_project_id=?`, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelInstance{}, sql.ErrNoRows
	}
	return item, err
}

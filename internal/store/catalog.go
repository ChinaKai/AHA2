package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) CreateProject(ctx context.Context, project domain.Project) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO projects(id,name,description,project_type,repository_identity,default_workspace_id,default_branch,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		project.ID, project.Name, project.Description, project.ProjectType, project.RepositoryIdentity, project.DefaultWorkspaceID,
		project.DefaultBranch, timeString(project.CreatedAt), timeString(project.UpdatedAt),
	)
	return err
}

func (s *Store) ListProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,project_type,repository_identity,default_workspace_id,default_branch,created_at,updated_at FROM projects ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Project{}
	for rows.Next() {
		var item domain.Project
		var createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.ProjectType, &item.RepositoryIdentity, &item.DefaultWorkspaceID, &item.DefaultBranch, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Project(ctx context.Context, id string) (domain.Project, error) {
	var item domain.Project
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,description,project_type,repository_identity,default_workspace_id,default_branch,created_at,updated_at FROM projects WHERE id=?`, id).
		Scan(&item.ID, &item.Name, &item.Description, &item.ProjectType, &item.RepositoryIdentity, &item.DefaultWorkspaceID, &item.DefaultBranch, &createdAt, &updatedAt)
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return item, err
}

func (s *Store) UpdateProject(ctx context.Context, item domain.Project) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE projects SET name=?,description=?,project_type=?,repository_identity=?,default_branch=?,updated_at=? WHERE id=?`,
		item.Name, item.Description, item.ProjectType, item.RepositoryIdentity, item.DefaultBranch,
		timeString(item.UpdatedAt), item.ID,
	)
	return err
}

func (s *Store) CreateWorkspace(ctx context.Context, item domain.Workspace) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO workspaces(
			id,project_id,name,locality,transport,root_path,ssh_host,ssh_user,ssh_port,distro,platform,health,
			capabilities_json,repository_json,last_detected_at,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.ProjectID, item.Name, item.Locality, item.Transport, item.RootPath,
		item.SSHHost, item.SSHUser, item.SSHPort, item.Distro, item.Platform, item.Health,
		encodeJSON(item.Capabilities), encodeJSON(item.Repository), timeString(item.LastDetectedAt),
		timeString(item.CreatedAt), timeString(item.UpdatedAt),
	)
	return err
}

func scanWorkspace(scanner interface{ Scan(...any) error }) (domain.Workspace, error) {
	var item domain.Workspace
	var capabilities, repository, detectedAt, createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.ProjectID, &item.Name, &item.Locality, &item.Transport, &item.RootPath,
		&item.SSHHost, &item.SSHUser, &item.SSHPort, &item.Distro, &item.Platform, &item.Health,
		&capabilities, &repository, &detectedAt, &createdAt, &updatedAt,
	)
	item.Capabilities = decodeJSON(capabilities, map[string]any{})
	item.Repository = decodeJSON(repository, map[string]any{})
	item.LastDetectedAt = parseTime(detectedAt)
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return item, err
}

const workspaceColumns = `id,project_id,name,locality,transport,root_path,ssh_host,ssh_user,ssh_port,distro,platform,health,capabilities_json,repository_json,last_detected_at,created_at,updated_at`

func (s *Store) Workspace(ctx context.Context, id string) (domain.Workspace, error) {
	return scanWorkspace(s.db.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE id=?`, id))
}

func (s *Store) ListWorkspaces(ctx context.Context, projectID string) ([]domain.Workspace, error) {
	query := `SELECT ` + workspaceColumns + ` FROM workspaces`
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id=?`
		args = append(args, projectID)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Workspace{}
	for rows.Next() {
		item, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateWorkspaceDetection(ctx context.Context, item domain.Workspace) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE workspaces SET platform=?,health=?,capabilities_json=?,repository_json=?,last_detected_at=?,updated_at=?
		WHERE id=?`,
		item.Platform, item.Health, encodeJSON(item.Capabilities), encodeJSON(item.Repository),
		timeString(item.LastDetectedAt), timeString(item.UpdatedAt), item.ID,
	)
	return err
}

func (s *Store) UpsertModel(ctx context.Context, item domain.Model) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO models(id,display_name,provider_id,backend,wire_model,wire_api,context_window,max_output_tokens,default_effort,capabilities_json,default_env_group_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			display_name=excluded.display_name,provider_id=excluded.provider_id,backend=excluded.backend,
			wire_model=excluded.wire_model,wire_api=excluded.wire_api,context_window=excluded.context_window,
			max_output_tokens=excluded.max_output_tokens,default_effort=excluded.default_effort,
			capabilities_json=excluded.capabilities_json,default_env_group_id=excluded.default_env_group_id,
			updated_at=excluded.updated_at`,
		item.ID, item.DisplayName, item.ProviderID, item.Backend, item.WireModel, item.WireAPI,
		item.ContextWindow, item.MaxOutputTokens, item.DefaultEffort, encodeJSON(item.Capabilities),
		item.DefaultEnvGroupID, timeString(item.CreatedAt), timeString(item.UpdatedAt),
	)
	return err
}

func (s *Store) ListModels(ctx context.Context) ([]domain.Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,display_name,provider_id,backend,wire_model,wire_api,context_window,max_output_tokens,default_effort,capabilities_json,default_env_group_id,created_at,updated_at FROM models ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Model{}
	for rows.Next() {
		var item domain.Model
		var capabilities, createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.DisplayName, &item.ProviderID, &item.Backend, &item.WireModel, &item.WireAPI, &item.ContextWindow, &item.MaxOutputTokens, &item.DefaultEffort, &capabilities, &item.DefaultEnvGroupID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.Capabilities = decodeJSON(capabilities, map[string]any{})
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Model(ctx context.Context, id string) (domain.Model, error) {
	var item domain.Model
	var capabilities, createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,display_name,provider_id,backend,wire_model,wire_api,context_window,max_output_tokens,default_effort,capabilities_json,default_env_group_id,created_at,updated_at FROM models WHERE id=?`, id).
		Scan(&item.ID, &item.DisplayName, &item.ProviderID, &item.Backend, &item.WireModel, &item.WireAPI, &item.ContextWindow, &item.MaxOutputTokens, &item.DefaultEffort, &capabilities, &item.DefaultEnvGroupID, &createdAt, &updatedAt)
	item.Capabilities = decodeJSON(capabilities, map[string]any{})
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return item, err
}

func (s *Store) UpsertEnvGroup(ctx context.Context, item domain.EnvGroup) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO env_groups(id,name,provider_id,backend,revision,environment_json,secret_names_json,secret_configured,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,provider_id=excluded.provider_id,backend=excluded.backend,revision=excluded.revision,
			environment_json=excluded.environment_json,secret_names_json=excluded.secret_names_json,
			secret_configured=excluded.secret_configured,updated_at=excluded.updated_at`,
		item.ID, item.Name, item.ProviderID, item.Backend, item.Revision, encodeJSON(item.Environment),
		encodeJSON(item.SecretRefs), item.SecretConfigured, timeString(item.CreatedAt), timeString(item.UpdatedAt),
	)
	return err
}

func (s *Store) ListEnvGroups(ctx context.Context) ([]domain.EnvGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,provider_id,backend,revision,environment_json,secret_names_json,secret_configured,created_at,updated_at FROM env_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.EnvGroup{}
	for rows.Next() {
		var item domain.EnvGroup
		var environment, secretNames, createdAt, updatedAt string
		if err := rows.Scan(&item.ID, &item.Name, &item.ProviderID, &item.Backend, &item.Revision, &environment, &secretNames, &item.SecretConfigured, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.Environment = decodeJSON(environment, map[string]string{})
		item.SecretRefs = decodeJSON(secretNames, map[string]string{})
		item.SecretNames = sortedKeys(item.SecretRefs)
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) EnvGroup(ctx context.Context, id string) (domain.EnvGroup, error) {
	var item domain.EnvGroup
	var environment, secretNames, createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,provider_id,backend,revision,environment_json,secret_names_json,secret_configured,created_at,updated_at FROM env_groups WHERE id=?`, id).
		Scan(&item.ID, &item.Name, &item.ProviderID, &item.Backend, &item.Revision, &environment, &secretNames, &item.SecretConfigured, &createdAt, &updatedAt)
	item.Environment = decodeJSON(environment, map[string]string{})
	item.SecretRefs = decodeJSON(secretNames, map[string]string{})
	item.SecretNames = sortedKeys(item.SecretRefs)
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return item, err
}

func (s *Store) UpsertProvider(ctx context.Context, item domain.Provider) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO providers(id,name,base_url,anthropic_base_url,auth_style,credential_ref,credential_configured,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,base_url=excluded.base_url,anthropic_base_url=excluded.anthropic_base_url,
			auth_style=excluded.auth_style,
			credential_ref=excluded.credential_ref,credential_configured=excluded.credential_configured,
			updated_at=excluded.updated_at`,
		item.ID, item.Name, item.BaseURL, item.AnthropicBaseURL, item.AuthStyle, item.CredentialRef,
		boolInt(item.CredentialConfigured), timeString(item.CreatedAt), timeString(item.UpdatedAt),
	)
	return err
}

func scanProvider(scanner interface{ Scan(...any) error }) (domain.Provider, error) {
	var item domain.Provider
	var configured, createdAt, updatedAt string
	err := scanner.Scan(&item.ID, &item.Name, &item.BaseURL, &item.AnthropicBaseURL, &item.AuthStyle, &item.CredentialRef, &configured, &createdAt, &updatedAt)
	item.CredentialConfigured = configured == "1"
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return item, err
}

func (s *Store) ListProviders(ctx context.Context) ([]domain.Provider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,base_url,anthropic_base_url,auth_style,credential_ref,credential_configured,created_at,updated_at FROM providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Provider{}
	for rows.Next() {
		item, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) Provider(ctx context.Context, id string) (domain.Provider, error) {
	return scanProvider(s.db.QueryRowContext(ctx, `SELECT id,name,base_url,anthropic_base_url,auth_style,credential_ref,credential_configured,created_at,updated_at FROM providers WHERE id=?`, id))
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, id)
	return err
}

// RemapProvider moves models and env groups from one provider id to another
// (used when a provider's name/base_url changes and its derived id changes),
// rewriting the AHA_PROVIDER_ID env var stored in codex env groups.
func (s *Store) RemapProvider(ctx context.Context, oldID, newID string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE models SET provider_id=? WHERE provider_id=?`, newID, oldID); err != nil {
		return err
	}
	groups, err := s.ListEnvGroups(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, group := range groups {
		if group.ProviderID != oldID {
			continue
		}
		if group.Environment["AHA_PROVIDER_ID"] == oldID {
			group.Environment["AHA_PROVIDER_ID"] = newID
		}
		group.ProviderID = newID
		group.UpdatedAt = now
		if err := s.UpsertEnvGroup(ctx, group); err != nil {
			return err
		}
	}
	return nil
}

// UpdateWorkspaceConfig updates the connection/identity fields of a workspace
// and resets detection state so the workspace is re-detected after an edit.
func (s *Store) UpdateWorkspaceConfig(ctx context.Context, item domain.Workspace) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE workspaces SET
			name=?,locality=?,transport=?,root_path=?,ssh_host=?,ssh_user=?,ssh_port=?,distro=?,
			platform='',health='unknown',capabilities_json='{}',repository_json='{}',last_detected_at='',updated_at=?
		WHERE id=?`,
		item.Name, item.Locality, item.Transport, item.RootPath, item.SSHHost, item.SSHUser, item.SSHPort, item.Distro,
		timeString(item.UpdatedAt), item.ID,
	)
	return err
}

func (s *Store) DeleteModel(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM models WHERE id=?`, id)
	return err
}

func (s *Store) DeleteEnvGroup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM env_groups WHERE id=?`, id)
	return err
}

func (s *Store) DeleteWorkspace(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workspaces WHERE id=?`, id)
	return err
}

func (s *Store) DeleteProject(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)
	return err
}

func (s *Store) ModelInUse(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runtime_config_snapshots WHERE model_id=?)`, id).Scan(&exists)
	return exists, err
}

func (s *Store) EnvGroupInUse(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runtime_config_snapshots WHERE env_group_id=?)`, id).Scan(&exists)
	return exists, err
}

// BackfillProviders creates provider rows for existing env groups that predate
// the providers table (legacy AHA imports). Returns the number created.
func (s *Store) BackfillProviders(ctx context.Context) (int, error) {
	envGroups, err := s.ListEnvGroups(ctx)
	if err != nil {
		return 0, err
	}
	providers, err := s.ListProviders(ctx)
	if err != nil {
		return 0, err
	}
	existing := make(map[string]bool, len(providers))
	for _, provider := range providers {
		existing[provider.ID] = true
	}
	now := time.Now().UTC()
	count := 0
	seen := make(map[string]bool)
	for _, group := range envGroups {
		id := group.ProviderID
		if id == "" || id == "stub" || existing[id] || seen[id] {
			continue
		}
		seen[id] = true
		name := id
		if index := strings.LastIndex(group.Name, " / "); index > 0 {
			name = group.Name[:index]
		}
		item := domain.Provider{
			ID: id, Name: name, BaseURL: group.Environment["OPENAI_BASE_URL"],
			AuthStyle: "auto", CredentialRef: group.SecretRefs["OPENAI_API_KEY"],
			CredentialConfigured: group.SecretConfigured, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.UpsertProvider(ctx, item); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) CreateRuntimeSnapshot(ctx context.Context, item domain.RuntimeConfigSnapshot) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runtime_config_snapshots(id,workspace_id,backend,backend_version,model_id,wire_model,env_group_id,env_group_revision,reasoning_effort,permissions_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.WorkspaceID, item.Backend, item.BackendVersion, item.ModelID, item.WireModel,
		item.EnvGroupID, item.EnvGroupRevision, item.ReasoningEffort, item.PermissionsJSON, timeString(item.CreatedAt),
	)
	return err
}

func sortedKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func (s *Store) RuntimeSnapshot(ctx context.Context, id string) (domain.RuntimeConfigSnapshot, error) {
	var item domain.RuntimeConfigSnapshot
	var createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,backend,backend_version,model_id,wire_model,env_group_id,env_group_revision,reasoning_effort,permissions_json,created_at FROM runtime_config_snapshots WHERE id=?`, id).
		Scan(&item.ID, &item.WorkspaceID, &item.Backend, &item.BackendVersion, &item.ModelID, &item.WireModel, &item.EnvGroupID, &item.EnvGroupRevision, &item.ReasoningEffort, &item.PermissionsJSON, &createdAt)
	item.CreatedAt = parseTime(createdAt)
	return item, err
}

func isNotFound(err error) bool {
	return err == sql.ErrNoRows
}

func wrapNotFound(name string, err error) error {
	if isNotFound(err) {
		return fmt.Errorf("%s not found: %w", name, err)
	}
	return err
}

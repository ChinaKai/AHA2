package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	TypeKnowledge      = "knowledge"
	TypeSkill          = "skill"
	TypeProvider       = "provider"
	TypeModel          = "model"
	TypeEnvGroup       = "env_group"
	TypePromptOverride = "prompt_override"
	TypeProject        = "project"
	TypeWorkspace      = "workspace"
	TypeTask           = "task"
	TypeTaskAgent      = "task_agent"
	TypeRound          = "round"
	TypeTurn           = "turn"
	TypeConversation   = "conversation"
	TypeTaskMemory     = "task_memory"
)

type skillPayload struct {
	Skill domain.Skill       `json:"skill"`
	Files []domain.SkillFile `json:"files"`
}

// ExportBusinessObjects exports only portable configuration. Credential references,
// secret values, project/task/workspace identities, and local account bindings are removed.
func ExportBusinessObjects(ctx context.Context, database *store.Store) ([]domain.SyncObject, error) {
	return ExportBusinessObjectsForDevice(ctx, database, "")
}

func ExportBusinessObjectsForDevice(ctx context.Context, database *store.Store, ownerDeviceID string) ([]domain.SyncObject, error) {
	var result []domain.SyncObject
	add := func(kind, id, version string, value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		result = append(result, domain.SyncObject{Type: kind, ID: id, Operation: "upsert", Payload: raw, RemoteVersion: version, IdempotencyKey: kind + ":" + id + ":" + version})
		return nil
	}
	if ownerDeviceID != "" {
		graph, err := exportTaskGraph(ctx, database, ownerDeviceID)
		if err != nil {
			return nil, err
		}
		result = append(result, graph...)
	}
	knowledge, err := database.ListKnowledge(ctx, "", "", nil)
	if err != nil {
		return nil, err
	}
	for _, v := range knowledge {
		v.SourceTaskID = ""
		v.SourceTurnID = ""
		if err := add(TypeKnowledge, v.ID, strconv.Itoa(v.Revision), v); err != nil {
			return nil, err
		}
	}
	skills, err := database.ListSkills(ctx, "", "", false)
	if err != nil {
		return nil, err
	}
	for _, v := range skills {
		if v.ProjectID != "" {
			v.Scope = "global"
		}
		v.ProjectID = ""
		v.SourcePath = ""
		v.Files = nil
		if err := add(TypeSkill, v.ID, strconv.Itoa(v.Version), skillPayload{Skill: v, Files: v.PackageFiles}); err != nil {
			return nil, err
		}
	}
	providers, err := database.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range providers {
		v.CredentialRef = ""
		v.CredentialConfigured = false
		if err := add(TypeProvider, v.ID, timeVersion(v.UpdatedAt), v); err != nil {
			return nil, err
		}
	}
	models, err := database.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range models {
		v.CodexAccountID = ""
		v.ProviderName = ""
		if err := add(TypeModel, v.ID, timeVersion(v.UpdatedAt), v); err != nil {
			return nil, err
		}
	}
	groups, err := database.ListEnvGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range groups {
		v.SecretNames = nil
		v.SecretRefs = nil
		v.SecretConfigured = false
		if err := add(TypeEnvGroup, v.ID, strconv.Itoa(v.Revision), v); err != nil {
			return nil, err
		}
	}
	prompts, err := database.PromptTemplateOverrides(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range prompts {
		if err := add(TypePromptOverride, v.ID, strconv.Itoa(v.Version), v); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func RegisterBusinessHandlers(engine *Engine, database *store.Store) {
	for _, kind := range []string{TypeProject, TypeWorkspace, TypeTask, TypeTaskAgent, TypeRound, TypeTurn, TypeConversation, TypeTaskMemory, TypeKnowledge, TypeSkill, TypeProvider, TypeModel, TypeEnvGroup, TypePromptOverride} {
		objectType := kind
		engine.Register(objectType, func(ctx context.Context, obj domain.SyncObject) error { return applyBusinessObject(ctx, database, obj) })
	}
}

func applyBusinessObject(ctx context.Context, database *store.Store, obj domain.SyncObject) error {
	localPayload, localVersion, exists, err := currentBusinessObject(ctx, database, obj.Type, obj.ID)
	if err != nil {
		return err
	}
	if obj.BaseVersion != "" && (!exists || obj.BaseVersion != localVersion) {
		return &ConflictError{LocalPayload: localPayload, LocalVersion: localVersion, Cause: fmt.Errorf("%w: %s %s version %q does not match %q", ErrConflict, obj.Type, obj.ID, obj.BaseVersion, localVersion)}
	}
	if obj.Operation == "delete" {
		if !exists {
			return nil
		}
		return deleteBusinessObject(ctx, database, obj.Type, obj.ID)
	}
	if obj.Operation != "upsert" {
		return fmt.Errorf("unsupported operation %q", obj.Operation)
	}
	return upsertBusinessObject(ctx, database, obj, exists)
}

func currentBusinessObject(ctx context.Context, database *store.Store, kind, id string) (json.RawMessage, string, bool, error) {
	var value any
	var version string
	var err error
	switch kind {
	case TypeProject:
		var v domain.Project
		v, err = database.Project(ctx, id)
		value = v
		version = timeVersion(v.UpdatedAt)
	case TypeWorkspace:
		return nil, "", false, nil
	case TypeTask, TypeTaskAgent, TypeRound, TypeTurn, TypeConversation, TypeTaskMemory:
		return nil, "", false, nil
	case TypeKnowledge:
		var v domain.KnowledgeEntry
		v, err = database.Knowledge(ctx, id)
		value = v
		version = strconv.Itoa(v.Revision)
	case TypeSkill:
		var v domain.Skill
		v, err = database.Skill(ctx, id)
		v.SourcePath = ""
		v.Files = nil
		value = skillPayload{Skill: v, Files: v.PackageFiles}
		version = strconv.Itoa(v.Version)
	case TypeProvider:
		var v domain.Provider
		v, err = database.Provider(ctx, id)
		v.CredentialRef = ""
		v.CredentialConfigured = false
		value = v
		version = timeVersion(v.UpdatedAt)
	case TypeModel:
		var v domain.Model
		v, err = database.Model(ctx, id)
		v.CodexAccountID = ""
		v.ProviderName = ""
		value = v
		version = timeVersion(v.UpdatedAt)
	case TypeEnvGroup:
		var v domain.EnvGroup
		v, err = database.EnvGroup(ctx, id)
		v.SecretNames = nil
		v.SecretRefs = nil
		v.SecretConfigured = false
		value = v
		version = strconv.Itoa(v.Revision)
	case TypePromptOverride:
		var v domain.PromptTemplate
		v, err = database.PromptTemplateOverride(ctx, id)
		value = v
		version = strconv.Itoa(v.Version)
	default:
		return nil, "", false, fmt.Errorf("unsupported business sync type %q", kind)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	raw, err := json.Marshal(value)
	return raw, version, true, err
}

func upsertBusinessObject(ctx context.Context, database *store.Store, obj domain.SyncObject, exists bool) error {
	now := time.Now().UTC()
	switch obj.Type {
	case TypeProject, TypeWorkspace, TypeTask, TypeTaskAgent, TypeRound, TypeTurn, TypeConversation, TypeTaskMemory:
		return applyTaskGraphObject(ctx, database, obj)
	case TypeKnowledge:
		var v domain.KnowledgeEntry
		if err := decodePayload(obj, &v); err != nil {
			return err
		}
		v.ID = obj.ID
		if v.ProjectID != "" {
			if _, err := database.Project(ctx, v.ProjectID); err != nil {
				return fmt.Errorf("knowledge project dependency %s: %w", v.ProjectID, err)
			}
			v.Scope = "project"
		}
		v.SourceTaskID = ""
		v.SourceTurnID = ""
		if exists {
			return database.UpdateKnowledge(ctx, v)
		}
		return database.CreateKnowledge(ctx, v)
	case TypeSkill:
		var p skillPayload
		if err := decodePayload(obj, &p); err != nil {
			return err
		}
		p.Skill.ID = obj.ID
		if p.Skill.ProjectID != "" {
			p.Skill.Scope = "global"
		}
		p.Skill.ProjectID = ""
		p.Skill.SourcePath = ""
		if exists {
			current, err := database.Skill(ctx, obj.ID)
			if err != nil {
				return err
			}
			p.Skill.Version = current.Version
			p.Skill.PackageSlug = current.PackageSlug
			_, err = database.UpdateSkillPackage(ctx, p.Skill, current.Version, p.Files)
			return err
		}
		desired := p.Skill.Version
		if desired < 1 {
			desired = 1
		}
		p.Skill.Version = desired - 1
		if err := database.CreateSkill(ctx, p.Skill); err != nil {
			return err
		}
		created, err := database.Skill(ctx, obj.ID)
		if err != nil {
			return err
		}
		created.Scope, created.ProjectID = p.Skill.Scope, p.Skill.ProjectID
		created.Name, created.Description, created.Instructions = p.Skill.Name, p.Skill.Description, p.Skill.Instructions
		created.Status, created.Enabled, created.UpdatedAt = p.Skill.Status, p.Skill.Enabled, p.Skill.UpdatedAt
		_, err = database.UpdateSkillPackage(ctx, created, desired-1, p.Files)
		return err
	case TypeProvider:
		var v domain.Provider
		if err := decodePayload(obj, &v); err != nil {
			return err
		}
		v.ID = obj.ID
		if exists {
			local, err := database.Provider(ctx, obj.ID)
			if err != nil {
				return err
			}
			v.CredentialRef = local.CredentialRef
			v.CredentialConfigured = local.CredentialConfigured
		}
		return database.UpsertProvider(ctx, v)
	case TypeModel:
		var v domain.Model
		if err := decodePayload(obj, &v); err != nil {
			return err
		}
		v.ID = obj.ID
		if exists {
			local, err := database.Model(ctx, obj.ID)
			if err != nil {
				return err
			}
			v.CodexAccountID = local.CodexAccountID
		}
		return database.UpsertModel(ctx, v)
	case TypeEnvGroup:
		var v domain.EnvGroup
		if err := decodePayload(obj, &v); err != nil {
			return err
		}
		v.ID = obj.ID
		if exists {
			local, err := database.EnvGroup(ctx, obj.ID)
			if err != nil {
				return err
			}
			v.SecretRefs = local.SecretRefs
			v.SecretConfigured = local.SecretConfigured
		}
		return database.UpsertEnvGroup(ctx, v)
	case TypePromptOverride:
		var v domain.PromptTemplate
		if err := decodePayload(obj, &v); err != nil {
			return err
		}
		return database.ImportPromptTemplateOverride(ctx, obj.ID, v.Content, v.Version, now)
	default:
		return fmt.Errorf("unsupported business sync type %q", obj.Type)
	}
}

func deleteBusinessObject(ctx context.Context, database *store.Store, kind, id string) error {
	switch kind {
	case TypeKnowledge:
		return database.DeleteKnowledge(ctx, id)
	case TypeSkill:
		return database.DeleteSkill(ctx, id)
	case TypeProvider:
		return database.DeleteProvider(ctx, id)
	case TypeModel:
		return database.DeleteModel(ctx, id)
	case TypeEnvGroup:
		return database.DeleteEnvGroup(ctx, id)
	case TypePromptOverride:
		return database.DeletePromptTemplateOverride(ctx, id)
	default:
		return fmt.Errorf("unsupported business sync type %q", kind)
	}
}
func decodePayload(obj domain.SyncObject, value any) error {
	if len(obj.Payload) == 0 {
		return fmt.Errorf("%s %s has empty payload", obj.Type, obj.ID)
	}
	if err := json.Unmarshal(obj.Payload, value); err != nil {
		return fmt.Errorf("decode %s payload: %w", obj.Type, err)
	}
	return nil
}
func timeVersion(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

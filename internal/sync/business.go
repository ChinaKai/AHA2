package sync

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const (
	TypeKnowledge         = "knowledge"
	TypeKnowledgeProposal = "knowledge_proposal"
	TypeSkill             = "skill"
	TypeProvider          = "provider"
	TypeModel             = "model"
	TypeEnvGroup          = "env_group"
	TypePromptOverride    = "prompt_override"
	TypeProject           = "project"
	TypeWorkspace         = "workspace"
	TypeTask              = "task"
	TypeTaskAgent         = "task_agent"
	TypeRound             = "round"
	TypeTurn              = "turn"
	TypeConversation      = "conversation"
	TypeTaskMemory        = "task_memory"
	TypeHardware          = "hardware"
)

type skillPayload struct {
	Skill domain.Skill       `json:"skill"`
	Files []domain.SkillFile `json:"files"`
}

func payloadIdempotencyKey(kind, id, format string, payload json.RawMessage) string {
	hash := sha256.Sum256(payload)
	return fmt.Sprintf("%s:%s:%s:%x", kind, format, id, hash[:12])
}

func knowledgeIdempotencyKey(item domain.KnowledgeEntry) string {
	state := fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s", item.Revision, item.Scope, item.ProjectID, item.Status, item.HelpedCount, item.StaleCount, item.FeedbackState)
	hash := sha256.Sum256([]byte(state))
	return fmt.Sprintf("knowledge:v3:%s:%x", item.ID, hash[:12])
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
	tombstones, err := database.ListSyncTombstones(ctx)
	if err != nil {
		return nil, err
	}
	for _, tombstone := range tombstones {
		result = append(result, domain.SyncObject{
			Type: tombstone.ObjectType, ID: tombstone.ObjectID, Operation: "delete",
			RemoteVersion: tombstone.Version, IdempotencyKey: tombstone.SyncKey,
		})
	}
	proposals, err := database.ListKnowledgeProposals(ctx, "", "")
	if err != nil {
		return nil, err
	}
	addProposal := func(v domain.KnowledgeProposal) error {
		v.SourceTaskID, v.SourceTurnID = "", ""
		v.Proposed.SourceTaskID, v.Proposed.SourceTurnID = "", ""
		if v.BaseEntry != nil {
			base := *v.BaseEntry
			base.SourceTaskID, base.SourceTurnID = "", ""
			v.BaseEntry = &base
		}
		version := timeVersion(v.UpdatedAt)
		if version == "" {
			version = timeVersion(v.CreatedAt)
		}
		if err := add(TypeKnowledgeProposal, v.ID, version, v); err != nil {
			return err
		}
		item := &result[len(result)-1]
		item.IdempotencyKey = payloadIdempotencyKey(TypeKnowledgeProposal, v.ID, "v1", item.Payload)
		return nil
	}
	for _, v := range proposals {
		if v.Status != domain.KnowledgeProposalPending {
			if err := addProposal(v); err != nil {
				return nil, err
			}
		}
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
		result[len(result)-1].IdempotencyKey = knowledgeIdempotencyKey(v)
	}
	for _, v := range proposals {
		if v.Status == domain.KnowledgeProposalPending {
			if err := addProposal(v); err != nil {
				return nil, err
			}
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
	registerBusinessHandlers(engine, database, "")
}

func RegisterBusinessHandlersForDevice(engine *Engine, database *store.Store, localDeviceID string) {
	registerBusinessHandlers(engine, database, localDeviceID)
}

func registerBusinessHandlers(engine *Engine, database *store.Store, localDeviceID string) {
	for _, kind := range []string{TypeProject, TypeWorkspace, TypeTask, TypeTaskAgent, TypeRound, TypeTurn, TypeConversation, TypeTaskMemory, TypeHardware, TypeKnowledgeProposal, TypeKnowledge, TypeSkill, TypeProvider, TypeModel, TypeEnvGroup, TypePromptOverride} {
		objectType := kind
		engine.Register(objectType, func(ctx context.Context, obj domain.SyncObject) error {
			if localDeviceID != "" && objectType != TypeProject && isTaskGraphType(objectType) {
				if obj.Operation == "delete" && (objectType == TypeTask || objectType == TypeWorkspace) {
					ownerDeviceID, _, _ := strings.Cut(obj.ID, ":")
					if ownerDeviceID == localDeviceID {
						return nil
					}
					return applyBusinessObject(ctx, database, obj)
				}
				var envelope graphEnvelope
				if err := decodePayload(obj, &envelope); err != nil {
					return err
				}
				if envelope.OwnerDeviceID == localDeviceID {
					return nil
				}
			}
			return applyBusinessObject(ctx, database, obj)
		})
	}
}

func isTaskGraphType(kind string) bool {
	switch kind {
	case TypeProject, TypeWorkspace, TypeTask, TypeTaskAgent, TypeRound, TypeTurn, TypeConversation, TypeTaskMemory, TypeHardware:
		return true
	default:
		return false
	}
}

func applyBusinessObject(ctx context.Context, database *store.Store, obj domain.SyncObject) error {
	if obj.Operation == "delete" && (obj.Type == TypeTask || obj.Type == TypeWorkspace) {
		return applyTaskGraphObject(ctx, database, obj)
	}
	if tombstone, err := database.SyncTombstone(ctx, obj.Type, obj.ID); err == nil {
		if obj.Operation == "delete" || !sharedVersionNewer(obj.Type, obj.SourceVersion, tombstone.Version) {
			return nil
		}
		if err := database.DeleteSyncTombstone(ctx, obj.Type, obj.ID); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	localPayload, localVersion, exists, err := currentBusinessObject(ctx, database, obj.Type, obj.ID)
	if err != nil {
		return err
	}
	if obj.Operation == "delete" {
		if exists && obj.SourceVersion != "" && sharedVersionNewer(obj.Type, localVersion, obj.SourceVersion) {
			return &ConflictError{LocalPayload: localPayload, LocalVersion: localVersion, Cause: fmt.Errorf("%w: older delete for %s %s", ErrConflict, obj.Type, obj.ID)}
		}
		return deleteBusinessObject(ctx, database, obj)
	}
	if obj.BaseVersion != "" && (!exists || obj.BaseVersion != localVersion) {
		return &ConflictError{LocalPayload: localPayload, LocalVersion: localVersion, Cause: fmt.Errorf("%w: %s %s version %q does not match %q", ErrConflict, obj.Type, obj.ID, obj.BaseVersion, localVersion)}
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
	case TypeTask, TypeTaskAgent, TypeRound, TypeTurn, TypeConversation, TypeTaskMemory, TypeHardware:
		return nil, "", false, nil
	case TypeKnowledge:
		var v domain.KnowledgeEntry
		v, err = database.Knowledge(ctx, id)
		value = v
		version = strconv.Itoa(v.Revision)
	case TypeKnowledgeProposal:
		var v domain.KnowledgeProposal
		v, err = database.KnowledgeProposal(ctx, id)
		value = v
		version = timeVersion(v.UpdatedAt)
		if version == "" {
			version = timeVersion(v.CreatedAt)
		}
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
	case TypeProject, TypeWorkspace, TypeTask, TypeTaskAgent, TypeRound, TypeTurn, TypeConversation, TypeTaskMemory, TypeHardware:
		return applyTaskGraphObject(ctx, database, obj)
	case TypeKnowledge:
		var v domain.KnowledgeEntry
		if err := decodePayload(obj, &v); err != nil {
			return err
		}
		v.ID = obj.ID
		if err := normalizeSyncedKnowledgeScope(ctx, database, &v); err != nil {
			return err
		}
		v.SourceTaskID = ""
		v.SourceTurnID = ""
		if exists {
			return database.UpdateKnowledge(ctx, v)
		}
		return database.CreateKnowledge(ctx, v)
	case TypeKnowledgeProposal:
		var v domain.KnowledgeProposal
		if err := decodePayload(obj, &v); err != nil {
			return err
		}
		v.ID = obj.ID
		v.Proposed.ID = v.EntryID
		v.SourceTaskID, v.SourceTurnID = "", ""
		v.Proposed.SourceTaskID, v.Proposed.SourceTurnID = "", ""
		if err := normalizeSyncedKnowledgeScope(ctx, database, &v.Proposed); err != nil {
			return err
		}
		if v.BaseEntry != nil {
			base := *v.BaseEntry
			base.SourceTaskID, base.SourceTurnID = "", ""
			if err := normalizeSyncedKnowledgeScope(ctx, database, &base); err != nil {
				return err
			}
			v.BaseEntry = &base
		}
		return database.ImportKnowledgeProposal(ctx, v)
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

func deleteBusinessObject(ctx context.Context, database *store.Store, obj domain.SyncObject) error {
	_, err := database.ApplySyncTombstone(ctx, obj.Type, obj.ID, obj.IdempotencyKey, obj.SourceVersion, time.Now().UTC())
	return err
}

func sharedVersionNewer(objectType, candidate, deleted string) bool {
	if candidate == "" || deleted == "" || deleted == "unknown" {
		return false
	}
	switch objectType {
	case TypeKnowledge, TypeSkill, TypeEnvGroup, TypePromptOverride:
		candidateNumber, candidateErr := strconv.ParseInt(candidate, 10, 64)
		deletedNumber, deletedErr := strconv.ParseInt(deleted, 10, 64)
		return candidateErr == nil && deletedErr == nil && candidateNumber > deletedNumber
	default:
		candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
		deletedTime, deletedErr := time.Parse(time.RFC3339Nano, deleted)
		return candidateErr == nil && deletedErr == nil && candidateTime.After(deletedTime)
	}
}

func normalizeSyncedKnowledgeScope(ctx context.Context, database *store.Store, v *domain.KnowledgeEntry) error {
	v.Scope = strings.TrimSpace(v.Scope)
	v.ProjectID = strings.TrimSpace(v.ProjectID)
	if v.ProjectID != "" {
		if _, err := database.Project(ctx, v.ProjectID); err != nil {
			return fmt.Errorf("knowledge project dependency %s: %w", v.ProjectID, err)
		}
		v.Scope = "project"
		return nil
	}
	switch v.Scope {
	case "global":
		return nil
	case "", "project":
		// Legacy profile-sync payloads removed ProjectID without changing Scope.
		// They were historically intended to be portable global fallbacks.
		v.Scope = "global"
		v.ParentID = ""
		v.ProductLineID = ""
		v.BranchScope = ""
		return nil
	default:
		return fmt.Errorf("%w: synced knowledge scope %q", store.ErrKnowledgeInvalidScope, v.Scope)
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

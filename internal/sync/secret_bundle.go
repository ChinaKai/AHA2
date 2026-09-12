package sync

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

const TypeSecretBundle = "secret_bundle"

var ErrNoPortableSecrets = errors.New("selection contains no configured portable secrets")

var bundleIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var bundleEnvPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type SecretReaderWriter interface {
	Get(string) (string, bool)
	PutMany(map[string]string) error
}

// SecretSelection is an explicit override used by focused callers and tests.
// Runner discovers all configured portable credentials when this is nil.
type SecretSelection struct {
	ProviderIDs     []string `json:"provider_ids,omitempty"`
	EnvGroupIDs     []string `json:"env_group_ids,omitempty"`
	CodexAccountIDs []string `json:"codex_account_ids,omitempty"`
}

type portableSecretBundle struct {
	Version        int                            `json:"version"`
	OwnerDeviceID  string                         `json:"owner_device_id,omitempty"`
	Providers      []portableProvider             `json:"providers,omitempty"`
	EnvGroups      []portableEnvGroup             `json:"env_groups,omitempty"`
	CodexAccounts  []portableCodexAccount         `json:"codex_accounts,omitempty"`
	Infrastructure []portableInfrastructureSecret `json:"infrastructure,omitempty"`
}
type portableProvider struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}
type portableEnvGroup struct {
	ID     string            `json:"id"`
	Values map[string]string `json:"values"`
}
type portableCodexAccount struct {
	Account domain.CodexAccount `json:"account"`
	Value   string              `json:"value"`
}

type portableInfrastructureSecret struct {
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	HardwareID  string `json:"hardware_id,omitempty"`
	Value       string `json:"value"`
}

// ExportSecretBundle reads only explicitly selected Provider, Env and Codex
// credentials. SSH, hardware and arbitrary Secret Store references are excluded.
func ExportSecretBundle(ctx context.Context, database *store.Store, secrets SecretReaderWriter, selection SecretSelection, passphrase string) (domain.SyncObject, error) {
	if database == nil || secrets == nil {
		return domain.SyncObject{}, errors.New("sync database and secret store are required")
	}
	bundle := portableSecretBundle{Version: 1}
	for _, id := range uniqueIDs(selection.ProviderIDs) {
		provider, err := database.Provider(ctx, id)
		if err != nil {
			return domain.SyncObject{}, fmt.Errorf("load provider %s: %w", id, err)
		}
		value, ok := selectedSecret(secrets, provider.CredentialRef, isProviderSecretRef)
		if ok {
			bundle.Providers = append(bundle.Providers, portableProvider{ID: id, Value: value})
		}
	}
	for _, id := range uniqueIDs(selection.EnvGroupIDs) {
		group, err := database.EnvGroup(ctx, id)
		if err != nil {
			return domain.SyncObject{}, fmt.Errorf("load env group %s: %w", id, err)
		}
		values := map[string]string{}
		for name, ref := range group.SecretRefs {
			if !bundleEnvPattern.MatchString(name) {
				return domain.SyncObject{}, fmt.Errorf("env group %s has invalid secret name", id)
			}
			value, ok := selectedSecret(secrets, ref, isEnvSecretRef, isProviderSecretRef)
			if ok {
				values[name] = value
			}
		}
		if len(values) > 0 {
			bundle.EnvGroups = append(bundle.EnvGroups, portableEnvGroup{ID: id, Values: values})
		}
	}
	for _, id := range uniqueIDs(selection.CodexAccountIDs) {
		account, err := database.CodexAccount(ctx, id)
		if err != nil {
			return domain.SyncObject{}, fmt.Errorf("load Codex account %s: %w", id, err)
		}
		value, ok := selectedSecret(secrets, account.CredentialRef, isCodexAccountSecretRef)
		if ok {
			account.CredentialRef = ""
			account.CredentialConfigured = true
			bundle.CodexAccounts = append(bundle.CodexAccounts, portableCodexAccount{Account: account, Value: value})
		}
	}
	if len(bundle.Providers)+len(bundle.EnvGroups)+len(bundle.CodexAccounts) == 0 {
		return domain.SyncObject{}, ErrNoPortableSecrets
	}
	plain, err := json.Marshal(bundle)
	if err != nil {
		return domain.SyncObject{}, err
	}
	encrypted, err := EncryptBundle(plain, passphrase)
	if err != nil {
		return domain.SyncObject{}, err
	}
	payload, err := json.Marshal(string(encrypted))
	if err != nil {
		return domain.SyncObject{}, err
	}
	mac := hmac.New(sha256.New, []byte(passphrase))
	_, _ = mac.Write(plain)
	fingerprint := hex.EncodeToString(mac.Sum(nil))
	return domain.SyncObject{Type: TypeSecretBundle, ID: "profile-secrets", Operation: "upsert", Payload: payload, IdempotencyKey: TypeSecretBundle + ":profile-secrets:" + fingerprint}, nil
}

// ExportInfrastructureSecretBundle keeps SSH and hardware credentials scoped to
// the device that owns their read-only graph. Receiving devices can only reuse
// these encrypted values through an explicit takeover operation.
func ExportInfrastructureSecretBundle(ctx context.Context, database *store.Store, secrets SecretReaderWriter, deviceID, passphrase string) (domain.SyncObject, error) {
	if database == nil || secrets == nil || !bundleIDPattern.MatchString(deviceID) {
		return domain.SyncObject{}, errors.New("sync database, secret store and device id are required")
	}
	bundle := portableSecretBundle{Version: 2, OwnerDeviceID: deviceID}
	workspaces, err := database.ListWorkspaces(ctx, "")
	if err != nil {
		return domain.SyncObject{}, err
	}
	for _, workspace := range workspaces {
		if workspace.ReadOnly || workspace.OwnerDeviceID != deviceID || !workspace.SSHPasswordConfigured {
			continue
		}
		expected := localWorkspaceSecretRef(workspace.ID)
		if workspace.SSHCredentialRef != expected {
			return domain.SyncObject{}, fmt.Errorf("workspace %s has non-portable SSH secret reference", workspace.ID)
		}
		value, ok := secrets.Get(expected)
		if !ok || value == "" {
			return domain.SyncObject{}, fmt.Errorf("workspace %s SSH credential is missing", workspace.ID)
		}
		bundle.Infrastructure = append(bundle.Infrastructure, portableInfrastructureSecret{Kind: "workspace_ssh", WorkspaceID: workspace.ID, Value: value})
	}
	tasks, err := database.ListTasks(ctx, "")
	if err != nil {
		return domain.SyncObject{}, err
	}
	for _, task := range tasks {
		if task.ReadOnly {
			continue
		}
		groups, groupErr := database.HardwareGroups(ctx, task.ID)
		if groupErr != nil {
			return domain.SyncObject{}, groupErr
		}
		for _, group := range groups {
			if !group.PasswordConfigured {
				continue
			}
			expected := localHardwareSecretRef(task.ID, group.ID)
			if group.CredentialRef != expected {
				return domain.SyncObject{}, fmt.Errorf("hardware %s/%s has non-portable secret reference", task.ID, group.ID)
			}
			value, ok := secrets.Get(expected)
			if !ok || value == "" {
				return domain.SyncObject{}, fmt.Errorf("hardware %s/%s credential is missing", task.ID, group.ID)
			}
			bundle.Infrastructure = append(bundle.Infrastructure, portableInfrastructureSecret{Kind: "hardware", TaskID: task.ID, HardwareID: group.ID, Value: value})
		}
	}
	if len(bundle.Infrastructure) == 0 {
		return domain.SyncObject{}, ErrNoPortableSecrets
	}
	return encryptSecretBundle(bundle, "infrastructure-secrets-"+deviceID, passphrase)
}

func encryptSecretBundle(bundle portableSecretBundle, id, passphrase string) (domain.SyncObject, error) {
	plain, err := json.Marshal(bundle)
	if err != nil {
		return domain.SyncObject{}, err
	}
	encrypted, err := EncryptBundle(plain, passphrase)
	if err != nil {
		return domain.SyncObject{}, err
	}
	payload, err := json.Marshal(string(encrypted))
	if err != nil {
		return domain.SyncObject{}, err
	}
	mac := hmac.New(sha256.New, []byte(passphrase))
	_, _ = mac.Write(plain)
	fingerprint := hex.EncodeToString(mac.Sum(nil))
	return domain.SyncObject{Type: TypeSecretBundle, ID: id, Operation: "upsert", Payload: payload, IdempotencyKey: TypeSecretBundle + ":" + id + ":" + fingerprint}, nil
}

func PortableSecretSelection(ctx context.Context, database *store.Store) (SecretSelection, error) {
	providers, err := database.ListProviders(ctx)
	if err != nil {
		return SecretSelection{}, err
	}
	groups, err := database.ListEnvGroups(ctx)
	if err != nil {
		return SecretSelection{}, err
	}
	accounts, err := database.ListCodexAccounts(ctx)
	if err != nil {
		return SecretSelection{}, err
	}
	selection := SecretSelection{}
	for _, item := range providers {
		if item.CredentialConfigured {
			selection.ProviderIDs = append(selection.ProviderIDs, item.ID)
		}
	}
	for _, item := range groups {
		if item.SecretConfigured {
			selection.EnvGroupIDs = append(selection.EnvGroupIDs, item.ID)
		}
	}
	for _, item := range accounts {
		if item.CredentialConfigured {
			selection.CodexAccountIDs = append(selection.CodexAccountIDs, item.ID)
		}
	}
	return selection, nil
}

func RegisterSecretBundleHandler(engine *Engine, database *store.Store, secrets SecretReaderWriter, passphrase string) {
	engine.Register(TypeSecretBundle, func(ctx context.Context, obj domain.SyncObject) error {
		return ApplySecretBundle(ctx, database, secrets, obj, passphrase)
	})
}

func selectedSecret(secrets SecretReaderWriter, ref string, allow ...func(string) bool) (string, bool) {
	if ref == "" {
		return "", false
	}
	for _, allowed := range allow {
		if allowed(ref) {
			value, ok := secrets.Get(ref)
			return value, ok && value != ""
		}
	}
	return "", false
}

func providerSecretRef(id string) string {
	if !bundleIDPattern.MatchString(id) {
		return ""
	}
	return "provider/" + id + "/credential"
}

func envSecretRef(id, name string) string {
	if !bundleIDPattern.MatchString(id) || !bundleEnvPattern.MatchString(name) {
		return ""
	}
	return "env/" + id + "/" + name
}

func codexAccountSecretRef(id string) string {
	if !bundleIDPattern.MatchString(id) {
		return ""
	}
	return "codex-account/" + id + "/auth"
}

func isProviderSecretRef(ref string) bool {
	parts := strings.Split(ref, "/")
	return len(parts) == 3 && parts[0] == "provider" && bundleIDPattern.MatchString(parts[1]) && parts[2] == "credential"
}

func isEnvSecretRef(ref string) bool {
	parts := strings.Split(ref, "/")
	return len(parts) == 3 && parts[0] == "env" && bundleIDPattern.MatchString(parts[1]) && bundleEnvPattern.MatchString(parts[2])
}

func isCodexAccountSecretRef(ref string) bool {
	parts := strings.Split(ref, "/")
	return len(parts) == 3 && parts[0] == "codex-account" && bundleIDPattern.MatchString(parts[1]) && parts[2] == "auth"
}

func localWorkspaceSecretRef(workspaceID string) string {
	if !bundleIDPattern.MatchString(workspaceID) {
		return ""
	}
	return "workspace/" + workspaceID + "/ssh/credential"
}

func localHardwareSecretRef(taskID, hardwareID string) string {
	if !bundleIDPattern.MatchString(taskID) || !bundleIDPattern.MatchString(hardwareID) {
		return ""
	}
	return "hardware/" + taskID + "/" + hardwareID + "/credential"
}

func mirrorSecretRef(kind string, values ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(kind))
	for _, value := range values {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return "sync-mirror/" + kind + "/" + hex.EncodeToString(hash.Sum(nil)[:16]) + "/credential"
}

// MirrorWorkspaceSecretRef returns the non-active Secret Store location for a
// remote workspace mirror. The public mirror ID is deterministic per owner.
func MirrorWorkspaceSecretRef(publicWorkspaceID string) string {
	return mirrorSecretRef("workspace", publicWorkspaceID)
}

// MirrorHardwareSecretRef returns the non-active Secret Store location for a
// hardware credential that belongs to another device's read-only Task graph.
func MirrorHardwareSecretRef(ownerDeviceID, sourceTaskID, hardwareID string) string {
	return mirrorSecretRef("hardware", ownerDeviceID, sourceTaskID, hardwareID)
}

// ApplySecretBundle decrypts and validates the complete bundle before writing.
// Existing local Secret Store references are retained.
func ApplySecretBundle(ctx context.Context, database *store.Store, secrets SecretReaderWriter, obj domain.SyncObject, passphrase string) error {
	if obj.Type != TypeSecretBundle || obj.Operation != "upsert" {
		return errors.New("object is not an upsert secret bundle")
	}
	var encrypted string
	if err := json.Unmarshal(obj.Payload, &encrypted); err != nil || encrypted == "" {
		return errors.New("secret bundle payload is not opaque ciphertext")
	}
	plain, err := DecryptBundle([]byte(encrypted), passphrase)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	var bundle portableSecretBundle
	if err := decoder.Decode(&bundle); err != nil || (bundle.Version != 1 && bundle.Version != 2) {
		return errors.New("invalid secret bundle plaintext")
	}
	writes := map[string]string{}
	providerUpdates := map[string]domain.Provider{}
	envUpdates := map[string]domain.EnvGroup{}
	accountUpdates := map[string]domain.CodexAccount{}
	for _, entry := range bundle.Providers {
		if !validBundleIdentity(entry.ID, entry.Value) {
			return errors.New("invalid provider secret entry")
		}
		provider, err := database.Provider(ctx, entry.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("load provider %s: %w", entry.ID, err)
		}
		ref := provider.CredentialRef
		if ref == "" {
			ref = providerSecretRef(entry.ID)
		} else if !isProviderSecretRef(ref) {
			return fmt.Errorf("provider %s has non-portable local secret reference", entry.ID)
		}
		writes[ref] = entry.Value
		provider.CredentialRef = ref
		provider.CredentialConfigured = true
		providerUpdates[entry.ID] = provider
	}
	for _, entry := range bundle.EnvGroups {
		if !bundleIDPattern.MatchString(entry.ID) || len(entry.Values) == 0 {
			return errors.New("invalid env secret entry")
		}
		group, err := database.EnvGroup(ctx, entry.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("load env group %s: %w", entry.ID, err)
		}
		if group.SecretRefs == nil {
			group.SecretRefs = map[string]string{}
		}
		for name, value := range entry.Values {
			if !bundleEnvPattern.MatchString(name) || value == "" {
				return errors.New("invalid env secret value")
			}
			ref := group.SecretRefs[name]
			if ref == "" {
				ref = envSecretRef(entry.ID, name)
			} else if !isEnvSecretRef(ref) && !isProviderSecretRef(ref) {
				return fmt.Errorf("env group %s has non-portable local secret reference", entry.ID)
			}
			group.SecretRefs[name] = ref
			writes[ref] = value
		}
		group.SecretConfigured = true
		envUpdates[entry.ID] = group
	}
	for _, entry := range bundle.CodexAccounts {
		if !validBundleIdentity(entry.Account.ID, entry.Value) {
			return errors.New("invalid Codex account secret entry")
		}
		account := entry.Account
		local, err := database.CodexAccount(ctx, account.ID)
		if err == nil {
			account.CredentialRef = local.CredentialRef
			account.CreatedAt = local.CreatedAt
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if account.CredentialRef == "" {
			account.CredentialRef = codexAccountSecretRef(account.ID)
		} else if !isCodexAccountSecretRef(account.CredentialRef) {
			return fmt.Errorf("Codex account %s has non-portable local secret reference", account.ID)
		}
		account.CredentialConfigured = true
		if account.CreatedAt.IsZero() {
			account.CreatedAt = time.Now().UTC()
		}
		if account.UpdatedAt.IsZero() {
			account.UpdatedAt = time.Now().UTC()
		}
		writes[account.CredentialRef] = entry.Value
		accountUpdates[account.ID] = account
	}
	if len(bundle.Infrastructure) > 0 {
		if bundle.Version != 2 || !bundleIDPattern.MatchString(bundle.OwnerDeviceID) {
			return errors.New("invalid infrastructure secret bundle")
		}
		for _, entry := range bundle.Infrastructure {
			if entry.Value == "" {
				return errors.New("invalid infrastructure secret value")
			}
			switch entry.Kind {
			case "workspace_ssh":
				if !bundleIDPattern.MatchString(entry.WorkspaceID) || entry.TaskID != "" || entry.HardwareID != "" {
					return errors.New("invalid workspace SSH secret entry")
				}
				writes[MirrorWorkspaceSecretRef(syncedWorkspaceID(bundle.OwnerDeviceID, entry.WorkspaceID))] = entry.Value
			case "hardware":
				if !bundleIDPattern.MatchString(entry.TaskID) || !bundleIDPattern.MatchString(entry.HardwareID) || entry.WorkspaceID != "" {
					return errors.New("invalid hardware secret entry")
				}
				writes[MirrorHardwareSecretRef(bundle.OwnerDeviceID, entry.TaskID, entry.HardwareID)] = entry.Value
			default:
				return errors.New("invalid infrastructure secret kind")
			}
		}
	}
	if len(writes) == 0 {
		if len(bundle.Providers)+len(bundle.EnvGroups)+len(bundle.CodexAccounts) > 0 {
			return nil
		}
		return errors.New("secret bundle contains no secrets")
	}
	if err := secrets.PutMany(writes); err != nil {
		return fmt.Errorf("store imported secrets: %w", err)
	}
	for _, provider := range providerUpdates {
		if err := database.UpsertProvider(ctx, provider); err != nil {
			return err
		}
	}
	for _, group := range envUpdates {
		if err := database.UpsertEnvGroup(ctx, group); err != nil {
			return err
		}
	}
	for _, account := range accountUpdates {
		if err := database.UpsertCodexAccount(ctx, account); err != nil {
			return err
		}
	}
	return nil
}

func validBundleIdentity(id, value string) bool {
	return bundleIDPattern.MatchString(id) && value != ""
}
func uniqueIDs(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, id := range values {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}

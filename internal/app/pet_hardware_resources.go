package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardwareconfig"
)

// PetHardwareResourceView is the pet-facing hardware resource shape. It never
// carries CredentialRef or plaintext credentials; password_configured is the
// only credential-derived field.
type PetHardwareResourceView struct {
	ID                 string                       `json:"id"`
	Description        string                       `json:"description"`
	Mode               string                       `json:"mode"`
	Access             string                       `json:"access"`
	Serial             domain.HardwareSerialConfig  `json:"serial"`
	Network            domain.HardwareNetworkConfig `json:"network"`
	Username           string                       `json:"username"`
	PasswordConfigured bool                         `json:"password_configured"`
	ADBPairedAt        string                       `json:"adb_paired_at,omitempty"`
	BindingCount       int                          `json:"binding_count"`
	UpdatedAt          time.Time                    `json:"updated_at"`
}

type PetHardwareSerialInput struct {
	Device   *string `json:"device"`
	Baudrate *int    `json:"baudrate"`
}

type PetHardwareNetworkInput struct {
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	Protocol *string `json:"protocol"`
	SSHAuth  *string `json:"ssh_auth"`
}

type PetHardwareResourceInput struct {
	ID          string                   `json:"id"`
	ResourceID  string                   `json:"resource_id"`
	Description *string                  `json:"description"`
	Mode        *string                  `json:"mode"`
	Access      *string                  `json:"access"`
	Username    *string                  `json:"username"`
	Serial      *PetHardwareSerialInput  `json:"serial"`
	Network     *PetHardwareNetworkInput `json:"network"`
}

var petHardwareCredentialFields = map[string]bool{
	"password": true, "credential_ref": true, "clear_secret": true, "clear_password": true,
}

var errPetHardwareCredentialOwnerOnly = fmt.Errorf(
	"hardware_credential_owner_only: 宠物不能设置硬件凭据，请由 Owner 在 Web/Secret Store 配置",
)

var errPetHardwareCredentialTargetLocked = fmt.Errorf(
	"hardware_credential_owner_only: 该资源已由 Owner 配置凭据，宠物不能修改连接目标；请由 Owner 在 Web/Secret Store 中调整",
)

func rejectPetHardwareCredentialFields(raw map[string]json.RawMessage) error {
	// JSON field matching in Go is case-insensitive, so reject case variants too;
	// otherwise a pet could pass "Password" and assume it was applied.
	for field := range raw {
		if petHardwareCredentialFields[strings.ToLower(field)] {
			return errPetHardwareCredentialOwnerOnly
		}
	}
	return nil
}

// decodePetHardwareResourceInput rejects owner-only credential fields before
// decoding so a pet can never quietly write a secret through these tools.
func decodePetHardwareResourceInput(args json.RawMessage) (PetHardwareResourceInput, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return PetHardwareResourceInput{}, fmt.Errorf("hardware_resource_invalid: 请求格式无效")
	}
	if err := rejectPetHardwareCredentialFields(raw); err != nil {
		return PetHardwareResourceInput{}, err
	}
	// Reject credentials in nested connection objects too, even though the
	// decoder would otherwise silently drop them. Section names are matched
	// case-insensitively for the same reason as field names.
	for section, value := range raw {
		switch strings.ToLower(section) {
		case "serial", "network":
		default:
			continue
		}
		if len(value) == 0 {
			continue
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(value, &nested); err == nil {
			if err := rejectPetHardwareCredentialFields(nested); err != nil {
				return PetHardwareResourceInput{}, err
			}
		}
	}
	var input PetHardwareResourceInput
	if err := json.Unmarshal(args, &input); err != nil {
		return PetHardwareResourceInput{}, fmt.Errorf("hardware_resource_invalid: 请求格式无效")
	}
	return input, nil
}

func (s *Service) petManageSystemAssistant(ctx context.Context) (domain.OwnerAssistant, error) {
	assistant, err := s.petAssistant(ctx)
	if err != nil {
		return domain.OwnerAssistant{}, err
	}
	if !assistant.PetAllowManageSystem {
		return domain.OwnerAssistant{}, fmt.Errorf("主人未开启管理系统设置权限")
	}
	return assistant, nil
}

func (input PetHardwareResourceInput) applyTo(base hardwareconfig.GroupPayload) hardwareconfig.GroupPayload {
	if input.Description != nil {
		base.Description = *input.Description
	}
	if input.Mode != nil {
		base.Mode = *input.Mode
	}
	if input.Access != nil {
		base.Access = *input.Access
	}
	if input.Username != nil {
		base.Username = *input.Username
	}
	if input.Serial != nil {
		if input.Serial.Device != nil {
			base.Serial.Device = *input.Serial.Device
		}
		if input.Serial.Baudrate != nil {
			base.Serial.Baudrate = *input.Serial.Baudrate
		}
	}
	if input.Network != nil {
		if input.Network.Host != nil {
			base.Network.Host = *input.Network.Host
		}
		if input.Network.Port != nil {
			base.Network.Port = *input.Network.Port
		}
		if input.Network.Protocol != nil {
			base.Network.Protocol = *input.Network.Protocol
		}
		if input.Network.SSHAuth != nil {
			base.Network.SSHAuth = *input.Network.SSHAuth
		}
	}
	return base
}

func validatePetHardwareResourceInput(payload hardwareconfig.GroupPayload, input PetHardwareResourceInput) error {
	mode := strings.TrimSpace(payload.Mode)
	if mode != domain.HardwareModeSerial && mode != domain.HardwareModeNetwork {
		return fmt.Errorf("hardware_resource_invalid: 硬件资源必须选择 serial 或 network 中的一种连接方式")
	}
	if mode == domain.HardwareModeSerial {
		if strings.TrimSpace(payload.Serial.Device) == "" {
			return fmt.Errorf("hardware_resource_invalid: 串口硬件资源必须提供 serial.device")
		}
		if payload.Serial.Baudrate <= 0 {
			return fmt.Errorf("hardware_resource_invalid: 串口硬件资源必须提供 serial.baudrate 且大于 0")
		}
	}
	if mode == domain.HardwareModeNetwork {
		if strings.TrimSpace(payload.Network.Host) == "" {
			return fmt.Errorf("hardware_resource_invalid: 网络硬件资源必须提供 network.host")
		}
		if input.Network != nil && input.Network.Port != nil &&
			(*input.Network.Port <= 0 || *input.Network.Port > 65535) {
			return fmt.Errorf("hardware_resource_invalid: 网络端口必须在 1 到 65535 之间")
		}
	}
	return nil
}

func petHardwareResourceFromGroup(group domain.HardwareGroup) domain.HardwareResource {
	resource := domain.HardwareResource{
		ID: group.ID, Description: group.Description, Mode: group.Mode, Serial: group.Serial,
		Network: group.Network, Username: group.Username, CredentialRef: group.CredentialRef,
		PasswordConfigured: group.PasswordConfigured, Access: group.Access,
		CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt,
	}
	if strings.TrimSpace(resource.Description) == "" {
		resource.Description = resource.ID
	}
	if resource.Network.Protocol == domain.HardwareProtocolADB {
		resource.Username = ""
		resource.CredentialRef = ""
		resource.PasswordConfigured = false
	}
	return resource
}

func petHardwareResourceView(item domain.HardwareResource) PetHardwareResourceView {
	return PetHardwareResourceView{
		ID: item.ID, Description: item.Description, Mode: item.Mode, Access: item.Access,
		Serial: item.Serial, Network: item.Network, Username: item.Username,
		PasswordConfigured: item.PasswordConfigured, ADBPairedAt: item.ADBPairedAt,
		BindingCount: item.BindingCount, UpdatedAt: item.UpdatedAt,
	}
}

func petHardwareResourceChangedFields(before, after domain.HardwareResource) []string {
	changed := make([]string, 0, 10)
	if before.Description != after.Description {
		changed = append(changed, "description")
	}
	if before.Mode != after.Mode {
		changed = append(changed, "mode")
	}
	if before.Access != after.Access {
		changed = append(changed, "access")
	}
	if before.Serial.Device != after.Serial.Device {
		changed = append(changed, "serial.device")
	}
	if before.Serial.Baudrate != after.Serial.Baudrate {
		changed = append(changed, "serial.baudrate")
	}
	if before.Network.Host != after.Network.Host {
		changed = append(changed, "network.host")
	}
	if before.Network.Port != after.Network.Port {
		changed = append(changed, "network.port")
	}
	if before.Network.Protocol != after.Network.Protocol {
		changed = append(changed, "network.protocol")
	}
	if before.Network.SSHAuth != after.Network.SSHAuth {
		changed = append(changed, "network.ssh_auth")
	}
	if before.Username != after.Username {
		changed = append(changed, "username")
	}
	return changed
}

func (s *Service) auditPetHardwareResource(ctx context.Context, action string, resource domain.HardwareResource, changedFields []string) {
	// Details deliberately omit credentials and CredentialRef.
	data := map[string]any{
		"resource_id": resource.ID, "description": resource.Description,
		"mode": resource.Mode, "access": resource.Access, "changed_fields": changedFields,
	}
	_ = s.store.AppendAudit(
		ctx, "pet", action, "hardware_resource", resource.ID, data,
		s.now().UTC().Format(time.RFC3339Nano),
	)
}

// disconnectPetHardwareResource mirrors the owner HTTP handler: every binding of
// the old resource is disconnected on both transports the old resource supports.
func (s *Service) disconnectPetHardwareResource(ctx context.Context, resource domain.HardwareResource) {
	if s.petHardware == nil {
		return
	}
	bindings, err := s.store.HardwareResourceBindings(ctx, resource.ID)
	if err != nil {
		return
	}
	for _, binding := range bindings {
		if resource.Supports(domain.HardwareTransportSerial) {
			s.petHardware.Disconnect(binding.TaskID, binding.ID, domain.HardwareTransportSerial)
		}
		if resource.Supports(domain.HardwareTransportNetwork) {
			s.petHardware.Disconnect(binding.TaskID, binding.ID, domain.HardwareTransportNetwork)
		}
	}
}

func petHardwareResourceHasOwnerCredential(item domain.HardwareResource) bool {
	return item.PasswordConfigured || strings.TrimSpace(item.CredentialRef) != ""
}

// petHardwareResourceTargetChanged reports whether the update would redirect the
// resource's stored Owner credential to a different endpoint or principal.
func petHardwareResourceTargetChanged(before, after domain.HardwareResource) bool {
	return before.Mode != after.Mode ||
		before.Serial.Device != after.Serial.Device ||
		before.Network.Host != after.Network.Host ||
		before.Network.Port != after.Network.Port ||
		before.Network.Protocol != after.Network.Protocol ||
		before.Username != after.Username
}

func (s *Service) PetCreateHardwareResource(ctx context.Context, input PetHardwareResourceInput) (PetHardwareResourceView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetHardwareResourceView{}, err
	}
	now := s.now().UTC()
	id := hardwareconfig.NormalizeID(input.ID)
	if id == "" {
		id = domain.NewID("hardware_resource")
	} else if _, err := s.store.HardwareResource(ctx, id); err == nil {
		return PetHardwareResourceView{}, fmt.Errorf("hardware_resource_exists: 硬件资源 %s 已存在", id)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PetHardwareResourceView{}, fmt.Errorf("create_hardware_resource_failed: %w", err)
	}
	payload := input.applyTo(hardwareconfig.GroupPayload{ID: id})
	if err := validatePetHardwareResourceInput(payload, input); err != nil {
		return PetHardwareResourceView{}, err
	}
	group, err := hardwareconfig.NormalizeGroup("", id, 0, payload, domain.HardwareGroup{}, now)
	if err != nil {
		return PetHardwareResourceView{}, fmt.Errorf("hardware_resource_invalid: %w", err)
	}
	resource := petHardwareResourceFromGroup(group)
	if err := s.store.UpsertHardwareResource(ctx, resource); err != nil {
		return PetHardwareResourceView{}, fmt.Errorf("create_hardware_resource_failed: %w", err)
	}
	stored, err := s.store.HardwareResource(ctx, resource.ID)
	if err == nil {
		resource = stored
	}
	s.auditPetHardwareResource(ctx, "pet.hardware_resource.create", resource, petHardwareResourceChangedFields(domain.HardwareResource{}, resource))
	return petHardwareResourceView(resource), nil
}

func (s *Service) PetUpdateHardwareResource(ctx context.Context, input PetHardwareResourceInput) (PetHardwareResourceView, error) {
	if _, err := s.petManageSystemAssistant(ctx); err != nil {
		return PetHardwareResourceView{}, err
	}
	resourceID := hardwareconfig.NormalizeID(input.ResourceID)
	if resourceID == "" {
		return PetHardwareResourceView{}, fmt.Errorf("hardware_resource_invalid: resource_id 不能为空")
	}
	existing, err := s.store.HardwareResource(ctx, resourceID)
	if err != nil {
		return PetHardwareResourceView{}, fmt.Errorf("hardware_resource_not_found: 硬件资源 %s 不存在", resourceID)
	}
	payload := input.applyTo(hardwareconfig.GroupPayload{
		ID: existing.ID, Description: existing.Description, Mode: existing.Mode,
		Access: existing.Access, Username: existing.Username,
		Serial: existing.Serial, Network: existing.Network,
	})
	if err := validatePetHardwareResourceInput(payload, input); err != nil {
		return PetHardwareResourceView{}, err
	}
	group, err := hardwareconfig.NormalizeGroup("", existing.ID, 0, payload, domain.HardwareGroup{
		ID: existing.ID, Description: existing.Description, Mode: existing.Mode,
		Serial: existing.Serial, Network: existing.Network, Username: existing.Username,
		CredentialRef: existing.CredentialRef, PasswordConfigured: existing.PasswordConfigured,
		Access: existing.Access, CreatedAt: existing.CreatedAt, UpdatedAt: existing.UpdatedAt,
	}, s.now().UTC())
	if err != nil {
		return PetHardwareResourceView{}, fmt.Errorf("hardware_resource_invalid: %w", err)
	}
	resource := petHardwareResourceFromGroup(group)
	// ADB pairing only remains valid while the same ADB host stays configured.
	if group.Mode != domain.HardwareModeNetwork ||
		group.Network.Protocol != domain.HardwareProtocolADB ||
		existing.Network.Host != group.Network.Host {
		resource.ADBPairedAt = ""
	} else {
		resource.ADBPairedAt = existing.ADBPairedAt
	}
	// The pet must not be able to point an Owner-configured credential at a new
	// endpoint (for example its own host), where auto-login would send the
	// stored password. Reject before any write so the old binding stays intact.
	if petHardwareResourceHasOwnerCredential(existing) && petHardwareResourceTargetChanged(existing, resource) {
		return PetHardwareResourceView{}, errPetHardwareCredentialTargetLocked
	}
	if err := s.store.UpsertHardwareResource(ctx, resource); err != nil {
		return PetHardwareResourceView{}, fmt.Errorf("update_hardware_resource_failed: %w", err)
	}
	s.disconnectPetHardwareResource(ctx, existing)
	// Mirror the owner handler: drop the old owner-configured secret once the
	// resource no longer references it (for example after switching to ADB).
	if s.secrets != nil && existing.CredentialRef != "" && existing.CredentialRef != resource.CredentialRef {
		shared, sharedErr := s.store.HardwareCredentialReferenced(ctx, existing.CredentialRef, resource.ID)
		if sharedErr == nil && !shared {
			_ = s.secrets.DeleteMany([]string{existing.CredentialRef})
		}
	}
	stored, err := s.store.HardwareResource(ctx, resource.ID)
	if err == nil {
		resource = stored
	}
	s.auditPetHardwareResource(ctx, "pet.hardware_resource.update", resource, petHardwareResourceChangedFields(existing, resource))
	return petHardwareResourceView(resource), nil
}

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/hardwareconfig"
)

type hardwareGroupPayload = hardwareconfig.GroupPayload

func (s *Server) hardwareSerialPorts(writer http.ResponseWriter, _ *http.Request) {
	if s.hardware == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "ports": []domain.SerialPort{}})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "ports": s.hardware.SerialPorts()})
}

func (s *Server) listHardwareResources(writer http.ResponseWriter, request *http.Request) {
	resources, err := s.store.HardwareResources(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "hardware_resources_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "resources": resources})
}

func resourceFromPayload(
	id string,
	raw hardwareGroupPayload,
	existing domain.HardwareResource,
	now time.Time,
) (domain.HardwareResource, error) {
	group, err := normalizeHardwareGroup("", id, 0, raw, domain.HardwareGroup{
		ID: existing.ID, Description: existing.Description, Mode: existing.Mode,
		Serial: existing.Serial, Network: existing.Network, Username: existing.Username,
		CredentialRef: existing.CredentialRef, PasswordConfigured: existing.PasswordConfigured,
		Access: existing.Access, CreatedAt: existing.CreatedAt, UpdatedAt: existing.UpdatedAt,
	}, now)
	if err != nil {
		return domain.HardwareResource{}, err
	}
	if group.Mode != domain.HardwareModeSerial && group.Mode != domain.HardwareModeNetwork {
		return domain.HardwareResource{}, fmt.Errorf("硬件资源必须选择串口或网络中的一种连接方式")
	}
	pairedAt := existing.ADBPairedAt
	if group.Mode != domain.HardwareModeNetwork || group.Network.Protocol != domain.HardwareProtocolADB || existing.Network.Host != group.Network.Host {
		pairedAt = ""
	}
	return domain.HardwareResource{
		ID: group.ID, Description: group.Description, Mode: group.Mode, Serial: group.Serial,
		Network: group.Network, Username: group.Username, CredentialRef: group.CredentialRef,
		PasswordConfigured: group.PasswordConfigured, ADBPairedAt: pairedAt, Access: group.Access,
		CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt,
	}, nil
}

func (s *Server) disconnectHardwareResource(ctx context.Context, resource domain.HardwareResource) {
	if s.hardware == nil {
		return
	}
	bindings, err := s.store.HardwareResourceBindings(ctx, resource.ID)
	if err != nil {
		return
	}
	for _, binding := range bindings {
		if resource.Supports(domain.HardwareTransportSerial) {
			s.hardware.Disconnect(binding.TaskID, binding.ID, domain.HardwareTransportSerial)
		}
		if resource.Supports(domain.HardwareTransportNetwork) {
			s.hardware.Disconnect(binding.TaskID, binding.ID, domain.HardwareTransportNetwork)
		}
	}
}

func (s *Server) createHardwareResource(writer http.ResponseWriter, request *http.Request) {
	var payload hardwareGroupPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	now := time.Now().UTC()
	id := normalizeHardwareID(payload.ID)
	if id == "" {
		id = domain.NewID("hardware_resource")
	}
	resource, err := resourceFromPayload(id, payload, domain.HardwareResource{}, now)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "hardware_resource_invalid", "message": err.Error()})
		return
	}
	if strings.TrimSpace(resource.Description) == "" {
		resource.Description = resource.ID
	}
	if resource.Network.Protocol == domain.HardwareProtocolADB {
		resource.Username = ""
		resource.CredentialRef = ""
		resource.PasswordConfigured = false
		payload.Password = ""
		payload.ClearSecret = true
	}
	var secretRef string
	if payload.ClearSecret {
		resource.CredentialRef = ""
		resource.PasswordConfigured = false
	} else if payload.Password != "" {
		if s.secrets == nil {
			writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
			return
		}
		secretRef = "hardware-resource/" + resource.ID + "/credential"
		resource.CredentialRef = secretRef
		resource.PasswordConfigured = true
		if err := s.secrets.PutMany(map[string]string{secretRef: payload.Password}); err != nil {
			writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
			return
		}
	}
	if err := s.store.UpsertHardwareResource(request.Context(), resource); err != nil {
		if secretRef != "" && s.secrets != nil {
			_ = s.secrets.DeleteMany([]string{secretRef})
		}
		writeError(writer, http.StatusInternalServerError, "create_hardware_resource_failed")
		return
	}
	s.audit(request, "hardware_resource.create", "hardware_resource", resource.ID, map[string]any{
		"mode": resource.Mode, "access": resource.Access,
	})
	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "resource": resource})
}

func (s *Server) updateHardwareResource(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	existing, err := s.store.HardwareResource(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "hardware_resource_not_found")
		return
	}
	var payload hardwareGroupPayload
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	resource, err := resourceFromPayload(id, payload, existing, time.Now().UTC())
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "hardware_resource_invalid", "message": err.Error()})
		return
	}
	if strings.TrimSpace(resource.Description) == "" {
		resource.Description = resource.ID
	}
	if resource.Network.Protocol == domain.HardwareProtocolADB {
		resource.Username = ""
		resource.CredentialRef = ""
		resource.PasswordConfigured = false
		payload.Password = ""
		payload.ClearSecret = true
	}
	oldSecretRef := existing.CredentialRef
	var newSecretRef string
	if payload.ClearSecret {
		resource.CredentialRef = ""
		resource.PasswordConfigured = false
	} else if payload.Password != "" {
		if s.secrets == nil {
			writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
			return
		}
		newSecretRef = "hardware-resource/" + resource.ID + "/credential"
		resource.CredentialRef = newSecretRef
		resource.PasswordConfigured = true
		if err := s.secrets.PutMany(map[string]string{newSecretRef: payload.Password}); err != nil {
			writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
			return
		}
	}
	if err := s.store.UpsertHardwareResource(request.Context(), resource); err != nil {
		writeError(writer, http.StatusInternalServerError, "update_hardware_resource_failed")
		return
	}
	s.disconnectHardwareResource(request.Context(), existing)
	if s.secrets != nil && oldSecretRef != "" && oldSecretRef != resource.CredentialRef {
		shared, sharedErr := s.store.HardwareCredentialReferenced(request.Context(), oldSecretRef, resource.ID)
		if sharedErr == nil && !shared {
			_ = s.secrets.DeleteMany([]string{oldSecretRef})
		}
	}
	s.audit(request, "hardware_resource.update", "hardware_resource", resource.ID, map[string]any{
		"mode": resource.Mode, "access": resource.Access, "bindings": existing.BindingCount,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "resource": resource})
}

func (s *Server) deleteHardwareResource(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	resource, err := s.store.HardwareResource(request.Context(), id)
	if err != nil {
		writeError(writer, http.StatusNotFound, "hardware_resource_not_found")
		return
	}
	s.disconnectHardwareResource(request.Context(), resource)
	if err := s.store.DeleteHardwareResource(request.Context(), id); err != nil {
		writeError(writer, http.StatusInternalServerError, "delete_hardware_resource_failed")
		return
	}
	if s.secrets != nil && resource.CredentialRef != "" {
		shared, sharedErr := s.store.HardwareCredentialReferenced(request.Context(), resource.CredentialRef, resource.ID)
		if sharedErr == nil && !shared {
			_ = s.secrets.DeleteMany([]string{resource.CredentialRef})
		}
	}
	s.audit(request, "hardware_resource.delete", "hardware_resource", id, map[string]any{
		"bindings": resource.BindingCount, "mode": resource.Mode,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "deleted_bindings": resource.BindingCount})
}

func (s *Server) pairHardwareADB(writer http.ResponseWriter, request *http.Request) {
	resource, err := s.store.HardwareResource(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "hardware_resource_not_found")
		return
	}
	if resource.Mode != domain.HardwareModeNetwork || resource.Network.Protocol != domain.HardwareProtocolADB {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_pair_unavailable", "message": "当前硬件资源不是 ADB 网络设备"})
		return
	}
	var payload struct {
		PairingPort int    `json:"pairing_port"`
		PairingCode string `json:"pairing_code"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	pairingCode := strings.TrimSpace(payload.PairingCode)
	if payload.PairingPort < 1 || payload.PairingPort > 65535 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_pairing_port_invalid", "message": "ADB 配对端口必须在 1 到 65535 之间"})
		return
	}
	if len(pairingCode) != 6 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_pairing_code_invalid", "message": "ADB 配对码必须是 6 位数字"})
		return
	}
	for _, character := range pairingCode {
		if character < '0' || character > '9' {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_pairing_code_invalid", "message": "ADB 配对码必须是 6 位数字"})
			return
		}
	}
	endpoint := net.JoinHostPort(resource.Network.Host, fmt.Sprint(payload.PairingPort))
	ctx, cancel := context.WithTimeout(request.Context(), 35*time.Second)
	defer cancel()
	output, err := s.pairADB(ctx, endpoint, pairingCode)
	if err != nil {
		s.audit(request, "hardware_resource.adb_pair_failed", "hardware_resource", resource.ID, map[string]any{"connection_endpoint": net.JoinHostPort(resource.Network.Host, fmt.Sprint(resource.Network.Port))})
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "adb_pair_failed",
			"message": adbPairingFailureMessage(err),
			"details": err.Error(),
		})
		return
	}
	resource.ADBPairedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.store.UpsertHardwareResource(request.Context(), resource); err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"ok": false, "error": "adb_pair_state_save_failed", "message": "设备已配对，但保存配对状态失败"})
		return
	}
	s.audit(request, "hardware_resource.adb_pair", "hardware_resource", resource.ID, map[string]any{"connection_endpoint": net.JoinHostPort(resource.Network.Host, fmt.Sprint(resource.Network.Port))})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "message": "Android 设备配对成功", "output": output, "resource": resource})
}

func adbPairingFailureMessage(err error) string {
	detail := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(detail, "adb executable not found"),
		strings.Contains(detail, "configured adb executable is unavailable"):
		return "找不到 ADB。请安装 Android Platform-Tools，或在 AHA2 运行账户中把 AHA2_ADB_PATH 设置为 adb.exe 的完整路径，然后重启 AHA2 User。"
	case strings.Contains(detail, "failed to authenticate"),
		strings.Contains(detail, "wrong pairing"),
		strings.Contains(detail, "pairing code"):
		return "配对码不正确或已经过期。请在手机的无线调试页面重新生成配对码后再试。"
	case strings.Contains(detail, "connection refused"),
		strings.Contains(detail, "failed to connect"),
		strings.Contains(detail, "cannot connect"),
		strings.Contains(detail, "unable to connect"),
		strings.Contains(detail, "no route"):
		return "无法连接到手机配对端口。请确认手机和电脑在同一网络、无线调试仍开启，并使用当前显示的配对端口。"
	case strings.Contains(detail, "timed out"),
		strings.Contains(detail, "timeout"),
		strings.Contains(detail, "deadline exceeded"):
		return "ADB 配对超时。请保持无线调试页面打开，确认配对端口未变化后重试。"
	default:
		return "ADB 配对失败。请确认手机与电脑在同一网络、无线调试已开启，并重新生成配对端口和配对码。"
	}
}

func (s *Server) taskHardware(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		if mirror, mirrorErr := s.store.RemoteTaskMirror(request.Context(), taskID); mirrorErr == nil {
			writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "groups": mirror.Hardware, "read_only": true})
			return
		}
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	groups, err := s.store.HardwareGroups(request.Context(), taskID)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "hardware_groups_failed")
		return
	}
	resources, _ := s.store.HardwareResources(request.Context())
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "groups": groups, "resources": resources})
}

func (s *Server) updateTaskHardwareBindings(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if s.rejectRetiredChannelTaskWrite(writer, request, taskID) {
		return
	}
	if s.rejectCompletedTaskWrite(writer, request, taskID) {
		return
	}
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	var payload struct {
		Bindings []struct {
			ID         string `json:"id"`
			ResourceID string `json:"resource_id"`
			// Retained as an ignored compatibility field for pages loaded before
			// access became authoritative on the system hardware resource.
			Access string `json:"access"`
		} `json:"bindings"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if len(payload.Bindings) > 16 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "hardware_bindings_invalid", "message": "每个任务最多启用 16 个硬件资源",
		})
		return
	}
	existing, _ := s.store.TaskHardwareBindings(request.Context(), taskID)
	existingByID := map[string]domain.TaskHardwareBinding{}
	for _, binding := range existing {
		existingByID[binding.ID] = binding
	}
	now := time.Now().UTC()
	bindings := make([]domain.TaskHardwareBinding, 0, len(payload.Bindings))
	seenIDs, seenResources := map[string]bool{}, map[string]bool{}
	for _, raw := range payload.Bindings {
		resourceID := strings.TrimSpace(raw.ResourceID)
		if resourceID == "" || seenResources[resourceID] {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "hardware_binding_duplicate"})
			return
		}
		_, err := s.store.HardwareResource(request.Context(), resourceID)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "hardware_resource_not_found"})
			return
		}
		id := normalizeHardwareID(raw.ID)
		if id == "" {
			id = resourceID
		}
		if seenIDs[id] {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "hardware_binding_duplicate"})
			return
		}
		createdAt := now
		if previous, ok := existingByID[id]; ok && previous.ResourceID == resourceID {
			createdAt = previous.CreatedAt
		}
		seenIDs[id], seenResources[resourceID] = true, true
		bindings = append(bindings, domain.TaskHardwareBinding{
			TaskID: taskID, ID: id, ResourceID: resourceID,
			CreatedAt: createdAt, UpdatedAt: now,
		})
	}
	if s.hardware != nil {
		s.hardware.DisconnectTask(taskID)
	}
	if err := s.store.ReplaceTaskHardwareBindings(request.Context(), taskID, bindings); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "update_hardware_bindings_failed", "message": err.Error()})
		return
	}
	groups, _ := s.store.HardwareGroups(request.Context(), taskID)
	resources, _ := s.store.HardwareResources(request.Context())
	s.audit(request, "task.hardware.bindings.update", "task", taskID, map[string]any{
		"bindings": len(bindings), "resource_ids": func() []string {
			ids := make([]string, 0, len(bindings))
			for _, binding := range bindings {
				ids = append(ids, binding.ResourceID)
			}
			return ids
		}(),
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "groups": groups, "resources": resources})
}

func (s *Server) updateTaskHardware(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if s.rejectRetiredChannelTaskWrite(writer, request, taskID) {
		return
	}
	if s.rejectCompletedTaskWrite(writer, request, taskID) {
		return
	}
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		if _, mirrorErr := s.store.RemoteTaskMirror(request.Context(), taskID); mirrorErr == nil {
			writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "hardware_read_only", "message": "远端 Task 硬件配置只能查看，请先显式接管"})
			return
		}
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	var payload struct {
		Groups []hardwareGroupPayload `json:"groups"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if len(payload.Groups) > 16 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "hardware_groups_invalid", "message": "最多支持 16 个硬件组",
		})
		return
	}
	existing, _ := s.store.HardwareGroups(request.Context(), taskID)
	existingByID := make(map[string]domain.HardwareGroup, len(existing))
	for _, item := range existing {
		existingByID[item.ID] = item
	}
	now := time.Now().UTC()
	groups := make([]domain.HardwareGroup, 0, len(payload.Groups))
	seen := map[string]bool{}
	newSecrets := map[string]string{}
	for index, raw := range payload.Groups {
		id := normalizeHardwareID(raw.ID)
		if id == "" && index < len(existing) {
			id = existing[index].ID
		}
		if id == "" {
			id = fmt.Sprintf("hardware-%d", index+1)
		}
		baseID := id
		for suffix := 2; seen[id]; suffix++ {
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		seen[id] = true
		item, err := normalizeHardwareGroup(taskID, id, index, raw, existingByID[id], now)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{
				"ok": false, "error": "hardware_group_invalid", "message": err.Error(),
			})
			return
		}
		if item.Network.Protocol == domain.HardwareProtocolADB {
			raw.ClearSecret = true
			raw.Password = ""
			item.CredentialRef = ""
			item.PasswordConfigured = false
		}
		if raw.ClearSecret {
			item.CredentialRef = ""
			item.PasswordConfigured = false
		} else if raw.Password != "" {
			if s.secrets == nil {
				writeError(writer, http.StatusInternalServerError, "secret_store_unavailable")
				return
			}
			item.CredentialRef = "hardware/" + taskID + "/" + id + "/credential"
			item.PasswordConfigured = true
			newSecrets[item.CredentialRef] = raw.Password
		}
		groups = append(groups, item)
	}
	if len(newSecrets) > 0 {
		if err := s.secrets.PutMany(newSecrets); err != nil {
			writeError(writer, http.StatusInternalServerError, "store_secrets_failed")
			return
		}
	}
	if s.hardware != nil {
		s.hardware.DisconnectTask(taskID)
	}
	if err := s.store.ReplaceHardwareGroups(request.Context(), taskID, groups); err != nil {
		writeError(writer, http.StatusInternalServerError, "update_hardware_failed")
		return
	}
	keepSecrets := map[string]bool{}
	for _, item := range groups {
		if item.CredentialRef != "" {
			keepSecrets[item.CredentialRef] = true
		}
	}
	var removeSecrets []string
	for _, item := range existing {
		if item.CredentialRef != "" && !keepSecrets[item.CredentialRef] {
			removeSecrets = append(removeSecrets, item.CredentialRef)
		}
	}
	if s.secrets != nil && len(removeSecrets) > 0 {
		_ = s.secrets.DeleteMany(removeSecrets)
	}
	hardwareIDs := make([]string, 0, len(groups))
	modes := make([]string, 0, len(groups))
	for _, group := range groups {
		hardwareIDs = append(hardwareIDs, group.ID)
		modes = append(modes, group.Mode)
	}
	s.audit(request, "task.hardware.update", "task", taskID, map[string]any{
		"groups": len(groups), "hardware_ids": hardwareIDs, "modes": modes,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "groups": groups})
}

func (s *Server) hardwareTerminal(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	page, err := s.store.HardwareIOPage(
		request.Context(), task.ID, group.ID, transport,
		queryInt(request, "after", 0), int(queryInt(request, "limit", 500)),
	)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "hardware_io_failed")
		return
	}
	status := disconnectedHardwareStatus(task.ID, group.ID, transport, readOnly)
	if s.hardware != nil {
		status = s.hardware.Status(task.ID, group.ID, transport, readOnly)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "group": group, "transport": transport, "status": status, "stream": page,
	})
}

func (s *Server) connectHardware(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if s.hardware == nil {
		writeError(writer, http.StatusServiceUnavailable, "hardware_runtime_unavailable")
		return
	}
	password := ""
	if group.CredentialRef != "" && s.secrets != nil {
		password, _ = s.secrets.Get(group.CredentialRef)
	}
	status, err := s.hardware.Connect(request.Context(), hardware.ConnectRequest{
		TaskID: task.ID, HardwareID: group.ID, Transport: transport, Group: group,
		Password: password, ReadOnly: readOnly,
	})
	if err != nil {
		s.audit(request, "task.hardware.connect_failed", "task", task.ID, map[string]any{
			"hardware_id": group.ID, "transport": transport, "error": err.Error(),
		})
		code := "hardware_connect_failed"
		if hardware.IsUnknownSSHHostKey(err) {
			code = "ssh_host_key_unknown"
		}
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": code, "message": err.Error()})
		return
	}
	s.audit(request, "task.hardware.connect", "task", task.ID, map[string]any{
		"hardware_id": group.ID, "transport": transport,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func (s *Server) hardwareSSHHostKey(writer http.ResponseWriter, request *http.Request) {
	_, group, transport, _, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if transport != domain.HardwareTransportNetwork || group.Network.Protocol != domain.HardwareProtocolSSH {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "ssh_host_key_unavailable", "message": "当前硬件连接不是 SSH"})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	info, err := s.probeSSHHostKey(ctx, net.JoinHostPort(group.Network.Host, fmt.Sprint(group.Network.Port)))
	if err != nil {
		writeJSON(writer, http.StatusBadGateway, map[string]any{"ok": false, "error": "ssh_host_key_probe_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "host_key": info})
}

func (s *Server) trustHardwareSSHHostKey(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if readOnly {
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "hardware_read_only", "message": "只读硬件不能修改主机信任"})
		return
	}
	if transport != domain.HardwareTransportNetwork || group.Network.Protocol != domain.HardwareProtocolSSH {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "ssh_host_key_unavailable", "message": "当前硬件连接不是 SSH"})
		return
	}
	var payload struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := decodeJSON(request, &payload); err != nil || strings.TrimSpace(payload.Fingerprint) == "" {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	info, err := s.trustSSHHostKey(ctx, net.JoinHostPort(group.Network.Host, fmt.Sprint(group.Network.Port)), strings.TrimSpace(payload.Fingerprint))
	if err != nil {
		writeJSON(writer, http.StatusConflict, map[string]any{"ok": false, "error": "ssh_host_key_trust_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.hardware.ssh_host_key_trust", "task", task.ID, map[string]any{"hardware_id": group.ID, "endpoint": info.Endpoint, "algorithm": info.Algorithm, "fingerprint": info.Fingerprint})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "host_key": info})
}

func (s *Server) disconnectHardware(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if s.hardware != nil {
		s.hardware.Disconnect(task.ID, group.ID, transport)
	}
	s.audit(request, "task.hardware.disconnect", "task", task.ID, map[string]any{
		"hardware_id": group.ID, "transport": transport,
	})
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "status": disconnectedHardwareStatus(task.ID, group.ID, transport, readOnly),
	})
}

func (s *Server) sendHardware(writer http.ResponseWriter, request *http.Request) {
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if readOnly {
		writeJSON(writer, http.StatusForbidden, map[string]any{
			"ok": false, "error": "hardware_read_only", "message": "当前硬件组或 Task 为只读",
		})
		return
	}
	if s.hardware == nil {
		writeError(writer, http.StatusServiceUnavailable, "hardware_runtime_unavailable")
		return
	}
	var payload struct {
		Data     string `json:"data"`
		Encoding string `json:"encoding"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	source := "web"
	if claims, ok := agentClaimsFromContext(request.Context()); ok {
		source = "agent:" + claims.AgentID
	}
	if err := s.hardware.Send(task.ID, group.ID, transport, payload.Data, payload.Encoding, source); err != nil {
		status := http.StatusConflict
		if errors.Is(err, hardware.ErrReadOnly) {
			status = http.StatusForbidden
		}
		writeJSON(writer, status, map[string]any{
			"ok": false, "error": "hardware_send_failed", "message": err.Error(),
		})
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) hardwareTarget(
	writer http.ResponseWriter,
	request *http.Request,
) (domain.Task, domain.HardwareGroup, string, bool, bool) {
	taskID := request.PathValue("id")
	hardwareID := request.PathValue("hardware")
	task, err := s.store.Task(request.Context(), taskID)
	if err != nil {
		if _, mirrorErr := s.store.RemoteTaskMirror(request.Context(), taskID); mirrorErr == nil {
			writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "hardware_read_only", "message": "远端 Task 硬件配置不能连接、登录或发送，请先显式接管"})
			return domain.Task{}, domain.HardwareGroup{}, "", true, false
		}
		writeError(writer, http.StatusNotFound, "task_not_found")
		return domain.Task{}, domain.HardwareGroup{}, "", false, false
	}
	group, err := s.store.HardwareGroup(request.Context(), taskID, hardwareID)
	if err != nil {
		writeError(writer, http.StatusNotFound, "hardware_group_not_found")
		return domain.Task{}, domain.HardwareGroup{}, "", false, false
	}
	transport := strings.TrimSpace(request.URL.Query().Get("transport"))
	if transport == "" {
		if group.Supports(domain.HardwareTransportSerial) {
			transport = domain.HardwareTransportSerial
		} else {
			transport = domain.HardwareTransportNetwork
		}
	}
	if !group.Supports(transport) {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "hardware_transport_invalid", "message": "硬件组未配置该连接方式",
		})
		return domain.Task{}, domain.HardwareGroup{}, "", false, false
	}
	readOnly := task.Status.Terminal() || !group.Writable()
	return task, group, transport, readOnly, true
}

func normalizeHardwareGroup(
	taskID, id string,
	position int,
	raw hardwareGroupPayload,
	existing domain.HardwareGroup,
	now time.Time,
) (domain.HardwareGroup, error) {
	return hardwareconfig.NormalizeGroup(taskID, id, position, raw, existing, now)
}

func normalizeHardwareID(value string) string {
	return hardwareconfig.NormalizeID(value)
}

func disconnectedHardwareStatus(taskID, hardwareID, transport string, readOnly bool) domain.HardwareTerminalStatus {
	return domain.HardwareTerminalStatus{
		TaskID: taskID, HardwareID: hardwareID, Transport: transport,
		Status: "disconnected", ReadOnly: readOnly,
	}
}

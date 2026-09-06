package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
)

var hardwareIDInvalid = regexp.MustCompile(`[^a-z0-9_-]+`)

type hardwareGroupPayload struct {
	ID                    string                       `json:"id"`
	Description           string                       `json:"description"`
	Mode                  string                       `json:"mode"`
	Serial                domain.HardwareSerialConfig  `json:"serial"`
	Network               domain.HardwareNetworkConfig `json:"network"`
	Username              string                       `json:"username"`
	Password              string                       `json:"password"`
	ReuseRemoteCredential bool                         `json:"reuse_remote_credential"`
	ClearSecret           bool                         `json:"clear_password"`
	Access                string                       `json:"access"`
}

func (s *Server) hardwareSerialPorts(writer http.ResponseWriter, _ *http.Request) {
	if s.hardware == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "ports": []domain.SerialPort{}})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "ports": s.hardware.SerialPorts()})
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
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "groups": groups})
}

func (s *Server) updateTaskHardware(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
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
		writeJSON(writer, http.StatusConflict, map[string]any{
			"ok": false, "error": "hardware_connect_failed", "message": err.Error(),
		})
		return
	}
	s.audit(request, "task.hardware.connect", "task", task.ID, map[string]any{
		"hardware_id": group.ID, "transport": transport,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "status": status})
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
	mode := strings.TrimSpace(raw.Mode)
	if mode == "" {
		mode = domain.HardwareModeOff
	}
	switch mode {
	case domain.HardwareModeOff, domain.HardwareModeSerial, domain.HardwareModeNetwork, domain.HardwareModeBoth:
	default:
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的模式无效", id)
	}
	access := strings.TrimSpace(raw.Access)
	if access == "" {
		access = domain.HardwareAccessReadWrite
	}
	if access != domain.HardwareAccessReadOnly && access != domain.HardwareAccessReadWrite {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的权限无效", id)
	}
	description := strings.TrimSpace(raw.Description)
	if utf8.RuneCountInString(description) > 2000 {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的描述超过 2000 字符", id)
	}
	raw.Serial.Device = strings.TrimSpace(raw.Serial.Device)
	if raw.Serial.Baudrate <= 0 {
		raw.Serial.Baudrate = 115200
	}
	raw.Network.Host = strings.TrimSpace(raw.Network.Host)
	raw.Network.Protocol = strings.TrimSpace(raw.Network.Protocol)
	if raw.Network.Protocol == "" {
		raw.Network.Protocol = domain.HardwareProtocolTelnet
	}
	if raw.Network.Protocol != domain.HardwareProtocolTelnet &&
		raw.Network.Protocol != domain.HardwareProtocolRaw &&
		raw.Network.Protocol != domain.HardwareProtocolSSH {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的网络协议无效", id)
	}
	raw.Network.SSHAuth = strings.TrimSpace(raw.Network.SSHAuth)
	if raw.Network.SSHAuth == "" {
		raw.Network.SSHAuth = domain.HardwareSSHAuthAuto
	}
	if raw.Network.SSHAuth != domain.HardwareSSHAuthAuto &&
		raw.Network.SSHAuth != domain.HardwareSSHAuthPassword &&
		raw.Network.SSHAuth != domain.HardwareSSHAuthKey {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的 SSH 登录方式无效", id)
	}
	if raw.Network.Port <= 0 {
		if raw.Network.Protocol == domain.HardwareProtocolSSH {
			raw.Network.Port = 22
		} else {
			raw.Network.Port = 23
		}
	}
	if raw.Network.Port > 65535 {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的网络端口无效", id)
	}
	if mode == domain.HardwareModeSerial && raw.Serial.Device == "" {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 缺少串口设备", id)
	}
	if mode == domain.HardwareModeNetwork && raw.Network.Host == "" {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 缺少网络地址", id)
	}
	if (mode == domain.HardwareModeNetwork || mode == domain.HardwareModeBoth) &&
		raw.Network.Protocol == domain.HardwareProtocolSSH &&
		strings.TrimSpace(raw.Username) == "" {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 的 SSH 用户名不能为空", id)
	}
	if mode == domain.HardwareModeBoth && raw.Serial.Device == "" && raw.Network.Host == "" {
		return domain.HardwareGroup{}, fmt.Errorf("硬件组 %s 至少需要一种连接", id)
	}
	createdAt := existing.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	return domain.HardwareGroup{
		TaskID: taskID, ID: id, Position: position, Description: description, Mode: mode,
		Serial: raw.Serial, Network: raw.Network, Username: strings.TrimSpace(raw.Username),
		CredentialRef: existing.CredentialRef, PasswordConfigured: existing.PasswordConfigured,
		Access: access, CreatedAt: createdAt, UpdatedAt: now,
	}, nil
}

func normalizeHardwareID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = hardwareIDInvalid.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-_")
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func disconnectedHardwareStatus(taskID, hardwareID, transport string, readOnly bool) domain.HardwareTerminalStatus {
	return domain.HardwareTerminalStatus{
		TaskID: taskID, HardwareID: hardwareID, Transport: transport,
		Status: "disconnected", ReadOnly: readOnly,
	}
}

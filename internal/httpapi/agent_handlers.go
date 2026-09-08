package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/managedprocess"
	"github.com/ChinaKai/AHA2/internal/workspace"
)

func (s *Server) agentHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.taskHardware(writer, request)
}

func (s *Server) agentHardwareTerminal(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.hardwareTerminal(writer, request)
}

func (s *Server) agentConnectHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.connectHardware(writer, request)
}

func (s *Server) agentDisconnectHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.disconnectHardware(writer, request)
}

func (s *Server) agentSendHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	s.sendHardware(writer, request)
}

func (s *Server) agentLoginHardware(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if readOnly {
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "hardware_read_only", "message": "当前硬件组或 Task 为只读"})
		return
	}
	if s.hardware == nil {
		writeError(writer, http.StatusServiceUnavailable, "hardware_runtime_unavailable")
		return
	}
	var options hardware.LoginOptions
	if err := decodeJSON(request, &options); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	status, err := s.hardware.Login(task.ID, group.ID, transport, options)
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, hardware.ErrReadOnly) {
			code = http.StatusForbidden
		}
		writeJSON(writer, code, map[string]any{"ok": false, "error": "hardware_login_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.hardware.login", "task", task.ID, map[string]any{"hardware_id": group.ID, "transport": transport})
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "status": status})
}

func (s *Server) bindAgentTask(writer http.ResponseWriter, request *http.Request) bool {
	claims, ok := agentClaimsFromContext(request.Context())
	if !ok || claims.TaskID == "" {
		writeError(writer, http.StatusUnauthorized, "agent_capability_invalid")
		return false
	}
	request.SetPathValue("id", claims.TaskID)
	return true
}

func (s *Server) agentProcesses(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	if s.rejectChannelManagedProcess(writer, request, claims.TurnID) {
		return
	}
	if s.managedProcesses == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_processes_unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "processes": s.managedProcesses.List(claims.TaskID)})
}

func (s *Server) startAgentProcess(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	if s.rejectChannelManagedProcess(writer, request, claims.TurnID) {
		return
	}
	if s.managedProcesses == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_processes_unavailable")
		return
	}
	var payload managedprocess.StartRequest
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	task, err := s.store.Task(request.Context(), claims.TaskID)
	if err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	item, err := s.store.Workspace(request.Context(), task.WorkspaceID)
	if err != nil {
		writeError(writer, http.StatusNotFound, "workspace_not_found")
		return
	}
	if item.SSHCredentialRef != "" && s.secrets != nil {
		item.SSHPassword, _ = s.secrets.Get(item.SSHCredentialRef)
	}
	root := task.TaskWorkspacePath
	if root == "" {
		root = item.RootPath
	}
	payload.Dir, err = managedProcessDir(item.Transport, root, payload.Dir)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "managed_process_invalid", "message": err.Error()})
		return
	}
	status, err := s.managedProcesses.Start(claims.TaskID, claims.AgentID, workspace.RunnerFor(item), payload)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, managedprocess.ErrAlreadyRunning) {
			code = http.StatusConflict
		}
		writeJSON(writer, code, map[string]any{"ok": false, "error": "managed_process_start_failed", "message": err.Error()})
		return
	}
	s.audit(request, "task.process.start", "task", claims.TaskID, map[string]any{"name": status.Name, "executable": status.Executable})
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "process": status})
}

func (s *Server) agentProcessStatus(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	if s.rejectChannelManagedProcess(writer, request, claims.TurnID) {
		return
	}
	if s.managedProcesses == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_processes_unavailable")
		return
	}
	status, err := s.managedProcesses.Status(claims.TaskID, request.PathValue("name"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "managed_process_not_found")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "process": status})
}

func (s *Server) stopAgentProcess(writer http.ResponseWriter, request *http.Request) {
	claims, _ := agentClaimsFromContext(request.Context())
	if s.rejectChannelManagedProcess(writer, request, claims.TurnID) {
		return
	}
	if s.managedProcesses == nil {
		writeError(writer, http.StatusServiceUnavailable, "managed_processes_unavailable")
		return
	}
	status, err := s.managedProcesses.Stop(claims.TaskID, request.PathValue("name"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "managed_process_not_found")
		return
	}
	s.audit(request, "task.process.stop", "task", claims.TaskID, map[string]any{"name": status.Name})
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "process": status})
}

func (s *Server) rejectChannelManagedProcess(writer http.ResponseWriter, request *http.Request, turnID string) bool {
	turn, err := s.store.Turn(request.Context(), turnID)
	if err != nil {
		return false
	}
	channelContext, err := s.store.ChannelContextForInboxBatch(request.Context(), turn.InboxBatchID)
	if err != nil {
		return false
	}
	route, _ := channelContext["route"].(map[string]any)
	if strings.TrimSpace(fmt.Sprint(route["mode"])) == "task_route" {
		return false
	}
	writeError(writer, http.StatusForbidden, "channel_operation_forbidden")
	return true
}

func managedProcessDir(transport, root, requested string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("task workspace is unavailable")
	}
	if transport == "native" {
		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		target := requested
		if strings.TrimSpace(target) == "" {
			target = absoluteRoot
		} else if !filepath.IsAbs(target) {
			target = filepath.Join(absoluteRoot, target)
		}
		target, err = filepath.Abs(target)
		if err != nil {
			return "", err
		}
		prefix := absoluteRoot + string(filepath.Separator)
		inside := target == absoluteRoot || strings.HasPrefix(target, prefix)
		if runtime.GOOS == "windows" {
			inside = strings.EqualFold(target, absoluteRoot) || strings.HasPrefix(strings.ToLower(target), strings.ToLower(prefix))
		}
		if !inside {
			return "", errors.New("cwd must stay inside the Task workspace")
		}
		return target, nil
	}
	cleanRoot := path.Clean(strings.ReplaceAll(root, `\`, "/"))
	target := strings.TrimSpace(requested)
	if target == "" {
		target = cleanRoot
	} else {
		target = strings.ReplaceAll(target, `\`, "/")
		if !path.IsAbs(target) {
			target = path.Join(cleanRoot, target)
		}
		target = path.Clean(target)
	}
	if target != cleanRoot && !strings.HasPrefix(target, cleanRoot+"/") {
		return "", errors.New("cwd must stay inside the Task workspace")
	}
	return target, nil
}

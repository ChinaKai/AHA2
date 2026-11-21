package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
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
	if request.Context().Err() != nil {
		writeError(writer, http.StatusConflict, "operation_cancelled")
		return
	}
	status, err := s.hardware.Login(task.ID, group.ID, transport, options)
	if err != nil {
		code := http.StatusConflict
		if errors.Is(err, hardware.ErrReadOnly) {
			code = http.StatusForbidden
		}
		writeJSON(writer, code, map[string]any{"ok": false, "error": hardwarePublicErrorCode(err, "hardware_login_failed"), "message": err.Error()})
		return
	}
	s.audit(request, "task.hardware.login", "task", task.ID, map[string]any{"hardware_id": group.ID, "transport": transport})
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "status": status})
}

var blockedADBHostCommands = map[string]bool{
	"connect": true, "disconnect": true, "pair": true,
	"kill-server": true, "start-server": true, "devices": true,
	"mdns": true, "host-features": true,
	// These are also parsed as server-mode modifiers before device commands.
	"server": true, "nodaemon": true, "fork-server": true,
	// tcpip restarts adbd in TCP mode, silently turning a USB or wireless
	// resource into a network device outside the resource's connection form.
	"tcpip": true,
}

type adbCommand struct {
	name              string
	serial            string
	unsafeGlobalFlags bool
}

// parseADBCommand only parses the global prefix. Once a device command is
// found, its arguments belong to that command (including options and names
// such as "tcpip" passed to shell). Unknown or incomplete prefixes fail closed.
func parseADBCommand(args []string) (adbCommand, bool) {
	var command adbCommand
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		option, value := args[i], ""
		switch option {
		case "-s", "-t", "-H", "-P", "-L":
			i++
			if i == len(args) {
				return adbCommand{}, false
			}
			value = args[i]
		case "-a", "-d", "-e", "--exit-on-write-error":
		default:
			// adb accepts attached -H/-P values, and attached -s/-t values
			// only when their first character is a digit. -L needs a token.
			if len(option) <= 2 {
				return adbCommand{}, false
			}
			prefix := option[:2]
			if prefix != "-H" && prefix != "-P" &&
				!((prefix == "-s" || prefix == "-t") && option[2] >= '0' && option[2] <= '9') {
				return adbCommand{}, false
			}
			option, value = prefix, option[2:]
		}
		switch option {
		case "-s", "-t", "-H", "-P", "-L":
			if value == "" || strings.HasPrefix(value, "-") {
				return adbCommand{}, false
			}
		}
		if option == "-s" {
			command.serial = value // Like adb, the last -s wins.
		} else if option != "--exit-on-write-error" {
			// Recognizing an option must not grant access to another device,
			// transport ID, or ADB server. Keep the existing resource boundary.
			command.unsafeGlobalFlags = true
		}
		i++
	}
	for i < len(args) && strings.HasPrefix(args[i], "wait-for-") {
		if args[i] == "wait-for-" {
			return adbCommand{}, false
		}
		command.name = args[i]
		i++
	}
	if i < len(args) {
		command.name = args[i]
	}
	return command, command.name != "" && !strings.HasPrefix(command.name, "-")
}

func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request) {
	if !s.bindAgentTask(writer, request) {
		return
	}
	task, group, transport, readOnly, ok := s.hardwareTarget(writer, request)
	if !ok {
		return
	}
	if transport != domain.HardwareTransportNetwork || group.Network.Protocol != domain.HardwareProtocolADB {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_exec_unavailable", "message": "当前硬件资源不是 ADB 设备"})
		return
	}
	if readOnly {
		writeJSON(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "hardware_read_only", "message": "ADB 通用执行要求硬件资源和 Task 可读写"})
		return
	}
	var payload struct {
		Args           []string `json:"args"`
		StdinBase64    string   `json:"stdin_base64"`
		TimeoutSeconds int      `json:"timeout_seconds"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if len(payload.Args) > 64 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_args_invalid", "message": "ADB 参数数量必须在 1 到 64 之间"})
		return
	}
	command, parsed := parseADBCommand(payload.Args)
	target := net.JoinHostPort(group.Network.Host, strconv.Itoa(group.Network.Port))
	if group.Network.ADBUSB() {
		target = group.Network.ADBSerial
	}
	// Wireless debugging resolves its serial via mDNS at execution time, so a
	// caller-supplied serial cannot be verified against that resource here.
	if !parsed || blockedADBHostCommands[strings.ToLower(command.name)] || command.unsafeGlobalFlags ||
		(command.serial != "" && (group.Network.ADBWireless() || command.serial != target)) {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_command_not_allowed", "message": "这里只允许面向当前设备的 ADB 子命令"})
		return
	}
	argumentBytes := 0
	for _, argument := range payload.Args {
		if strings.ContainsRune(argument, '\x00') || len(argument) > 4096 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_args_invalid", "message": "ADB 参数包含无效内容或过长"})
			return
		}
		argumentBytes += len(argument)
	}
	if argumentBytes > 16*1024 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_args_invalid", "message": "ADB 参数总长度超过限制"})
		return
	}
	input := []byte(nil)
	if payload.StdinBase64 != "" {
		var err error
		input, err = base64.StdEncoding.DecodeString(payload.StdinBase64)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_stdin_invalid", "message": "ADB stdin 必须是 base64"})
			return
		}
	}
	if len(input) > 1024*1024 {
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]any{"ok": false, "error": "adb_stdin_too_large", "message": "ADB stdin 超过 1 MiB"})
		return
	}
	timeoutSeconds := payload.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = 30
	}
	if timeoutSeconds < 1 || timeoutSeconds > 120 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_timeout_invalid", "message": "ADB 超时时间必须在 1 到 120 秒之间"})
		return
	}
	// The target comes only from this Task's bound resource: USB direct runs
	// on that serial without connect/mDNS, network direct connects exactly
	// its host:port without mDNS, wireless debugging keeps its mDNS path.
	var result hardware.ADBExecResult
	var err error
	timeout := time.Duration(timeoutSeconds) * time.Second
	startedAt := time.Now()
	switch {
	case group.Network.ADBUSB():
		result, err = s.executeADBUSB(request.Context(), group.Network.ADBSerial, payload.Args, input, timeout)
	case group.Network.ADBTCP():
		result, err = s.executeADBTCP(request.Context(), net.JoinHostPort(group.Network.Host, strconv.Itoa(group.Network.Port)), payload.Args, input, timeout)
	default:
		result, err = s.executeADB(request.Context(), net.JoinHostPort(group.Network.Host, strconv.Itoa(group.Network.Port)), payload.Args, input, timeout)
	}
	if err != nil {
		code := http.StatusConflict
		errorCode := hardwarePublicErrorCode(err, "adb_exec_failed")
		// Output overflow and the command deadline both end a command that may
		// already have changed the device; a caller must not retry blindly. A
		// connection that hangs until the deadline is also reported this way:
		// it cannot be told apart from a started command here.
		mayHaveExecuted := errors.Is(err, hardware.ErrADBOutputTooLarge)
		if mayHaveExecuted {
			code = http.StatusRequestEntityTooLarge
		} else if time.Since(startedAt) >= timeout || errors.Is(request.Context().Err(), context.DeadlineExceeded) {
			mayHaveExecuted, errorCode = true, "adb_timeout_may_have_executed"
		}
		s.audit(request, "task.hardware.adb_exec_failed", "task", task.ID, map[string]any{
			"hardware_id": group.ID, "argument_count": len(payload.Args), "input_bytes": len(input), "may_have_executed": mayHaveExecuted,
		})
		writeJSON(writer, code, map[string]any{"ok": false, "error": errorCode, "may_have_executed": mayHaveExecuted, "message": err.Error()})
		return
	}
	s.audit(request, "task.hardware.adb_exec", "task", task.ID, map[string]any{
		"hardware_id": group.ID, "argument_count": len(payload.Args),
		"input_bytes": len(input), "stdout_bytes": len(result.Stdout),
		"stderr_bytes": len(result.Stderr), "exit_code": result.ExitCode,
	})
	stdout, stderr := result.Stdout, result.Stderr
	stdoutTruncated, stderrTruncated := false, false
	if operation, _ := request.Context().Value(agentOperationContextKey{}).(string); operation == "hardware_adb_exec" {
		// The command has already run. Preserve exit/status and an explicit
		// bounded result instead of turning a successful side effect into a
		// response-size failure that could provoke an unsafe command retry.
		const budget = 24 << 10
		// Keep diagnostic stderr even when stdout fills its entire allowance.
		reservedStderr := len(stderr)
		if reservedStderr > 6<<10 {
			reservedStderr = 6 << 10
		}
		stdoutBudget := budget - reservedStderr
		if len(stdout) > stdoutBudget {
			stdout = stdout[:stdoutBudget]
			stdoutTruncated = true
		}
		remaining := budget - len(stdout)
		if len(stderr) > remaining {
			stderr = stderr[:remaining]
			stderrTruncated = true
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ok": true, "endpoint": result.Endpoint, "exit_code": result.ExitCode,
		"stdout_base64":    base64.StdEncoding.EncodeToString(stdout),
		"stderr_base64":    base64.StdEncoding.EncodeToString(stderr),
		"stdout_truncated": stdoutTruncated, "stderr_truncated": stderrTruncated,
		"stdout_bytes": len(result.Stdout), "stderr_bytes": len(result.Stderr),
		"duration_ms": result.Duration.Milliseconds(),
	})
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

# task-726 review packet diff-2-go-api：Go 生产代码：httpapi / app（宠物、接管）/ prompt

`git diff 43624a4 3890244 -- internal/httpapi/agent_handlers.go internal/httpapi/hardware_handlers.go internal/httpapi/server.go internal/httpapi/takeover_handlers.go internal/app/pet.go internal/app/pet_hardware_resources.go internal/app/pet_takeover.go internal/prompt/registry.go internal/prompt/templates/context-hardware.md internal/prompt/templates/zh-CN/context-hardware.md` 原样输出。

~~~~diff
diff --git a/internal/app/pet.go b/internal/app/pet.go
index dc64160..c536056 100644
--- a/internal/app/pet.go
+++ b/internal/app/pet.go
@@ -1574,10 +1574,12 @@ func ahaPetTools() []gateway.ChatTool {
 							"baudrate": map[string]any{"type": "integer"},
 						}},
 						"network": map[string]any{"type": "object", "properties": map[string]any{
-							"host":     map[string]any{"type": "string"},
-							"port":     map[string]any{"type": "integer"},
-							"protocol": map[string]any{"type": "string", "enum": []string{"telnet", "raw", "ssh", "adb"}},
-							"ssh_auth": map[string]any{"type": "string", "enum": []string{"auto", "password", "key"}},
+							"host":           map[string]any{"type": "string"},
+							"port":           map[string]any{"type": "integer"},
+							"protocol":       map[string]any{"type": "string", "enum": []string{"telnet", "raw", "ssh", "adb"}},
+							"ssh_auth":       map[string]any{"type": "string", "enum": []string{"auto", "password", "key"}},
+							"adb_connection": map[string]any{"type": "string", "enum": []string{"wireless", "tcp", "usb"}},
+							"adb_serial":     map[string]any{"type": "string"},
 						}},
 						"username":                map[string]any{"type": "string"},
 						"password":                map[string]any{"type": "string"},
@@ -1730,7 +1732,7 @@ func ahaPetTools() []gateway.ChatTool {
 					},
 				},
 				"network": map[string]any{
-					"type": "object", "description": "mode=network 时 host 必填；port 不传时按协议使用默认端口",
+					"type": "object", "description": "mode=network 时 host 必填（ADB USB 直连除外，改为 adb_serial 必填）；port 不传时按协议使用默认端口",
 					"properties": map[string]any{
 						"host": map[string]any{"type": "string"},
 						"port": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
@@ -1741,6 +1743,10 @@ func ahaPetTools() []gateway.ChatTool {
 						"ssh_auth": map[string]any{"type": "string", "enum": []string{
 							domain.HardwareSSHAuthAuto, domain.HardwareSSHAuthPassword, domain.HardwareSSHAuthKey,
 						}},
+						"adb_connection": map[string]any{"type": "string", "enum": []string{
+							domain.HardwareADBConnectionWireless, domain.HardwareADBConnectionTCP, domain.HardwareADBConnectionUSB,
+						}, "description": "仅 protocol=adb：wireless=Android 无线调试（host+调试连接端口，配对码由 Owner 在 Web 资源页配对）；tcp=网络直连（显式 host+port，默认 5555，不配对、不做 mDNS 端口发现，设备需已开启网络 ADB）；usb=USB 直连（只用 adb_serial，不用 host/port/配对码）。不传时旧资源按 wireless"},
+						"adb_serial": map[string]any{"type": "string", "description": "仅 adb_connection=usb：AHA 宿主机上 USB 设备的序列号（Owner 在 Web 资源页从检测到的设备中选择）；不接受 IP:端口、mDNS 名或模拟器"},
 					},
 				},
 				"username": map[string]any{"type": "string", "description": "SSH 用户名；ADB 资源会忽略"},
@@ -1764,7 +1770,7 @@ func ahaPetTools() []gateway.ChatTool {
 					},
 				},
 				"network": map[string]any{
-					"type": "object", "description": "未提供字段保持原值；mode=network 时 host 必填",
+					"type": "object", "description": "未提供字段保持原值；mode=network 时 host 必填（ADB USB 直连除外，改为 adb_serial 必填）",
 					"properties": map[string]any{
 						"host": map[string]any{"type": "string"},
 						"port": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
@@ -1775,6 +1781,10 @@ func ahaPetTools() []gateway.ChatTool {
 						"ssh_auth": map[string]any{"type": "string", "enum": []string{
 							domain.HardwareSSHAuthAuto, domain.HardwareSSHAuthPassword, domain.HardwareSSHAuthKey,
 						}},
+						"adb_connection": map[string]any{"type": "string", "enum": []string{
+							domain.HardwareADBConnectionWireless, domain.HardwareADBConnectionTCP, domain.HardwareADBConnectionUSB,
+						}, "description": "仅 protocol=adb：wireless=Android 无线调试（host+调试连接端口，配对码由 Owner 在 Web 资源页配对）；tcp=网络直连（显式 host+port，默认 5555，不配对、不做 mDNS 端口发现，设备需已开启网络 ADB）；usb=USB 直连（只用 adb_serial，不用 host/port/配对码）。不传时旧资源按 wireless"},
+						"adb_serial": map[string]any{"type": "string", "description": "仅 adb_connection=usb：AHA 宿主机上 USB 设备的序列号（Owner 在 Web 资源页从检测到的设备中选择）；不接受 IP:端口、mDNS 名或模拟器"},
 					},
 				},
 				"username": map[string]any{"type": "string", "description": "SSH 用户名；ADB 资源会忽略"},
diff --git a/internal/app/pet_hardware_resources.go b/internal/app/pet_hardware_resources.go
index 032805f..3a157ee 100644
--- a/internal/app/pet_hardware_resources.go
+++ b/internal/app/pet_hardware_resources.go
@@ -40,6 +40,9 @@ type PetHardwareNetworkInput struct {
 	Port     *int    `json:"port"`
 	Protocol *string `json:"protocol"`
 	SSHAuth  *string `json:"ssh_auth"`
+	// ADBConnection is wireless or usb; ADBSerial is the USB device serial.
+	ADBConnection *string `json:"adb_connection"`
+	ADBSerial     *string `json:"adb_serial"`
 }
 
 type PetHardwareResourceInput struct {
@@ -55,6 +58,9 @@ type PetHardwareResourceInput struct {
 
 var petHardwareCredentialFields = map[string]bool{
 	"password": true, "credential_ref": true, "clear_secret": true, "clear_password": true,
+	// ADB wireless pairing codes are Owner-only, entered in the Web resource
+	// page; reject instead of silently dropping so the pet never assumes it paired.
+	"pairing_code": true,
 }
 
 var errPetHardwareCredentialOwnerOnly = fmt.Errorf(
@@ -157,6 +163,12 @@ func (input PetHardwareResourceInput) applyTo(base hardwareconfig.GroupPayload)
 		if input.Network.SSHAuth != nil {
 			base.Network.SSHAuth = *input.Network.SSHAuth
 		}
+		if input.Network.ADBConnection != nil {
+			base.Network.ADBConnection = *input.Network.ADBConnection
+		}
+		if input.Network.ADBSerial != nil {
+			base.Network.ADBSerial = *input.Network.ADBSerial
+		}
 	}
 	return base
 }
@@ -175,7 +187,12 @@ func validatePetHardwareResourceInput(payload hardwareconfig.GroupPayload, input
 		}
 	}
 	if mode == domain.HardwareModeNetwork {
-		if strings.TrimSpace(payload.Network.Host) == "" {
+		adbUSB := strings.TrimSpace(payload.Network.Protocol) == domain.HardwareProtocolADB &&
+			strings.TrimSpace(payload.Network.ADBConnection) == domain.HardwareADBConnectionUSB
+		if adbUSB && strings.TrimSpace(payload.Network.ADBSerial) == "" {
+			return fmt.Errorf("hardware_resource_invalid: ADB USB 直连资源必须提供 network.adb_serial（AHA 宿主机上的 USB 设备序列号），不需要 host、port 或配对码")
+		}
+		if !adbUSB && strings.TrimSpace(payload.Network.Host) == "" {
 			return fmt.Errorf("hardware_resource_invalid: 网络硬件资源必须提供 network.host")
 		}
 		if input.Network != nil && input.Network.Port != nil &&
@@ -242,6 +259,12 @@ func petHardwareResourceChangedFields(before, after domain.HardwareResource) []s
 	if before.Network.SSHAuth != after.Network.SSHAuth {
 		changed = append(changed, "network.ssh_auth")
 	}
+	if before.Network.ADBConnection != after.Network.ADBConnection {
+		changed = append(changed, "network.adb_connection")
+	}
+	if before.Network.ADBSerial != after.Network.ADBSerial {
+		changed = append(changed, "network.adb_serial")
+	}
 	if before.Username != after.Username {
 		changed = append(changed, "username")
 	}
@@ -292,6 +315,8 @@ func petHardwareResourceTargetChanged(before, after domain.HardwareResource) boo
 		before.Network.Host != after.Network.Host ||
 		before.Network.Port != after.Network.Port ||
 		before.Network.Protocol != after.Network.Protocol ||
+		before.Network.ADBConnection != after.Network.ADBConnection ||
+		before.Network.ADBSerial != after.Network.ADBSerial ||
 		before.Username != after.Username
 }
 
@@ -358,13 +383,12 @@ func (s *Service) PetUpdateHardwareResource(ctx context.Context, input PetHardwa
 		return PetHardwareResourceView{}, fmt.Errorf("hardware_resource_invalid: %w", err)
 	}
 	resource := petHardwareResourceFromGroup(group)
-	// ADB pairing only remains valid while the same ADB host stays configured.
-	if group.Mode != domain.HardwareModeNetwork ||
-		group.Network.Protocol != domain.HardwareProtocolADB ||
-		existing.Network.Host != group.Network.Host {
-		resource.ADBPairedAt = ""
-	} else {
+	// ADB pairing only remains valid while the same wireless host stays
+	// configured; switching to USB direct or another host drops it.
+	if hardwareconfig.ADBPairingStillValid(existing.Mode, existing.Network, group.Mode, group.Network) {
 		resource.ADBPairedAt = existing.ADBPairedAt
+	} else {
+		resource.ADBPairedAt = ""
 	}
 	// The pet must not be able to point an Owner-configured credential at a new
 	// endpoint (for example its own host), where auto-login would send the
diff --git a/internal/app/pet_takeover.go b/internal/app/pet_takeover.go
index c4aa464..ae6497e 100644
--- a/internal/app/pet_takeover.go
+++ b/internal/app/pet_takeover.go
@@ -164,8 +164,8 @@ func (s *Service) PetTakeoverRemoteTask(ctx context.Context, input PetTakeoverTa
 	}
 	for _, remote := range source.Hardware {
 		raw, ok := rawByID[remote.ID]
-		if !ok || remote.Serial.Device != "" && strings.TrimSpace(raw.Serial.Device) == "" || remote.Network.Host != "" && strings.TrimSpace(raw.Network.Host) == "" {
-			return domain.Task{}, nil, fmt.Errorf("硬件组 %s 必须重新提供本机串口和网络端点", remote.ID)
+		if !ok || remote.Serial.Device != "" && strings.TrimSpace(raw.Serial.Device) == "" || remote.Network.Host != "" && strings.TrimSpace(raw.Network.Host) == "" || remote.Network.ADBUSB() && strings.TrimSpace(raw.Network.ADBSerial) == "" && strings.TrimSpace(raw.Network.ADBConnection) != domain.HardwareADBConnectionWireless {
+			return domain.Task{}, nil, fmt.Errorf("硬件组 %s 必须重新提供本机串口、网络端点或 USB 设备序列号", remote.ID)
 		}
 		if remote.PasswordConfigured && raw.Password == "" {
 			if raw.ReuseRemoteCredential && s.secrets != nil {
@@ -242,6 +242,11 @@ func (s *Service) PetTakeoverRemoteTask(ctx context.Context, input PetTakeoverTa
 		if raw.Network.SSHAuth == "" {
 			raw.Network.SSHAuth = remote.Network.SSHAuth
 		}
+		// An omitted ADB connection form follows the remote resource, so a USB
+		// device is not silently reinterpreted as wireless (or the reverse).
+		if raw.Network.ADBConnection == "" && raw.Network.Protocol == domain.HardwareProtocolADB && remote.Network.Protocol == domain.HardwareProtocolADB {
+			raw.Network.ADBConnection = remote.Network.NormalizedADB().ADBConnection
+		}
 		if raw.Username == "" {
 			raw.Username = remote.Username
 		}
diff --git a/internal/httpapi/agent_handlers.go b/internal/httpapi/agent_handlers.go
index 6b4fab5..906c710 100644
--- a/internal/httpapi/agent_handlers.go
+++ b/internal/httpapi/agent_handlers.go
@@ -103,7 +103,7 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
 		return
 	}
 	if transport != domain.HardwareTransportNetwork || group.Network.Protocol != domain.HardwareProtocolADB {
-		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_exec_unavailable", "message": "当前硬件资源不是 ADB 网络设备"})
+		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_exec_unavailable", "message": "当前硬件资源不是 ADB 设备"})
 		return
 	}
 	if readOnly {
@@ -160,8 +160,20 @@ func (s *Server) agentADBExec(writer http.ResponseWriter, request *http.Request)
 		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_timeout_invalid", "message": "ADB 超时时间必须在 1 到 120 秒之间"})
 		return
 	}
-	endpoint := net.JoinHostPort(group.Network.Host, strconv.Itoa(group.Network.Port))
-	result, err := s.executeADB(request.Context(), endpoint, payload.Args, input, time.Duration(timeoutSeconds)*time.Second)
+	// The target comes only from this Task's bound resource: USB direct runs
+	// on that serial without connect/mDNS, network direct connects exactly
+	// its host:port without mDNS, wireless debugging keeps its mDNS path.
+	var result hardware.ADBExecResult
+	var err error
+	timeout := time.Duration(timeoutSeconds) * time.Second
+	switch {
+	case group.Network.ADBUSB():
+		result, err = s.executeADBUSB(request.Context(), group.Network.ADBSerial, payload.Args, input, timeout)
+	case group.Network.ADBTCP():
+		result, err = s.executeADBTCP(request.Context(), net.JoinHostPort(group.Network.Host, strconv.Itoa(group.Network.Port)), payload.Args, input, timeout)
+	default:
+		result, err = s.executeADB(request.Context(), net.JoinHostPort(group.Network.Host, strconv.Itoa(group.Network.Port)), payload.Args, input, timeout)
+	}
 	if err != nil {
 		code := http.StatusConflict
 		if errors.Is(err, hardware.ErrADBOutputTooLarge) {
diff --git a/internal/httpapi/hardware_handlers.go b/internal/httpapi/hardware_handlers.go
index d688177..db70329 100644
--- a/internal/httpapi/hardware_handlers.go
+++ b/internal/httpapi/hardware_handlers.go
@@ -52,7 +52,7 @@ func resourceFromPayload(
 		return domain.HardwareResource{}, fmt.Errorf("硬件资源必须选择串口或网络中的一种连接方式")
 	}
 	pairedAt := existing.ADBPairedAt
-	if group.Mode != domain.HardwareModeNetwork || group.Network.Protocol != domain.HardwareProtocolADB || existing.Network.Host != group.Network.Host {
+	if !hardwareconfig.ADBPairingStillValid(existing.Mode, existing.Network, group.Mode, group.Network) {
 		pairedAt = ""
 	}
 	return domain.HardwareResource{
@@ -132,7 +132,7 @@ func (s *Server) createHardwareResource(writer http.ResponseWriter, request *htt
 		return
 	}
 	s.audit(request, "hardware_resource.create", "hardware_resource", resource.ID, map[string]any{
-		"mode": resource.Mode, "access": resource.Access,
+		"mode": resource.Mode, "access": resource.Access, "adb_connection": resource.Network.ADBConnection,
 	})
 	writeJSON(writer, http.StatusCreated, map[string]any{"ok": true, "resource": resource})
 }
@@ -195,6 +195,7 @@ func (s *Server) updateHardwareResource(writer http.ResponseWriter, request *htt
 	}
 	s.audit(request, "hardware_resource.update", "hardware_resource", resource.ID, map[string]any{
 		"mode": resource.Mode, "access": resource.Access, "bindings": existing.BindingCount,
+		"adb_connection": resource.Network.ADBConnection,
 	})
 	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "resource": resource})
 }
@@ -233,6 +234,14 @@ func (s *Server) pairHardwareADB(writer http.ResponseWriter, request *http.Reque
 		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_pair_unavailable", "message": "当前硬件资源不是 ADB 网络设备"})
 		return
 	}
+	if resource.Network.ADBUSB() {
+		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_pair_unavailable", "message": "USB 直连资源不需要配对码，请在设备上允许 USB 调试"})
+		return
+	}
+	if resource.Network.ADBTCP() {
+		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "adb_pair_unavailable", "message": "网络直连资源不需要配对码，直接连接 IP:端口；首次连接时请在设备上允许调试"})
+		return
+	}
 	var payload struct {
 		PairingPort int    `json:"pairing_port"`
 		PairingCode string `json:"pairing_code"`
@@ -278,6 +287,38 @@ func (s *Server) pairHardwareADB(writer http.ResponseWriter, request *http.Reque
 	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "message": "Android 设备配对成功", "output": output, "resource": resource})
 }
 
+// hardwareADBUSBDevices lists USB-attached ADB devices on the AHA host so the
+// Owner can pick an explicit serial. Owner session only; it never connects,
+// pairs or changes any resource.
+func (s *Server) hardwareADBUSBDevices(writer http.ResponseWriter, request *http.Request) {
+	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
+	defer cancel()
+	list, err := s.listADBUSBDevices(ctx)
+	if err != nil {
+		writeJSON(writer, http.StatusConflict, map[string]any{
+			"ok": false, "error": "adb_usb_devices_failed", "message": adbUSBListFailureMessage(err),
+		})
+		return
+	}
+	if list.Devices == nil {
+		list.Devices = []hardware.ADBUSBDevice{}
+	}
+	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "devices": list.Devices, "other_count": list.OtherCount})
+}
+
+func adbUSBListFailureMessage(err error) string {
+	detail := strings.ToLower(err.Error())
+	switch {
+	case strings.Contains(detail, "adb executable not found"),
+		strings.Contains(detail, "configured adb executable is unavailable"):
+		return "找不到 ADB。请安装 Android Platform-Tools，或在 AHA2 运行账户中把 AHA2_ADB_PATH 设置为 adb.exe 的完整路径，然后重启 AHA2 User。"
+	case errors.Is(err, context.DeadlineExceeded):
+		return "读取 USB 设备列表超时，请稍后重试。"
+	default:
+		return "读取 USB 设备列表失败，请确认 AHA 宿主机上的 ADB 可以正常运行后重试。"
+	}
+}
+
 func adbPairingFailureMessage(err error) string {
 	detail := strings.ToLower(strings.TrimSpace(err.Error()))
 	switch {
diff --git a/internal/httpapi/server.go b/internal/httpapi/server.go
index 4446ebd..cf75afd 100644
--- a/internal/httpapi/server.go
+++ b/internal/httpapi/server.go
@@ -63,6 +63,9 @@ type Config struct {
 	TrustSSHHostKey     func(context.Context, string, string) (hardware.SSHHostKeyInfo, error)
 	PairADB             func(context.Context, string, string) (string, error)
 	ExecuteADB          func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
+	ExecuteADBUSB       func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
+	ExecuteADBTCP       func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
+	ListADBUSBDevices   func(context.Context) (hardware.ADBUSBDeviceList, error)
 	Version             string
 	WebVersion          string
 	// ListenAddress is what this process was launched with, reported so the UI can
@@ -122,6 +125,9 @@ type Server struct {
 	pairADB                  func(context.Context, string, string) (string, error)
 	backupPreviewTimeout     time.Duration
 	executeADB               func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
+	executeADBUSB            func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
+	executeADBTCP            func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error)
+	listADBUSBDevices        func(context.Context) (hardware.ADBUSBDeviceList, error)
 	version                  string
 	webVersion               string
 	startedAt                time.Time
@@ -173,6 +179,18 @@ func New(config Config) *Server {
 	if executeADB == nil {
 		executeADB = hardware.ExecuteADB
 	}
+	executeADBUSB := config.ExecuteADBUSB
+	if executeADBUSB == nil {
+		executeADBUSB = hardware.ExecuteADBUSB
+	}
+	executeADBTCP := config.ExecuteADBTCP
+	if executeADBTCP == nil {
+		executeADBTCP = hardware.ExecuteADBTCP
+	}
+	listADBUSBDevices := config.ListADBUSBDevices
+	if listADBUSBDevices == nil {
+		listADBUSBDevices = hardware.ListADBUSBDevices
+	}
 	mailTester := config.MailTester
 	if mailTester == nil {
 		mailTester = mailpkg.NewSMTPTester()
@@ -234,6 +252,9 @@ func New(config Config) *Server {
 		trustSSHHostKey:          trustSSHHostKey,
 		pairADB:                  pairADB,
 		executeADB:               executeADB,
+		executeADBUSB:            executeADBUSB,
+		executeADBTCP:            executeADBTCP,
+		listADBUSBDevices:        listADBUSBDevices,
 		version:                  config.Version,
 		webVersion:               config.WebVersion,
 		startedAt:                startedAt,
@@ -448,6 +469,9 @@ func (s *Server) Handler() http.Handler {
 	mux.Handle("PUT /api/v1/hardware-resources/{id}", s.withAuth(http.HandlerFunc(s.updateHardwareResource)))
 	mux.Handle("DELETE /api/v1/hardware-resources/{id}", s.withAuth(http.HandlerFunc(s.deleteHardwareResource)))
 	mux.Handle(hardwareADBPairRoute, s.withAuth(http.HandlerFunc(s.pairHardwareADB)))
+	// Owner-only: lists USB ADB devices on the AHA host for resource config.
+	// POST so the adb server start it may cause needs CSRF + same origin.
+	mux.Handle("POST /api/v1/hardware/adb/usb-devices", s.withAuth(http.HandlerFunc(s.hardwareADBUSBDevices)))
 	s.registerBrowserRoutes(mux)
 
 	// Mail resources phase one task B: Owner-only account/template/binding
diff --git a/internal/httpapi/takeover_handlers.go b/internal/httpapi/takeover_handlers.go
index 4e06a5e..6e01039 100644
--- a/internal/httpapi/takeover_handlers.go
+++ b/internal/httpapi/takeover_handlers.go
@@ -157,8 +157,8 @@ func (s *Server) takeoverTask(writer http.ResponseWriter, request *http.Request)
 	}
 	for _, remote := range source.Hardware {
 		raw, ok := rawByID[remote.ID]
-		if !ok || remote.Serial.Device != "" && strings.TrimSpace(raw.Serial.Device) == "" || remote.Network.Host != "" && strings.TrimSpace(raw.Network.Host) == "" {
-			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "local_hardware_fields_required", "message": fmt.Sprintf("硬件组 %s 必须重新提供本机串口和网络端点", remote.ID)})
+		if !ok || remote.Serial.Device != "" && strings.TrimSpace(raw.Serial.Device) == "" || remote.Network.Host != "" && strings.TrimSpace(raw.Network.Host) == "" || remote.Network.ADBUSB() && strings.TrimSpace(raw.Network.ADBSerial) == "" && strings.TrimSpace(raw.Network.ADBConnection) != domain.HardwareADBConnectionWireless {
+			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "local_hardware_fields_required", "message": fmt.Sprintf("硬件组 %s 必须重新提供本机串口、网络端点或 USB 设备序列号", remote.ID)})
 			return
 		}
 		if remote.PasswordConfigured && raw.Password == "" {
@@ -240,6 +240,11 @@ func (s *Server) takeoverTask(writer http.ResponseWriter, request *http.Request)
 		if raw.Network.SSHAuth == "" {
 			raw.Network.SSHAuth = remote.Network.SSHAuth
 		}
+		// An omitted ADB connection form follows the remote resource, so a USB
+		// device is not silently reinterpreted as wireless (or the reverse).
+		if raw.Network.ADBConnection == "" && raw.Network.Protocol == domain.HardwareProtocolADB && remote.Network.Protocol == domain.HardwareProtocolADB {
+			raw.Network.ADBConnection = remote.Network.NormalizedADB().ADBConnection
+		}
 		if raw.Username == "" {
 			raw.Username = remote.Username
 		}
diff --git a/internal/prompt/registry.go b/internal/prompt/registry.go
index 0efcf85..61e72cc 100644
--- a/internal/prompt/registry.go
+++ b/internal/prompt/registry.go
@@ -221,6 +221,9 @@ type hardwareContextGroup struct {
 	NetworkHost        string
 	NetworkPort        int
 	NetworkProtocol    string
+	ADBUSB             bool
+	ADBSerial          string
+	ADBConnection      string
 	SSHAuthentication  string
 	Username           string
 	PasswordConfigured bool
@@ -2199,6 +2202,12 @@ func hardwareContextGroups(groups []domain.HardwareGroup) []hardwareContextGroup
 			HasNetwork: group.Supports(domain.HardwareTransportNetwork), NetworkHost: group.Network.Host, NetworkPort: group.Network.Port,
 			NetworkProtocol: string(group.Network.Protocol), Username: group.Username, PasswordConfigured: group.PasswordConfigured,
 		}
+		if item.HasNetwork && group.Network.Protocol == domain.HardwareProtocolADB {
+			item.ADBConnection = group.Network.NormalizedADB().ADBConnection
+		}
+		if item.HasNetwork && group.Network.ADBUSB() {
+			item.ADBUSB, item.ADBSerial = true, group.Network.ADBSerial
+		}
 		if item.HasNetwork && group.Network.Protocol == domain.HardwareProtocolSSH {
 			item.SSHAuthentication = normalizeHardwareSSHAuth(group.Network.SSHAuth)
 		}
diff --git a/internal/prompt/templates/context-hardware.md b/internal/prompt/templates/context-hardware.md
index 3b1177a..85f6c2c 100644
--- a/internal/prompt/templates/context-hardware.md
+++ b/internal/prompt/templates/context-hardware.md
@@ -7,8 +7,9 @@ Credentials are never included in this file.
 - access: {{.Access}}
 - mode: {{.Mode}}
 {{if .HasSerial}}- serial: {{.SerialDevice}} @ {{.SerialBaudrate}}
-{{end}}{{if .HasNetwork}}- network: {{.NetworkHost}}:{{.NetworkPort}} ({{.NetworkProtocol}})
-{{end}}{{if .SSHAuthentication}}- ssh authentication: {{.SSHAuthentication}}
+{{end}}{{if .HasNetwork}}{{if .ADBUSB}}- network: ADB USB direct, device serial {{.ADBSerial}}
+{{else}}- network: {{.NetworkHost}}:{{.NetworkPort}} ({{.NetworkProtocol}}{{if eq .ADBConnection "tcp"}}, network direct without pairing{{else if eq .ADBConnection "wireless"}}, Android wireless debugging{{end}})
+{{end}}{{end}}{{if .SSHAuthentication}}- ssh authentication: {{.SSHAuthentication}}
 {{end}}{{if .Username}}- username: {{.Username}}
 {{end}}- password configured: {{.PasswordConfigured}}
 {{end}}
diff --git a/internal/prompt/templates/zh-CN/context-hardware.md b/internal/prompt/templates/zh-CN/context-hardware.md
index e96bd8e..25c967d 100644
--- a/internal/prompt/templates/zh-CN/context-hardware.md
+++ b/internal/prompt/templates/zh-CN/context-hardware.md
@@ -7,8 +7,9 @@
 - 访问方式：{{.Access}}
 - 模式：{{.Mode}}
 {{if .HasSerial}}- 串口：{{.SerialDevice}} @ {{.SerialBaudrate}}
-{{end}}{{if .HasNetwork}}- 网络：{{.NetworkHost}}:{{.NetworkPort}}（{{.NetworkProtocol}}）
-{{end}}{{if .SSHAuthentication}}- SSH 认证：{{.SSHAuthentication}}
+{{end}}{{if .HasNetwork}}{{if .ADBUSB}}- 网络：ADB USB 直连，设备序列号 {{.ADBSerial}}
+{{else}}- 网络：{{.NetworkHost}}:{{.NetworkPort}}（{{.NetworkProtocol}}{{if eq .ADBConnection "tcp"}}，网络直连免配对{{else if eq .ADBConnection "wireless"}}，Android 无线调试{{end}}）
+{{end}}{{end}}{{if .SSHAuthentication}}- SSH 认证：{{.SSHAuthentication}}
 {{end}}{{if .Username}}- 用户名：{{.Username}}
 {{end}}- 已配置密码：{{.PasswordConfigured}}
 {{end}}
~~~~

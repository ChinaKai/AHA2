package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func enablePetHardwareTestSettings(t *testing.T, fixture *petTaskFixture, manageSystem bool) {
	t.Helper()
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true), AllowManageSystem: boolPointer(manageSystem),
	}); err != nil {
		t.Fatal(err)
	}
}

func petHardwareToolCall(t *testing.T, fixture *petTaskFixture, name, args string) string {
	t.Helper()
	raw, err := fixture.service.PetMCPTool(fixture.ctx, name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s(%s): %v", name, args, err)
	}
	return raw
}

func decodePetHardwareView(t *testing.T, raw string) PetHardwareResourceView {
	t.Helper()
	var view PetHardwareResourceView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("decode hardware resource view %q: %v", raw, err)
	}
	return view
}

func auditDataByAction(t *testing.T, databasePath string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT action, data_json FROM audit_events WHERE action LIKE 'pet.hardware_resource.%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var action, data string
		if err := rows.Scan(&action, &data); err != nil {
			t.Fatal(err)
		}
		result[action] = data
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPetHardwareResourcesCreateUpdateSerial(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	createdRaw := petHardwareToolCall(t, fixture,
		"aha_create_hardware_resource",
		`{"id":"hw-serial-test","description":"测试串口","mode":"serial","access":"read_only","serial":{"device":"/dev/ttyUSB9","baudrate":9600}}`,
	)
	for _, forbidden := range []string{"credential_ref", `"password":`} {
		if strings.Contains(createdRaw, forbidden) {
			t.Fatalf("create response leaked %s: %s", forbidden, createdRaw)
		}
	}
	created := decodePetHardwareView(t, createdRaw)
	if created.ID != "hw-serial-test" || created.Mode != domain.HardwareModeSerial ||
		created.Serial.Device != "/dev/ttyUSB9" || created.Serial.Baudrate != 9600 ||
		created.Access != domain.HardwareAccessReadOnly || created.PasswordConfigured {
		t.Fatalf("created resource=%#v", created)
	}
	if created.UpdatedAt.IsZero() {
		t.Fatal("created resource has no updated_at")
	}

	list := petHardwareToolCall(t, fixture, "aha_hardware_resources", `{}`)
	for _, want := range []string{`"serial":{"device":"/dev/ttyUSB9","baudrate":9600}`, `"username":""`, `"updated_at":`} {
		if !strings.Contains(list, want) {
			t.Fatalf("hardware list %s missing %s", list, want)
		}
	}
	if strings.Contains(list, "credential_ref") {
		t.Fatalf("hardware list leaked credential_ref: %s", list)
	}

	updated := decodePetHardwareView(t, petHardwareToolCall(t, fixture,
		"aha_update_hardware_resource",
		`{"resource_id":"hw-serial-test","serial":{"baudrate":115200}}`,
	))
	if updated.Serial.Baudrate != 115200 || updated.Serial.Device != "/dev/ttyUSB9" {
		t.Fatalf("updated resource=%#v", updated)
	}
	stored, err := fixture.database.HardwareResource(fixture.ctx, "hw-serial-test")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Serial.Baudrate != 115200 {
		t.Fatalf("stored baudrate=%d", stored.Serial.Baudrate)
	}
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_update_hardware_resource",
		json.RawMessage(`{"resource_id":"hw-serial-test","serial":{"baudrate":0}}`)); err == nil ||
		!strings.Contains(err.Error(), "hardware_resource_invalid") {
		t.Fatalf("update invalid baudrate err=%v", err)
	}

	audits := auditDataByAction(t, fixture.databasePath)
	createData, ok := audits["pet.hardware_resource.create"]
	if !ok || !strings.Contains(createData, `"changed_fields"`) || !strings.Contains(createData, `"hw-serial-test"`) {
		t.Fatalf("create audit=%q all=%v", createData, audits)
	}
	updateData, ok := audits["pet.hardware_resource.update"]
	if !ok || !strings.Contains(updateData, "serial.baudrate") {
		t.Fatalf("update audit=%q all=%v", updateData, audits)
	}
	for action, data := range audits {
		if strings.Contains(data, "credential_ref") || strings.Contains(data, "password") {
			t.Fatalf("audit %s leaked credential material: %s", action, data)
		}
	}
}

func TestPetHardwareResourcesCreateUpdateNetwork(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	created := decodePetHardwareView(t, petHardwareToolCall(t, fixture,
		"aha_create_hardware_resource",
		`{"id":"hw-net-test","description":"测试网络","mode":"network","access":"read_write","network":{"host":"192.168.1.20","port":2222,"protocol":"ssh","ssh_auth":"password"},"username":"root"}`,
	))
	if created.Mode != domain.HardwareModeNetwork || created.Network.Host != "192.168.1.20" ||
		created.Network.Port != 2222 || created.Network.Protocol != domain.HardwareProtocolSSH ||
		created.Network.SSHAuth != domain.HardwareSSHAuthPassword || created.Username != "root" ||
		created.Access != domain.HardwareAccessReadWrite {
		t.Fatalf("created network resource=%#v", created)
	}

	updated := decodePetHardwareView(t, petHardwareToolCall(t, fixture,
		"aha_update_hardware_resource",
		`{"resource_id":"hw-net-test","network":{"host":"192.168.1.21"},"username":"admin"}`,
	))
	if updated.Network.Host != "192.168.1.21" || updated.Network.Port != 2222 ||
		updated.Network.Protocol != domain.HardwareProtocolSSH || updated.Username != "admin" {
		t.Fatalf("partial network update=%#v", updated)
	}

	invalid := []struct {
		name string
		args string
	}{
		{"mode-both", `{"mode":"both","description":"x"}`},
		{"mode-off", `{"mode":"off","description":"x"}`},
		{"serial-missing-device", `{"mode":"serial","serial":{"baudrate":9600}}`},
		{"serial-zero-baud", `{"mode":"serial","serial":{"device":"/dev/ttyX","baudrate":0}}`},
		{"network-missing-host", `{"mode":"network","network":{"port":23,"protocol":"telnet"}}`},
		{"network-zero-port", `{"mode":"network","network":{"host":"10.0.0.1","port":0,"protocol":"telnet"}}`},
		{"network-large-port", `{"mode":"network","network":{"host":"10.0.0.1","port":70000,"protocol":"telnet"}}`},
		{"network-protocol", `{"mode":"network","network":{"host":"10.0.0.1","port":23,"protocol":"ftp"}}`},
	}
	for _, item := range invalid {
		_, err := fixture.service.PetMCPTool(fixture.ctx, "aha_create_hardware_resource", json.RawMessage(item.args))
		if err == nil || !strings.Contains(err.Error(), "hardware_resource_invalid") {
			t.Fatalf("%s: err=%v, want hardware_resource_invalid", item.name, err)
		}
	}

	_, err := fixture.service.PetMCPTool(fixture.ctx, "aha_update_hardware_resource",
		json.RawMessage(`{"resource_id":"hw-missing","description":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "hardware_resource_not_found") {
		t.Fatalf("update missing resource err=%v", err)
	}
	_, err = fixture.service.PetMCPTool(fixture.ctx, "aha_update_hardware_resource",
		json.RawMessage(`{"resource_id":"hw-net-test","network":{"host":""}}`))
	if err == nil || !strings.Contains(err.Error(), "hardware_resource_invalid") {
		t.Fatalf("update invalid host err=%v", err)
	}
}

func TestPetHardwareResourcesRejectOwnerCredentials(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	const secret = "super-secret-value"
	for _, field := range []string{"password", "credential_ref", "clear_secret", "Password", "Credential_Ref"} {
		args := fmt.Sprintf(
			`{"id":"hw-cred-%s","description":"x","mode":"serial","serial":{"device":"/dev/ttyS0","baudrate":9600},"%s":"%s"}`,
			field, field, secret,
		)
		_, err := fixture.service.PetMCPTool(fixture.ctx, "aha_create_hardware_resource", json.RawMessage(args))
		if err == nil || !strings.Contains(err.Error(), "hardware_credential_owner_only") {
			t.Fatalf("create with %s: err=%v, want hardware_credential_owner_only", field, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error for %s leaked the secret: %v", field, err)
		}
		if _, err := fixture.database.HardwareResource(fixture.ctx, "hw-cred-"+field); err == nil {
			t.Fatalf("resource was created despite rejected %s", field)
		}
	}
	// The update rejection must run against a real resource, otherwise the
	// credential check is the only thing that could have failed.
	petHardwareToolCall(t, fixture, "aha_create_hardware_resource",
		`{"id":"hw-net-test","description":"凭据拒绝用例","mode":"network","network":{"host":"10.0.0.1","port":23,"protocol":"telnet"}}`)
	for _, field := range []string{"password", "credential_ref", "clear_secret", "Password", "Clear_Secret"} {
		args := fmt.Sprintf(`{"resource_id":"hw-net-test","%s":"%s"}`, field, secret)
		_, err := fixture.service.PetMCPTool(fixture.ctx, "aha_update_hardware_resource", json.RawMessage(args))
		if err == nil || !strings.Contains(err.Error(), "hardware_credential_owner_only") {
			t.Fatalf("update with %s: err=%v, want hardware_credential_owner_only", field, err)
		}
	}
	nested := []struct {
		name string
		tool string
		args string
	}{
		{"create-nested-serial", "aha_create_hardware_resource", fmt.Sprintf(`{"mode":"serial","serial":{"device":"/dev/ttyS0","baudrate":9600,"password":"%s"}}`, secret)},
		{"create-nested-network", "aha_create_hardware_resource", fmt.Sprintf(`{"mode":"network","network":{"host":"10.0.0.1","credential_ref":"%s"}}`, secret)},
		{"update-nested-network", "aha_update_hardware_resource", fmt.Sprintf(`{"resource_id":"hw-net-test","network":{"host":"10.0.0.2","clear_secret":"%s"}}`, secret)},
		{"create-uppercase-section", "aha_create_hardware_resource", fmt.Sprintf(`{"mode":"network","Network":{"host":"10.0.0.1","Password":"%s"}}`, secret)},
	}
	for _, item := range nested {
		_, err := fixture.service.PetMCPTool(fixture.ctx, item.tool, json.RawMessage(item.args))
		if err == nil || !strings.Contains(err.Error(), "hardware_credential_owner_only") {
			t.Fatalf("%s: err=%v, want hardware_credential_owner_only", item.name, err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("%s leaked the secret: %v", item.name, err)
		}
	}
	audits := auditDataByAction(t, fixture.databasePath)
	for action, data := range audits {
		if strings.Contains(data, secret) {
			t.Fatalf("audit %s leaked the secret: %s", action, data)
		}
	}
}

func TestPetHardwareResourcesRejectTargetChangeWithOwnerCredential(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	petHardwareToolCall(t, fixture, "aha_create_hardware_resource",
		`{"id":"hw-locked-serial","description":"锁定串口","mode":"serial","serial":{"device":"/dev/ttyUSB0","baudrate":9600}}`)
	petHardwareToolCall(t, fixture, "aha_create_hardware_resource",
		`{"id":"hw-locked-net","description":"锁定网络","mode":"network","network":{"host":"10.0.0.10","port":22,"protocol":"ssh","ssh_auth":"password"},"username":"root"}`)

	credentialRefs := map[string]string{
		"hw-locked-serial": "hardware-resource/hw-locked-serial/credential",
		"hw-locked-net":    "hardware-resource/hw-locked-net/credential",
	}
	before := map[string]domain.HardwareResource{}
	for id, ref := range credentialRefs {
		stored, err := fixture.database.HardwareResource(fixture.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		stored.CredentialRef = ref
		stored.PasswordConfigured = true
		if err := fixture.database.UpsertHardwareResource(fixture.ctx, stored); err != nil {
			t.Fatal(err)
		}
		if before[id], err = fixture.database.HardwareResource(fixture.ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	blocked := []struct {
		name       string
		resourceID string
		args       string
	}{
		{"mode", "hw-locked-serial", `{"resource_id":"hw-locked-serial","mode":"network","network":{"host":"10.1.1.1","port":23,"protocol":"telnet"}}`},
		{"serial-device", "hw-locked-serial", `{"resource_id":"hw-locked-serial","serial":{"device":"/dev/ttyUSB9"}}`},
		{"network-host", "hw-locked-net", `{"resource_id":"hw-locked-net","network":{"host":"10.0.0.11"}}`},
		{"network-port", "hw-locked-net", `{"resource_id":"hw-locked-net","network":{"port":2222}}`},
		{"network-protocol", "hw-locked-net", `{"resource_id":"hw-locked-net","network":{"protocol":"telnet"}}`},
		{"username", "hw-locked-net", `{"resource_id":"hw-locked-net","username":"admin"}`},
	}
	for _, item := range blocked {
		_, err := fixture.service.PetMCPTool(fixture.ctx, "aha_update_hardware_resource", json.RawMessage(item.args))
		if err == nil || !strings.Contains(err.Error(), "hardware_credential_owner_only") {
			t.Fatalf("%s: err=%v, want hardware_credential_owner_only", item.name, err)
		}
		if strings.Contains(err.Error(), credentialRefs[item.resourceID]) {
			t.Fatalf("%s leaked credential_ref: %v", item.name, err)
		}
		stored, err := fixture.database.HardwareResource(fixture.ctx, item.resourceID)
		if err != nil {
			t.Fatal(err)
		}
		if stored != before[item.resourceID] {
			t.Fatalf("%s changed the resource: before=%#v after=%#v", item.name, before[item.resourceID], stored)
		}
	}

	// Non-target updates stay allowed and must keep the Owner credential bound.
	updated := decodePetHardwareView(t, petHardwareToolCall(t, fixture,
		"aha_update_hardware_resource",
		`{"resource_id":"hw-locked-net","description":"改名后的网络","access":"read_only"}`))
	if updated.Description != "改名后的网络" || updated.Access != domain.HardwareAccessReadOnly {
		t.Fatalf("control update=%#v", updated)
	}
	stored, err := fixture.database.HardwareResource(fixture.ctx, "hw-locked-net")
	if err != nil {
		t.Fatal(err)
	}
	if stored.CredentialRef != credentialRefs["hw-locked-net"] || !stored.PasswordConfigured {
		t.Fatalf("control update dropped Owner credential: %#v", stored)
	}
	if stored.Network.Host != "10.0.0.10" || stored.Network.Port != 22 {
		t.Fatalf("control update changed target: %#v", stored)
	}

	audits := auditDataByAction(t, fixture.databasePath)
	for action, data := range audits {
		for _, ref := range credentialRefs {
			if strings.Contains(data, ref) {
				t.Fatalf("audit %s leaked credential_ref: %s", action, data)
			}
		}
	}
}

func TestPetHardwareResourcesRequireManageSystem(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, false)

	args := json.RawMessage(`{"description":"x","mode":"serial","serial":{"device":"/dev/ttyS0","baudrate":9600}}`)
	if _, err := fixture.service.PetMCPTool(fixture.ctx, "aha_create_hardware_resource", args); err == nil ||
		!strings.Contains(err.Error(), "主人未开启") {
		t.Fatalf("create without manage_system err=%v", err)
	}
	var input PetHardwareResourceInput
	if err := json.Unmarshal(args, &input); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetCreateHardwareResource(fixture.ctx, input); err == nil ||
		!strings.Contains(err.Error(), "管理系统设置权限") {
		t.Fatalf("service create without manage_system err=%v", err)
	}
	if _, err := fixture.service.PetUpdateHardwareResource(fixture.ctx, PetHardwareResourceInput{ResourceID: "missing"}); err == nil ||
		!strings.Contains(err.Error(), "管理系统设置权限") {
		t.Fatalf("service update without manage_system err=%v", err)
	}
	// Read-only listing and Task-level tooling are not gated by manage_system.
	if _, err := fixture.service.PetHardwareResources(fixture.ctx); err != nil {
		t.Fatalf("hardware list should still work: %v", err)
	}
}

type recordingHardwareDisconnector struct {
	calls []string
}

func (recorder *recordingHardwareDisconnector) Disconnect(taskID, hardwareID, transport string) {
	recorder.calls = append(recorder.calls, taskID+"|"+hardwareID+"|"+transport)
}

func TestPetHardwareResourcesUpdateDisconnectsBoundResource(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)
	recorder := &recordingHardwareDisconnector{}
	fixture.service.SetPetHardwareDisconnector(recorder)

	serial := decodePetHardwareView(t, petHardwareToolCall(t, fixture,
		"aha_create_hardware_resource",
		`{"id":"hw-bound-serial","description":"串口","mode":"serial","serial":{"device":"/dev/ttyUSB1","baudrate":9600}}`,
	))
	network := decodePetHardwareView(t, petHardwareToolCall(t, fixture,
		"aha_create_hardware_resource",
		`{"id":"hw-bound-net","description":"网络","mode":"network","network":{"host":"10.0.0.5","port":23,"protocol":"telnet"}}`,
	))

	project, err := fixture.service.PetCreateProject(fixture.ctx, PetCreateProjectInput{Name: "硬件项目"})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{
		ProjectID: project.ID, Name: "硬件 Workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := fixture.service.PetCreateTask(fixture.ctx, PetCreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "硬件任务",
		Request: "等待硬件", Start: boolPointer(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PetUpdateTaskHardware(fixture.ctx, PetUpdateTaskHardwareInput{
		TaskID: task.ID, ResourceIDs: []string{serial.ID, network.ID},
	}); err != nil {
		t.Fatal(err)
	}
	bindings, err := fixture.database.TaskHardwareBindings(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	byResource := map[string]string{}
	for _, binding := range bindings {
		byResource[binding.ResourceID] = binding.ID
	}

	petHardwareToolCall(t, fixture, "aha_update_hardware_resource",
		`{"resource_id":"hw-bound-serial","serial":{"device":"/dev/ttyUSB2"}}`)
	petHardwareToolCall(t, fixture, "aha_update_hardware_resource",
		`{"resource_id":"hw-bound-net","network":{"host":"10.0.0.6"}}`)

	want := map[string]bool{
		task.ID + "|" + byResource[serial.ID] + "|serial":   false,
		task.ID + "|" + byResource[network.ID] + "|network": false,
	}
	for _, call := range recorder.calls {
		if _, ok := want[call]; ok {
			want[call] = true
		}
	}
	for call, seen := range want {
		if !seen {
			t.Fatalf("disconnect %s not called; calls=%v", call, recorder.calls)
		}
	}
}

func TestPetHardwareResourcesUpdateClearsADBPairing(t *testing.T) {
	fixture := newPetTaskFixture(t)
	enablePetHardwareTestSettings(t, fixture, true)

	petHardwareToolCall(t, fixture,
		"aha_create_hardware_resource",
		`{"id":"hw-adb","description":"ADB 设备","mode":"network","network":{"host":"10.0.0.9","port":5555,"protocol":"adb"}}`)
	stored, err := fixture.database.HardwareResource(fixture.ctx, "hw-adb")
	if err != nil {
		t.Fatal(err)
	}
	stored.ADBPairedAt = "2026-01-01T00:00:00Z"
	if err := fixture.database.UpsertHardwareResource(fixture.ctx, stored); err != nil {
		t.Fatal(err)
	}

	// Non-connection updates keep the pairing state.
	kept := decodePetHardwareView(t, petHardwareToolCall(t, fixture,
		"aha_update_hardware_resource",
		`{"resource_id":"hw-adb","description":"ADB 设备（改名）"}`))
	if kept.ADBPairedAt == "" {
		t.Fatal("adb_paired_at was cleared by a description-only update")
	}

	// Changing the host invalidates the pairing for the old endpoint.
	cleared := decodePetHardwareView(t, petHardwareToolCall(t, fixture,
		"aha_update_hardware_resource",
		`{"resource_id":"hw-adb","network":{"host":"10.0.0.10"}}`))
	if cleared.ADBPairedAt != "" {
		t.Fatalf("adb_paired_at=%q after host change, want empty", cleared.ADBPairedAt)
	}
	stored, err = fixture.database.HardwareResource(fixture.ctx, "hw-adb")
	if err != nil {
		t.Fatal(err)
	}
	if stored.ADBPairedAt != "" {
		t.Fatalf("stored adb_paired_at=%q after host change, want empty", stored.ADBPairedAt)
	}
}

func TestPetHardwareResourceToolRegistrationAndSchema(t *testing.T) {
	tools := map[string]bool{}
	for _, tool := range ahaPetTools() {
		tools[tool.Name] = true
	}
	for _, name := range []string{"aha_create_hardware_resource", "aha_update_hardware_resource"} {
		if !tools[name] {
			t.Fatalf("%s is not registered", name)
		}
		if !isAhaPetTool(name) {
			t.Fatalf("%s is not recognized as an AHA pet tool", name)
		}
		allowed := domain.OwnerAssistant{PetAllowManageSystem: true}
		if !petToolAllowed(allowed, name) {
			t.Fatalf("%s is not allowed with manage_system", name)
		}
		denied := domain.OwnerAssistant{PetAllowViewTasks: true, PetAllowCreateTasks: true}
		if petToolAllowed(denied, name) {
			t.Fatalf("%s is allowed without manage_system", name)
		}
	}

	allowedTools := map[string]bool{}
	for _, name := range petMCPAllowedTools(domain.Task{ID: petTaskID}) {
		allowedTools[name] = true
	}
	for _, name := range []string{"aha_create_hardware_resource", "aha_update_hardware_resource"} {
		if !allowedTools["mcp__aha2-pet__"+name] {
			t.Fatalf("%s is not exposed through the pet MCP allowlist", name)
		}
	}

	for _, tool := range ahaPetTools() {
		params := tool.Parameters
		required, _ := params["required"].([]string)
		switch tool.Name {
		case "aha_create_hardware_resource":
			if len(required) != 1 || required[0] != "mode" {
				t.Fatalf("create required=%v", required)
			}
			properties, _ := params["properties"].(map[string]any)
			if _, ok := properties["serial"]; !ok {
				t.Fatalf("create schema missing serial: %#v", properties)
			}
			if _, ok := properties["network"]; !ok {
				t.Fatalf("create schema missing network: %#v", properties)
			}
		case "aha_update_hardware_resource":
			if len(required) != 1 || required[0] != "resource_id" {
				t.Fatalf("update required=%v", required)
			}
		}
	}
}

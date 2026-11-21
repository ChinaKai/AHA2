package app

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardwareconfig"
	"github.com/ChinaKai/AHA2/internal/store"
)

// A remote USB serial belongs to the other machine: takeover must ask for a
// local serial, and an omitted adb_connection follows the remote resource so
// USB / network direct / wireless never silently swap meaning.
func TestPetTakeoverKeepsADBConnectionForms(t *testing.T) {
	fixture := newPetTaskFixture(t)
	if _, err := fixture.service.OwnerAssistant(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateOwnerPetSettings(fixture.ctx, PetSettingsInput{
		Enabled: true, ChatModelID: fixture.model.ID, Actions: []string{"walk"},
		IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
		AllowViewTasks: boolPointer(true), AllowCreateTasks: boolPointer(true),
		AllowCreateWorkspaces: boolPointer(true), AllowMessageTasks: boolPointer(true),
		AllowManageModels: boolPointer(true),
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{ID: "project-adb-takeover", Name: "ADB 接管", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	if err := fixture.database.CreateProject(fixture.ctx, project); err != nil {
		t.Fatal(err)
	}
	localWorkspace, err := fixture.service.PetCreateWorkspace(fixture.ctx, PetCreateWorkspaceInput{ProjectID: project.ID, Name: "本机", RootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	remoteTask := domain.Task{
		ID: "remote-adb-task", ProjectID: project.ID, WorkspaceID: localWorkspace.ID, Title: "远端 ADB",
		OriginalRequest: "调试设备", Status: domain.TaskWaitingUser, CollaborationMode: "single", MaxAgents: 1,
		KnowledgePolicy: project.KnowledgePolicy, CreatedAt: now, UpdatedAt: now,
	}
	taskPayload, _ := json.Marshal(remoteTask)
	objects := []store.RemoteTaskObject{{OwnerDeviceID: "device-remote", ObjectType: "task", ObjectID: remoteTask.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: taskPayload}}
	for _, group := range []domain.HardwareGroup{
		{ID: "usb", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Protocol: "adb", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "REMOTE-SER"}},
		{ID: "tv", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "10.0.0.9", Port: 5601, Protocol: "adb", SSHAuth: "auto", ADBConnection: "tcp"}},
		// An older peer never sent adb_connection: it stays wireless.
		{ID: "phone", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "10.0.0.10", Port: 41235, Protocol: "adb", SSHAuth: "auto"}},
	} {
		group.TaskID, group.Access, group.CreatedAt, group.UpdatedAt = remoteTask.ID, domain.HardwareAccessReadOnly, now, now
		payload, _ := json.Marshal(group)
		objects = append(objects, store.RemoteTaskObject{OwnerDeviceID: "device-remote", ObjectType: "hardware", ObjectID: group.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: payload})
	}
	for _, item := range objects {
		if err := fixture.database.UpsertRemoteTaskObject(fixture.ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	mirrors, err := fixture.service.PetRemoteTaskMirrors(fixture.ctx, project.ID)
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("mirrors=%#v err=%v", mirrors, err)
	}
	groups := func(usbSerial string) []hardwareconfig.GroupPayload {
		return []hardwareconfig.GroupPayload{
			{ID: "usb", Network: domain.HardwareNetworkConfig{ADBSerial: usbSerial}},
			{ID: "tv", Network: domain.HardwareNetworkConfig{Host: "10.0.0.19"}},
			{ID: "phone", Network: domain.HardwareNetworkConfig{Host: "10.0.0.20"}},
		}
	}
	before := takeoverRowCounts(t, fixture.databasePath)
	// Layer 1 (pet_takeover): a remote USB group without a local serial is
	// refused before any Task is created. The message is the takeover guard's,
	// not NormalizeGroup's "缺少 USB 设备序列号".
	if _, _, err := fixture.service.PetTakeoverRemoteTask(fixture.ctx, PetTakeoverTaskInput{
		TaskID: mirrors[0].ID, WorkspaceID: localWorkspace.ID, ModelID: fixture.model.ID, Groups: groups(""),
	}); err == nil || !strings.Contains(err.Error(), "必须重新提供本机串口、网络端点或 USB 设备序列号") {
		t.Fatalf("takeover without a local USB serial must be refused by the takeover guard: %v", err)
	}
	if after := takeoverRowCounts(t, fixture.databasePath); after != before {
		t.Fatalf("guard rejection left rows behind: before=%v after=%v", before, after)
	}
	// Layer 2 (NormalizeGroup, after CreateTask): an invalid serial is refused
	// and the half-created Task is cleaned up.
	if _, _, err := fixture.service.PetTakeoverRemoteTask(fixture.ctx, PetTakeoverTaskInput{
		TaskID: mirrors[0].ID, WorkspaceID: localWorkspace.ID, ModelID: fixture.model.ID, Groups: groups("192.0.2.4"),
	}); err == nil || !strings.Contains(err.Error(), "USB 设备序列号无效") {
		t.Fatalf("takeover with an IP-literal USB serial must be refused by validation: %v", err)
	}
	if after := takeoverRowCounts(t, fixture.databasePath); after != before {
		t.Fatalf("validation rejection left a half-created Task behind: before=%v after=%v", before, after)
	}
	_, taken, err := fixture.service.PetTakeoverRemoteTask(fixture.ctx, PetTakeoverTaskInput{
		TaskID: mirrors[0].ID, WorkspaceID: localWorkspace.ID, ModelID: fixture.model.ID, Groups: groups("LOCAL-SER"),
	})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]domain.HardwareGroup{}
	for _, group := range taken {
		byID[group.ID] = group
	}
	if usb := byID["usb"].Network; !usb.ADBUSB() || usb.ADBSerial != "LOCAL-SER" || usb.Host != "" {
		t.Fatalf("taken USB=%#v", usb)
	}
	if tv := byID["tv"].Network; !tv.ADBTCP() || tv.Host != "10.0.0.19" || tv.Port != 5601 {
		t.Fatalf("taken network direct=%#v", tv)
	}
	if phone := byID["phone"].Network; !phone.ADBWireless() || phone.ADBConnection != "wireless" || phone.Port != 41235 {
		t.Fatalf("taken wireless=%#v", phone)
	}
}

// takeoverRowCounts snapshots the rows a takeover may create, so a rejected
// takeover can be shown to leave nothing behind.
func takeoverRowCounts(t *testing.T, databasePath string) [4]int {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var counts [4]int
	for index, table := range []string{"tasks", "hardware_resources", "task_hardware_bindings", "runtime_config_snapshots"} {
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&counts[index]); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return counts
}

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// The Web takeover endpoint mirrors the pet path: a remote USB serial belongs
// to the other machine, and an omitted adb_connection follows the remote
// resource so USB / network direct / wireless never swap meaning.
func TestTaskTakeoverKeepsADBConnectionForms(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "adb-takeover-project", Name: "ADB", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	local := domain.Workspace{ID: "adb-local", ProjectID: project.ID, Name: "Local", Locality: "local", Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateWorkspace(ctx, local); err != nil {
		t.Fatal(err)
	}
	env := domain.EnvGroup{ID: "adb-env", Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1, Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	model := domain.Model{ID: "adb-model", DisplayName: "Model", ProviderID: "stub", Backend: "stub", WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.UpsertEnvGroup(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertModel(ctx, model); err != nil {
		t.Fatal(err)
	}
	remoteTask := domain.Task{ID: "adb-source-task", ProjectID: project.ID, WorkspaceID: "source-workspace", Title: "Remote ADB", OriginalRequest: "debug", Status: domain.TaskWaitingUser, CollaborationMode: "single", MaxAgents: 1, KnowledgePolicy: "inherit", OwnerDeviceID: "device-remote", ReadOnly: true, CreatedAt: now, UpdatedAt: now}
	taskPayload, _ := json.Marshal(remoteTask)
	objects := []store.RemoteTaskObject{{OwnerDeviceID: "device-remote", ObjectType: "task", ObjectID: remoteTask.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: taskPayload, CreatedAt: now, UpdatedAt: now}}
	for _, group := range []domain.HardwareGroup{
		{ID: "usb", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Protocol: "adb", SSHAuth: "auto", ADBConnection: "usb", ADBSerial: "REMOTE-SER"}},
		{ID: "tv", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "10.0.0.9", Port: 5601, Protocol: "adb", SSHAuth: "auto", ADBConnection: "tcp"}},
		// An older peer never sent adb_connection: it stays wireless.
		{ID: "phone", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "10.0.0.10", Port: 41235, Protocol: "adb", SSHAuth: "auto"}},
	} {
		group.TaskID, group.Access, group.CreatedAt, group.UpdatedAt = remoteTask.ID, domain.HardwareAccessReadOnly, now, now
		payload, _ := json.Marshal(group)
		objects = append(objects, store.RemoteTaskObject{OwnerDeviceID: "device-remote", ObjectType: "hardware", ObjectID: "device-remote:" + remoteTask.ID + ":" + group.ID, TaskID: remoteTask.ID, ProjectID: project.ID, Payload: payload, CreatedAt: now, UpdatedAt: now})
	}
	for _, item := range objects {
		if err := database.UpsertRemoteTaskObject(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	mirrors, err := database.RemoteTaskMirrors(ctx, project.ID)
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("mirrors=%#v err=%v", mirrors, err)
	}
	server := httptest.NewServer(New(Config{Store: database, Auth: authServiceForTest(database), App: app.NewService(database, nil, app.StubExecutor{}), Secrets: &fakeSecretStore{}}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	takeover := func(usbNetwork map[string]any) *http.Response {
		return requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+mirrors[0].Task.ID+"/takeover", map[string]any{
			"workspace_id": local.ID, "model_id": model.ID, "collaboration_mode": "single",
			"groups": []map[string]any{
				{"id": "usb", "mode": "network", "network": usbNetwork},
				{"id": "tv", "mode": "network", "network": map[string]any{"host": "10.0.0.19", "protocol": "adb"}},
				{"id": "phone", "mode": "network", "network": map[string]any{"host": "10.0.0.20", "protocol": "adb"}},
			},
		}, csrf)
	}
	response := takeover(map[string]any{"protocol": "adb"})
	var failed map[string]any
	decodeResponse(t, response, &failed)
	if response.StatusCode != http.StatusBadRequest || failed["error"] != "local_hardware_fields_required" || !strings.Contains(failed["message"].(string), "USB 设备序列号") {
		t.Fatalf("takeover without a local USB serial status=%d payload=%#v", response.StatusCode, failed)
	}
	response = takeover(map[string]any{"protocol": "adb", "adb_serial": "192.0.2.4:5555"})
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("takeover with a network-style USB serial status=%d", response.StatusCode)
	}
	response = takeover(map[string]any{"protocol": "adb", "adb_serial": "LOCAL-SER"})
	var created struct {
		Task     domain.Task            `json:"task"`
		Hardware []domain.HardwareGroup `json:"hardware"`
	}
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("takeover status=%d payload=%#v", response.StatusCode, created)
	}
	groups, err := database.HardwareGroups(ctx, created.Task.ID)
	if err != nil || len(groups) != 3 {
		t.Fatalf("local groups=%#v err=%v", groups, err)
	}
	byID := map[string]domain.HardwareGroup{}
	for _, group := range groups {
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

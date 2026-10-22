package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"github.com/ChinaKai/AHA2/internal/store"
	"golang.org/x/crypto/ssh"
)

func TestHardwareAPIConfigNetworkTerminalAndPermissions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	secretsStore := &fakeSecretStore{}
	hardwareManager := hardware.NewManager(database)
	defer hardwareManager.Close()
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, nil, app.StubExecutor{})
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: appService,
		Secrets: secretsStore, Hardware: hardwareManager,
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}

	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/hardware", map[string]any{
		"groups": []map[string]any{{
			"id": "board", "description": "Main board", "mode": "network",
			"network":  map[string]any{"host": host, "port": port, "protocol": "raw"},
			"username": "root", "password": "secret-password",
		}},
	}, csrf)
	var configured map[string]any
	decodeResponse(t, response, &configured)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("configure hardware: %d %#v", response.StatusCode, configured)
	}
	group := configured["groups"].([]any)[0].(map[string]any)
	if group["password_configured"] != true {
		t.Fatalf("password state missing: %#v", group)
	}
	if group["access"] != domain.HardwareAccessReadWrite {
		t.Fatalf("default hardware access = %#v", group["access"])
	}
	if _, exposed := group["credential_ref"]; exposed {
		t.Fatalf("credential reference exposed: %#v", group)
	}
	if secretsStore.values["hardware/"+task.ID+"/board/credential"] != "secret-password" {
		t.Fatalf("hardware password not stored out of band: %#v", secretsStore.values)
	}

	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()
	response = requestJSON(t, client, http.MethodPost,
		server.URL+"/api/v1/tasks/"+task.ID+"/hardware/board/connect?transport=network", nil, csrf)
	var connected map[string]any
	decodeResponse(t, response, &connected)
	if response.StatusCode != http.StatusOK || connected["status"].(map[string]any)["connected"] != true {
		t.Fatalf("connect hardware: %d %#v", response.StatusCode, connected)
	}
	connection := <-accepted
	defer connection.Close()
	go func() {
		buffer := make([]byte, 32)
		count, _ := connection.Read(buffer)
		if string(buffer[:count]) == "ping\r\n" {
			_, _ = connection.Write([]byte("pong\r\n"))
		}
	}()
	response = requestJSON(t, client, http.MethodPost,
		server.URL+"/api/v1/tasks/"+task.ID+"/hardware/board/send?transport=network",
		map[string]any{"data": "ping\r\n", "encoding": "text"}, csrf)
	if response.StatusCode != http.StatusAccepted {
		var failure map[string]any
		decodeResponse(t, response, &failure)
		t.Fatalf("send hardware: %d %#v", response.StatusCode, failure)
	}
	response.Body.Close()

	var terminal map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response = requestJSON(t, client, http.MethodGet,
			server.URL+"/api/v1/tasks/"+task.ID+"/hardware/board/terminal?transport=network", nil, "")
		decodeResponse(t, response, &terminal)
		items := terminal["stream"].(map[string]any)["items"].([]any)
		found := false
		for _, value := range items {
			if strings.Contains(value.(map[string]any)["data"].(string), "pong") {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if terminal == nil {
		t.Fatal("terminal response missing")
	}
	foundPong := false
	for _, value := range terminal["stream"].(map[string]any)["items"].([]any) {
		if strings.Contains(value.(map[string]any)["data"].(string), "pong") {
			foundPong = true
		}
	}
	if !foundPong {
		t.Fatalf("network output missing: %#v", terminal)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/hardware", map[string]any{
		"groups": []map[string]any{{
			"id": "board", "description": "Main board", "mode": "network", "access": "read_only",
			"network": map[string]any{"host": host, "port": port, "protocol": "raw"},
		}},
	}, csrf)
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost,
		server.URL+"/api/v1/tasks/"+task.ID+"/hardware/board/send?transport=network",
		map[string]any{"data": "blocked"}, csrf)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected read-only rejection, got %d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/hardware", map[string]any{
		"groups": []map[string]any{{
			"id": "ssh-board", "mode": "network", "access": "read_only", "username": "root",
			"network": map[string]any{"host": "192.0.2.20", "port": 0, "protocol": "ssh"},
		}},
	}, csrf)
	var sshConfigured map[string]any
	decodeResponse(t, response, &sshConfigured)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("configure SSH hardware: %d %#v", response.StatusCode, sshConfigured)
	}
	sshGroup := sshConfigured["groups"].([]any)[0].(map[string]any)
	if sshGroup["network"].(map[string]any)["port"] != float64(22) {
		t.Fatalf("SSH default port missing: %#v", sshGroup)
	}
	if sshGroup["network"].(map[string]any)["ssh_auth"] != domain.HardwareSSHAuthAuto {
		t.Fatalf("SSH default auth mode missing: %#v", sshGroup)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/hardware", map[string]any{
		"groups": []map[string]any{{
			"id": "android", "mode": "network", "access": "read_only",
			"network":  map[string]any{"host": "192.0.2.30", "port": 0, "protocol": "adb"},
			"username": "ignored", "password": "ignored-secret",
		}},
	}, csrf)
	var adbConfigured map[string]any
	decodeResponse(t, response, &adbConfigured)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("configure ADB hardware: %d %#v", response.StatusCode, adbConfigured)
	}
	adbGroup := adbConfigured["groups"].([]any)[0].(map[string]any)
	if adbGroup["network"].(map[string]any)["protocol"] != domain.HardwareProtocolADB ||
		adbGroup["network"].(map[string]any)["port"] != float64(5555) {
		t.Fatalf("ADB default port missing: %#v", adbGroup)
	}
	if adbGroup["password_configured"] != false || adbGroup["username"] != nil {
		t.Fatalf("ADB accepted unrelated login credentials: %#v", adbGroup)
	}
	if _, stored := secretsStore.values["hardware/"+task.ID+"/android/credential"]; stored {
		t.Fatal("ADB credential was stored even though ADB uses device authorization")
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/hardware/serial-ports", nil, "")
	var ports map[string]any
	decodeResponse(t, response, &ports)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("serial ports failed: %d %#v", response.StatusCode, ports)
	}
}

func TestHardwareResourcesAreSystemLevelAndBindToTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	firstTask := createHardwareAPITask(t, database)
	secondTask := createHardwareAPITask(t, database)
	secretsStore := &fakeSecretStore{}
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}),
		Secrets: secretsStore, Hardware: hardware.NewManager(database),
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware-resources", map[string]any{
		"description": "Shared board", "mode": "network", "access": "read_write",
		"network":  map[string]any{"host": "192.0.2.50", "port": 23, "protocol": "raw"},
		"username": "root", "password": "board-secret",
	}, csrf)
	var created struct {
		Resource domain.HardwareResource `json:"resource"`
	}
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.Resource.ID == "" || !created.Resource.PasswordConfigured {
		t.Fatalf("create resource status=%d payload=%#v", response.StatusCode, created)
	}
	if secretsStore.values["hardware-resource/"+created.Resource.ID+"/credential"] != "board-secret" {
		t.Fatal("system hardware password was not stored out of band")
	}

	for index, task := range []domain.Task{firstTask, secondTask} {
		response = requestJSON(t, client, http.MethodPut,
			server.URL+"/api/v1/tasks/"+task.ID+"/hardware-bindings",
			map[string]any{"bindings": []map[string]any{{"resource_id": created.Resource.ID, "access": domain.HardwareAccessReadOnly}}}, csrf)
		var bound struct {
			Groups []domain.HardwareGroup `json:"groups"`
		}
		decodeResponse(t, response, &bound)
		if response.StatusCode != http.StatusOK || len(bound.Groups) != 1 || bound.Groups[0].ResourceID != created.Resource.ID || bound.Groups[0].Access != created.Resource.Access {
			t.Fatalf("bind task %d status=%d payload=%#v", index, response.StatusCode, bound)
		}
	}

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/hardware-resources", nil, "")
	var listed struct {
		Resources []domain.HardwareResource `json:"resources"`
	}
	decodeResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || len(listed.Resources) != 1 || listed.Resources[0].BindingCount != 2 {
		t.Fatalf("list resources status=%d payload=%#v", response.StatusCode, listed)
	}

	response = requestJSON(t, client, http.MethodDelete, server.URL+"/api/v1/hardware-resources/"+created.Resource.ID, nil, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete resource status=%d", response.StatusCode)
	}
	response.Body.Close()
	if _, ok := secretsStore.values["hardware-resource/"+created.Resource.ID+"/credential"]; ok {
		t.Fatal("deleted hardware resource left its credential behind")
	}
	for _, task := range []domain.Task{firstTask, secondTask} {
		groups, err := database.HardwareGroups(ctx, task.ID)
		if err != nil || len(groups) != 0 {
			t.Fatalf("task %s retained deleted resource: %#v err=%v", task.ID, groups, err)
		}
	}
}

func TestHardwareADBPairUsesSeparatePortAndDoesNotPersistCode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	authService := auth.NewService(database, "setup-test", time.Hour)
	pairedEndpoint, pairedCode := "", ""
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}),
		PairADB: func(_ context.Context, endpoint, code string) (string, error) {
			pairedEndpoint, pairedCode = endpoint, code
			return "Successfully paired", nil
		},
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/hardware-resources", map[string]any{
		"description": "Android phone", "mode": "network", "access": "read_write",
		"network": map[string]any{
			"host": "192.0.2.82", "port": 5555, "protocol": "adb", "ssh_auth": "auto",
		},
	}, csrf)
	var created struct {
		Resource domain.HardwareResource `json:"resource"`
	}
	decodeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.Resource.ADBPairedAt != "" {
		t.Fatalf("create ADB resource status=%d payload=%#v", response.StatusCode, created)
	}
	response = requestJSON(t, client, http.MethodPost,
		server.URL+"/api/v1/hardware-resources/"+created.Resource.ID+"/adb/pair",
		map[string]any{"pairing_port": 37124, "pairing_code": "123456"}, csrf)
	var paired map[string]any
	decodeResponse(t, response, &paired)
	if response.StatusCode != http.StatusOK || paired["message"] != "Android 设备配对成功" {
		t.Fatalf("pair ADB status=%d payload=%#v", response.StatusCode, paired)
	}
	if pairedEndpoint != "192.0.2.82:37124" || pairedCode != "123456" {
		t.Fatalf("pair endpoint=%q code=%q", pairedEndpoint, pairedCode)
	}
	stored, err := database.HardwareResource(ctx, created.Resource.ID)
	if err != nil || stored.Network.Port != 5555 || stored.ADBPairedAt == "" {
		t.Fatalf("stored ADB resource=%#v err=%v", stored, err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var pairingSecretsPersisted bool
	if err := raw.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM audit_events
		WHERE resource_id=? AND (data_json LIKE '%123456%' OR data_json LIKE '%37124%'))`, created.Resource.ID).Scan(&pairingSecretsPersisted); err != nil {
		t.Fatal(err)
	}
	if pairingSecretsPersisted {
		t.Fatal("ADB pairing port or code was persisted in audit data")
	}
}

func TestHardwareConnectFailureIsAudited(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "aha2.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	hardwareManager := hardware.NewManager(database)
	defer hardwareManager.Close()
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, nil, app.StubExecutor{})
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: appService,
		Secrets: &fakeSecretStore{}, Hardware: hardwareManager,
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := closedListener.Addr().(*net.TCPAddr).Port
	closedListener.Close()
	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/tasks/"+task.ID+"/hardware", map[string]any{
		"groups": []map[string]any{{
			"id": "closed", "mode": "network", "access": "read_only",
			"network": map[string]any{"host": "127.0.0.1", "port": closedPort, "protocol": "raw"},
		}},
	}, csrf)
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost,
		server.URL+"/api/v1/tasks/"+task.ID+"/hardware/closed/connect?transport=network", nil, csrf)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expected connection failure, got %d", response.StatusCode)
	}
	response.Body.Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var count int
	if err := raw.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM audit_events
		WHERE resource_id=? AND action='task.hardware.connect_failed'`, task.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("connect failure audit count=%d", count)
	}
}

func TestHardwareSSHHostKeyRequiresExplicitFingerprintConfirmation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createHardwareAPITask(t, database)
	now := time.Now().UTC()
	group := domain.HardwareGroup{TaskID: task.ID, ID: "ssh", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.40", Port: 22, Protocol: domain.HardwareProtocolSSH, SSHAuth: domain.HardwareSSHAuthPassword}, Username: "owner", PasswordConfigured: true, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}
	if err := database.ReplaceHardwareGroups(ctx, task.ID, []domain.HardwareGroup{group}); err != nil {
		t.Fatal(err)
	}
	info := hardware.SSHHostKeyInfo{Endpoint: "192.0.2.40:22", Algorithm: ssh.KeyAlgoED25519, Fingerprint: "SHA256:test-fingerprint"}
	trusted := ""
	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{
		Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{}),
		ProbeSSHHostKey: func(context.Context, string) (hardware.SSHHostKeyInfo, error) { return info, nil },
		TrustSSHHostKey: func(_ context.Context, _ string, fingerprint string) (hardware.SSHHostKeyInfo, error) {
			if fingerprint != info.Fingerprint {
				return hardware.SSHHostKeyInfo{}, fmt.Errorf("fingerprint changed")
			}
			trusted = fingerprint
			return info, nil
		},
	}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+task.ID+"/hardware/ssh/host-key?transport=network", nil, "")
	var probe map[string]any
	decodeResponse(t, response, &probe)
	if response.StatusCode != http.StatusOK || probe["host_key"].(map[string]any)["fingerprint"] != info.Fingerprint {
		t.Fatalf("probe status=%d payload=%#v", response.StatusCode, probe)
	}
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/hardware/ssh/host-key/trust?transport=network", map[string]any{"fingerprint": "SHA256:wrong"}, csrf)
	if response.StatusCode != http.StatusConflict || trusted != "" {
		t.Fatalf("wrong fingerprint status=%d trusted=%q", response.StatusCode, trusted)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/tasks/"+task.ID+"/hardware/ssh/host-key/trust?transport=network", map[string]any{"fingerprint": info.Fingerprint}, csrf)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || trusted != info.Fingerprint {
		t.Fatalf("trust status=%d trusted=%q", response.StatusCode, trusted)
	}
}

func createHardwareAPITask(t *testing.T, database *store.Store) domain.Task {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	project := domain.Project{ID: domain.NewID("project"), Name: "Hardware", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: domain.NewID("workspace"), ProjectID: project.ID, Name: "Local", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	env := domain.EnvGroup{
		ID: domain.NewID("env"), Name: "Env", ProviderID: "stub", Backend: "stub", Revision: 1,
		Environment: map[string]string{}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: domain.NewID("model"), DisplayName: "Stub", ProviderID: "stub", Backend: "stub",
		WireModel: "stub", DefaultEnvGroupID: env.ID, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertEnvGroup(ctx, env) },
		func() error { return database.UpsertModel(ctx, model) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := app.NewService(database, nil, app.StubExecutor{})
	task, err := service.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Hardware",
		Request: "debug board", ModelID: model.ID, CollaborationMode: "single", MaxAgents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

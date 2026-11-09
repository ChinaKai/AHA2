package httpapi

import (
	"context"
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
	"github.com/ChinaKai/AHA2/internal/secrets"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestOwnerPetStateAPIAndVersionConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet", nil, "")
	var current struct {
		State struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		} `json:"state"`
	}
	decodeResponse(t, response, &current)
	if response.StatusCode != http.StatusOK || current.State.ID != "owner_pet" || current.State.Version != 1 {
		t.Fatalf("pet state=%d %#v", response.StatusCode, current)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet", map[string]any{
		"action": "jump", "mood": "happy", "x": 0.4, "y": 0.5, "version": current.State.Version,
	}, csrf)
	var updated struct {
		State struct {
			Action  string  `json:"action"`
			Version int64   `json:"version"`
			X       float64 `json:"x"`
		} `json:"state"`
	}
	decodeResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated.State.Action != "jump" || updated.State.Version != 2 || updated.State.X != 0.4 {
		t.Fatalf("update pet state=%d %#v", response.StatusCode, updated)
	}

	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet", map[string]any{
		"action": "walk", "version": 1,
	}, csrf)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale pet update=%d", response.StatusCode)
	}
	response.Body.Close()
}

func TestPetApprovalCancelRequiresOwnerSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	approval, err := appService.CreatePetApproval(ctx, app.PetApprovalInput{
		Kind: "owner_decision", Title: "取消权限测试", Summary: "只有主人可以取消这条审批", Risk: "medium",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	resolveURL := server.URL + "/api/v1/pet/approvals/" + approval.ID + "/resolve"

	anonymous := &http.Client{}
	response := requestJSON(t, anonymous, http.MethodPost, resolveURL, map[string]any{"decision": "cancel"}, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous cancel=%d", response.StatusCode)
	}
	response.Body.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	response = requestJSON(t, client, http.MethodPost, resolveURL, map[string]any{"decision": "cancel"}, csrf)
	var payload struct {
		Approval struct {
			Status string `json:"status"`
		} `json:"approval"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.Approval.Status != "cancelled" {
		t.Fatalf("owner cancel=%d %#v", response.StatusCode, payload)
	}
}

func TestOwnerPetSettingsAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet/settings", map[string]any{
		"enabled": false, "character": "deepseek_fish", "actions": []string{"walk", "jump"},
		"identity": "should be ignored", "principles": "should be ignored",
		"idle_minutes": 10, "hourly_limit": 4, "quiet_start": 22, "quiet_end": 7,
		"allow_view_tasks": false, "allow_create_tasks": true,
		"allow_create_workspaces": true, "allow_message_tasks": true, "allow_manage_models": true, "allow_manage_knowledge": true, "allow_manage_channels": true, "allow_manage_system": true,
	}, csrf)
	var payload struct {
		Assistant struct {
			PetEnabled            bool     `json:"pet_enabled"`
			Character             string   `json:"character"`
			PetActions            []string `json:"pet_actions"`
			BrainModelID          string   `json:"brain_model_id"`
			PetIdentity           string   `json:"pet_identity"`
			IdleMinutes           int      `json:"pet_idle_minutes"`
			HourlyLimit           int      `json:"pet_hourly_limit"`
			AllowViewTasks        bool     `json:"pet_allow_view_tasks"`
			AllowCreateTasks      bool     `json:"pet_allow_create_tasks"`
			AllowCreateWorkspaces bool     `json:"pet_allow_create_workspaces"`
			AllowMessageTasks     bool     `json:"pet_allow_message_tasks"`
			AllowManageModels     bool     `json:"pet_allow_manage_models"`
			AllowManageKnowledge  bool     `json:"pet_allow_manage_knowledge"`
			AllowManageChannels   bool     `json:"pet_allow_manage_channels"`
			AllowManageSystem     bool     `json:"pet_allow_manage_system"`
		} `json:"assistant"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.Assistant.PetEnabled || payload.Assistant.Character != "deepseek_fish" || len(payload.Assistant.PetActions) != 2 || payload.Assistant.PetIdentity != "" || payload.Assistant.IdleMinutes != 10 || payload.Assistant.HourlyLimit != 4 ||
		payload.Assistant.AllowViewTasks || !payload.Assistant.AllowCreateTasks || !payload.Assistant.AllowCreateWorkspaces || !payload.Assistant.AllowMessageTasks || !payload.Assistant.AllowManageModels || !payload.Assistant.AllowManageKnowledge || !payload.Assistant.AllowManageChannels || !payload.Assistant.AllowManageSystem {
		t.Fatalf("pet settings=%d %#v", response.StatusCode, payload)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet/workbench/core/identity.md", map[string]any{
		"content": "# 身份\n\n- 名字：API 宠物\n",
	}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save pet document=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet/workbench/worklog/2026-11-09.md", map[string]any{"content": "preserved worklog"}, csrf)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save worklog=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench", nil, "")
	var workbench struct {
		Core []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"core"`
		Worklog []struct {
			Date    string `json:"date"`
			Content string `json:"content"`
		} `json:"worklog"`
	}
	decodeResponse(t, response, &workbench)
	if response.StatusCode != http.StatusOK || len(workbench.Core) == 0 || !strings.Contains(workbench.Core[0].Content, "API 宠物") {
		t.Fatalf("workbench=%d %#v", response.StatusCode, workbench)
	}
	if len(workbench.Worklog) != 1 || workbench.Worklog[0].Date != "2026-11-09" || workbench.Worklog[0].Content != "preserved worklog" {
		t.Fatalf("worklog not preserved: %#v", workbench.Worklog)
	}
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet/workbench/worklog/bad%20name.md", map[string]any{"content": "x"}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid pet document path=%d", response.StatusCode)
	}
}

func TestOwnerPetControlAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/control", map[string]any{
		"say": "去左边看看", "actions": []map[string]any{{"type": "move_to", "zone": "left"}, {"type": "mood", "name": "happy"}},
	}, csrf)
	var payload struct {
		OK      bool   `json:"ok"`
		Say     string `json:"say"`
		Actions []struct {
			Type string `json:"type"`
		} `json:"actions"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload.Say != "去左边看看" || len(payload.Actions) != 2 {
		t.Fatalf("pet control=%d %#v", response.StatusCode, payload)
	}

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/control", map[string]any{
		"say": "", "actions": []map[string]any{{"type": "play", "name": "explode"}},
	}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid pet control=%d", response.StatusCode)
	}
	response.Body.Close()
}

func TestPetResetRequiresTypedConfirmation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(database, "setup-test", time.Hour)
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: appService}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/reset", map[string]any{
		"confirm": "重置",
	}, csrf)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong confirmation=%d", response.StatusCode)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/reset", map[string]any{
		"confirm": "重置宠物",
	}, csrf)
	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			Tasks      int `json:"tasks"`
			Workspaces int `json:"workspaces"`
		} `json:"result"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || !payload.OK || payload.Result.Tasks != 0 || payload.Result.Workspaces != 0 {
		t.Fatalf("pet reset=%d %#v", response.StatusCode, payload)
	}
	response.Body.Close()

	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/assistant", nil, "")
	var view struct {
		Assistant struct {
			PetEnabled bool   `json:"pet_enabled"`
			PetTaskID  string `json:"pet_task_id"`
		} `json:"assistant"`
	}
	decodeResponse(t, response, &view)
	if response.StatusCode != http.StatusOK || view.Assistant.PetEnabled || view.Assistant.PetTaskID != "" {
		t.Fatalf("assistant after reset=%d %#v", response.StatusCode, view)
	}
	response.Body.Close()
}

func TestPetPendingApprovalCountAndRetiredRoutes(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	secretStore, err := secrets.Open(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	appService := app.NewService(database, secretStore, app.StubExecutor{})
	defer appService.Shutdown(ctx)
	server := httptest.NewServer(New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), App: appService}).Handler())
	defer server.Close()
	endpoint := server.URL + "/api/v1/pet/approvals/pending-count"
	response := requestJSON(t, &http.Client{}, http.MethodGet, endpoint, nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous count=%d", response.StatusCode)
	}
	response.Body.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	assertCount := func(want int) {
		t.Helper()
		response := requestJSON(t, client, http.MethodGet, endpoint, nil, "")
		var payload struct {
			OK      bool `json:"ok"`
			Pending int  `json:"pending"`
		}
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusOK || !payload.OK || payload.Pending != want {
			t.Fatalf("count=%d %#v, want %d", response.StatusCode, payload, want)
		}
	}
	assertCount(0)
	for _, status := range []string{"pending", "approved", "rejected", "cancelled"} {
		if err := database.CreatePetApproval(ctx, domain.PetApproval{ID: status, Kind: "owner_decision", Title: status, Status: status, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(1)
	for _, decision := range []string{"approve", "reject", "cancel"} {
		approval, err := appService.CreatePetApproval(ctx, app.PetApprovalInput{Kind: "owner_decision", Title: decision, Summary: decision, Risk: "low"})
		if err != nil {
			t.Fatal(err)
		}
		assertCount(2)
		response = requestJSON(t, client, http.MethodPost, server.URL+"/api/v1/pet/approvals/"+approval.ID+"/resolve", map[string]any{"decision": decision, "decision_reason": "test reason"}, csrf)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("resolve %s=%d", decision, response.StatusCode)
		}
		response.Body.Close()
		assertCount(1)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/workbench", nil, "")
	var workbench map[string]any
	decodeResponse(t, response, &workbench)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("workbench=%d %#v", response.StatusCode, workbench)
	}
	for key := range workbench {
		if strings.Contains(strings.ToLower(key), "diary") {
			t.Fatalf("retired field %q remains", key)
		}
	}
	if _, ok := workbench["core"]; !ok {
		t.Fatal("missing core")
	}
	if _, ok := workbench["worklog"]; !ok {
		t.Fatal("missing worklog")
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/pet/diary", nil, "")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("retired read route=%d", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, client, http.MethodPut, server.URL+"/api/v1/pet/workbench/diary/2026-11-09.md", map[string]any{"content": "retired"}, csrf)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("retired write route=%d", response.StatusCode)
	}
	response.Body.Close()
}

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type petMCPAgentCallResult struct {
	Status  int
	IsError bool
	Error   string
	Text    string
}

func callPetMCPTool(t *testing.T, server *Server, token, name, args string) petMCPAgentCallResult {
	t.Helper()
	payload := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/pet/mcp", strings.NewReader(payload))
	request.RemoteAddr = "127.0.0.1:12345"
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	var body struct {
		Error  string `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode pet mcp response: %v (%s)", err, recorder.Body.String())
	}
	text := ""
	if len(body.Result.Content) > 0 {
		text = body.Result.Content[0].Text
	}
	return petMCPAgentCallResult{
		Status: recorder.Code, IsError: body.Result.IsError, Error: body.Error, Text: text,
	}
}

func TestPetMCPUpdateTaskAgentSwitchesModelSource(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	project := domain.Project{ID: "project-pet-source", Name: "Pet source", ProjectType: "folder", KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now}
	workspace := domain.Workspace{
		ID: "workspace-pet-source", ProjectID: project.ID, Name: "Pet source", Locality: "local",
		Transport: "native", RootPath: t.TempDir(), SSHPort: 22, Health: "ready", CreatedAt: now, UpdatedAt: now,
	}
	provider := domain.Provider{
		ID: "provider-pet-source", Name: "Gateway", BaseURL: "https://gateway.test/v1",
		AuthStyle: "bearer", CredentialRef: "provider/pet-source", CredentialConfigured: true, CreatedAt: now, UpdatedAt: now,
	}
	envGroup := domain.EnvGroup{
		ID: "env-pet-source", Name: "Gateway / gpt-env", ProviderID: provider.ID, Backend: "codex", Revision: 1,
		Environment: map[string]string{"OPENAI_MODEL": "gpt-env"}, SecretRefs: map[string]string{}, CreatedAt: now, UpdatedAt: now,
	}
	model := domain.Model{
		ID: "model-pet-source", DisplayName: "Env", ProviderID: provider.ID, Source: domain.ModelSourceProvider,
		Backend: "codex", WireModel: "gpt-env", WireAPI: "responses", DefaultEnvGroupID: envGroup.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	account := domain.CodexAccount{
		ID: "account-pet-source", Label: "Official", Status: "ready", CredentialRef: "codex/pet-source",
		CredentialConfigured: true, AvailableModels: []domain.CodexModelOption{{
			WireModel: "gpt-official", DisplayName: "GPT Official",
		}}, CreatedAt: now, UpdatedAt: now,
	}
	for _, operation := range []func() error{
		func() error { return database.CreateProject(ctx, project) },
		func() error { return database.CreateWorkspace(ctx, workspace) },
		func() error { return database.UpsertProvider(ctx, provider) },
		func() error { return database.UpsertEnvGroup(ctx, envGroup) },
		func() error { return database.UpsertModel(ctx, model) },
		func() error { return database.UpsertCodexAccount(ctx, account) },
	} {
		if err := operation(); err != nil {
			t.Fatal(err)
		}
	}
	service := app.NewService(database, nil, nil)
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	enablePet := func(allowCreateTasks bool) {
		t.Helper()
		allow := allowCreateTasks
		if _, err := service.UpdateOwnerPetSettings(ctx, app.PetSettingsInput{
			Enabled: true, ChatModelID: model.ID, Actions: []string{"walk"},
			IdleMinutes: 5, QuietStart: 22, QuietEnd: 8, HourlyLimit: 2,
			AllowViewTasks: &allow, AllowCreateTasks: &allow,
		}); err != nil {
			t.Fatal(err)
		}
	}
	enablePet(true)
	task, err := service.CreateTask(ctx, app.CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Pet source task", Request: "test",
		Isolation: "inplace", Backend: "codex", ModelSource: domain.ModelSourceProvider, ModelID: model.ID,
		ReasoningEffort: "high", Filesystem: "workspace-write", Approval: "never",
		CollaborationMode: "single", MaxAgents: 1, StartMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := agentapi.NewCapabilities()
	petToken, err := capabilities.Issue("task_pet", "main", "turn_pet", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := capabilities.Issue("task_other", "main", "turn_other", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	targetToken, err := capabilities.Issue(task.ID, "main", "turn_target", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	server := New(Config{Store: database, App: service, AgentCapabilities: capabilities})
	petCall := func(name, args string) petMCPAgentCallResult {
		return callPetMCPTool(t, server, petToken, name, args)
	}

	call := petCall("aha_update_task_agent",
		`{"task_id":"`+task.ID+`","model_source":"official","wire_model":"gpt-official","codex_account_id":"`+account.ID+`"}`)
	if call.IsError || !strings.Contains(call.Text, `"ok":true`) || !strings.Contains(call.Text, `"status":"updated"`) {
		t.Fatalf("official switch through MCP failed: %#v", call)
	}
	agents, err := service.TaskAgents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) == 0 || agents[0].ModelSource != domain.ModelSourceOfficial || agents[0].CodexAccountID != account.ID {
		t.Fatalf("MCP did not persist the official source: %#v", agents)
	}
	officialModelID := agents[0].ModelID

	call = petCall("aha_update_task_agent",
		`{"task_id":"`+task.ID+`","model_source":"provider","model_id":"`+model.ID+`"}`)
	if call.IsError || !strings.Contains(call.Text, `"ok":true`) {
		t.Fatalf("provider switch through MCP failed: %#v", call)
	}
	agents, err = service.TaskAgents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) == 0 || agents[0].ModelSource != domain.ModelSourceProvider || agents[0].ModelID != model.ID {
		t.Fatalf("MCP did not persist the provider source: %#v", agents)
	}

	// A managed model selected as provider must surface a stable rejection code
	// rather than a 200 no-op.
	call = petCall("aha_update_task_agent",
		`{"task_id":"`+task.ID+`","model_source":"provider","model_id":"`+officialModelID+`"}`)
	if !call.IsError || !strings.Contains(call.Text, app.ErrCodeModelSourceMismatch) {
		t.Fatalf("invalid combination did not return %s: %#v", app.ErrCodeModelSourceMismatch, call)
	}

	// Mutating agent configuration needs the pet Task's own capability. The
	// loopback endpoint alone and another Task's valid token must both be
	// rejected before any read or write happens.
	agents, err = service.TaskAgents(ctx, task.ID)
	if err != nil || len(agents) == 0 {
		t.Fatalf("agents=%#v err=%v", agents, err)
	}
	beforeIdentity := agents[0]
	identityAuditCount := func() (cards, events int) {
		t.Helper()
		page, err := database.ConversationPage(ctx, task.ID, 0, 0, 100, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.Kind == "agent_config_updated" {
				cards++
			}
		}
		petEvents, err := database.ListPetEvents(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range petEvents {
			if event.Kind == "task_agent_config_updated" {
				events++
			}
		}
		return cards, events
	}
	cardsBefore, eventsBefore := identityAuditCount()
	escalate := `{"task_id":"` + task.ID + `","model_source":"official","wire_model":"gpt-official","codex_account_id":"` + account.ID + `"}`
	for _, attempt := range []struct {
		name  string
		token string
	}{
		{"no token", ""},
		{"foreign task token", otherToken},
		{"target task's own token", targetToken},
	} {
		call = callPetMCPTool(t, server, attempt.token, "aha_update_task_agent", escalate)
		if call.Status != http.StatusOK || !call.IsError || !strings.Contains(call.Text, app.ErrCodePetIdentityRequired) {
			t.Fatalf("%s was not rejected by identity: %#v", attempt.name, call)
		}
	}
	call = callPetMCPTool(t, server, "not-a-real-token", "aha_update_task_agent", escalate)
	if call.Status != http.StatusUnauthorized || call.Error != "agent_capability_invalid" {
		t.Fatalf("invalid token was not rejected at the endpoint: %#v", call)
	}
	agents, err = service.TaskAgents(ctx, task.ID)
	if err != nil || len(agents) == 0 {
		t.Fatalf("agents=%#v err=%v", agents, err)
	}
	if agents[0].ModelSource != domain.ModelSourceProvider || agents[0].ModelID != beforeIdentity.ModelID ||
		agents[0].RuntimeConfigSnapshotID != beforeIdentity.RuntimeConfigSnapshotID {
		t.Fatalf("rejected identity attempts changed the runtime: before=%#v after=%#v", beforeIdentity, agents[0])
	}
	cardsAfter, eventsAfter := identityAuditCount()
	if cardsAfter != cardsBefore || eventsAfter != eventsBefore {
		t.Fatalf("rejected identity attempts wrote audit success: cards %d→%d events %d→%d",
			cardsBefore, cardsAfter, eventsBefore, eventsAfter)
	}

	// Permission denial must also be explicit.
	enablePet(false)
	call = petCall("aha_update_task_agent",
		`{"task_id":"`+task.ID+`","model_source":"provider","model_id":"`+model.ID+`"}`)
	if !call.IsError || !strings.Contains(call.Text, app.ErrCodePetPermissionDenied) {
		t.Fatalf("permission denial did not return %s: %#v", app.ErrCodePetPermissionDenied, call)
	}

	// Audit: the conversation cards and pet events carry task, agent and
	// before/after source/model without any credential material.
	page, err := database.ConversationPage(ctx, task.ID, 0, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	sawOfficial := false
	sawProvider := false
	for _, item := range page.Items {
		if item.Kind != "agent_config_updated" {
			continue
		}
		if item.Payload["task_id"] != task.ID || item.Payload["agent_id"] != "main" {
			t.Fatalf("audit card is missing task/agent identity: %#v", item.Payload)
		}
		if item.Payload["previous_model_source"] == domain.ModelSourceProvider && item.Payload["model_source"] == domain.ModelSourceOfficial {
			sawOfficial = true
		}
		if item.Payload["previous_model_source"] == domain.ModelSourceOfficial && item.Payload["model_source"] == domain.ModelSourceProvider {
			sawProvider = true
		}
	}
	if !sawOfficial || !sawProvider {
		t.Fatalf("audit cards did not capture both source transitions: %#v", page.Items)
	}
	events, err := database.ListPetEvents(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	foundEvent := false
	for _, event := range events {
		if event.Kind != "task_agent_config_updated" {
			continue
		}
		foundEvent = true
		if strings.Contains(strings.ToLower(event.Detail), "api_key") || strings.Contains(strings.ToLower(event.Detail), "secret") {
			t.Fatalf("pet audit leaked credential-shaped fields: %s", event.Detail)
		}
	}
	if !foundEvent {
		t.Fatal("pet audit event missing")
	}
}

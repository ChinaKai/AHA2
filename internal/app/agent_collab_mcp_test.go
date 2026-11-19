package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/backend"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

type collabMCPFixture struct {
	ctx      context.Context
	database *store.Store
	service  *Service
	task     domain.Task
	round    domain.TaskRound
	turn     domain.Turn
}

// newCollabMCPFixture builds a Task in the requested collaboration mode with
// one running Main Turn. The Turn's inbox batch starts plain; channel fixtures
// add their own rows.
func newCollabMCPFixture(t *testing.T, mode string, maxAgents int) *collabMCPFixture {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	service := NewService(database, nil, StubExecutor{})
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })

	now := time.Now().UTC()
	project := domain.Project{ID: domain.NewID("project"), Name: "Collab domain", CreatedAt: now, UpdatedAt: now}
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
	task, err := service.CreateTask(ctx, CreateTaskInput{
		ProjectID: project.ID, WorkspaceID: workspace.ID, Title: "Collab domain",
		Request: "delegate work", ModelID: model.ID, CollaborationMode: mode, MaxAgents: maxAgents,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := domain.Message{
		ID: domain.NewID("message"), TaskID: task.ID, Role: "user", Sender: "owner",
		Content: "start collab turn", CreatedAt: now,
	}
	round := domain.TaskRound{
		ID: domain.NewID("round"), TaskID: task.ID, InputMessageID: message.ID,
		Status: domain.RoundRunning, CreatedAt: now, StartedAt: now,
	}
	turn := domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, RoundID: round.ID, AgentID: "main",
		InputMessageID: message.ID, Status: domain.TurnRunning,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
		InboxBatchID:            "batch-collab-" + task.ID, Attempt: 1, Required: true, QueuedAt: now,
	}
	createdRound, createdTurn, err := database.CreateMessageRoundAndTurn(ctx, message, round, turn)
	if err != nil {
		t.Fatal(err)
	}
	return &collabMCPFixture{ctx: ctx, database: database, service: service, task: task, round: createdRound, turn: createdTurn}
}

func (f *collabMCPFixture) claims(agentID string) agentapi.Claims {
	return agentapi.Claims{TaskID: f.task.ID, AgentID: agentID, TurnID: f.turn.ID}
}

func (f *collabMCPFixture) callContext() AgentCallContext {
	return AgentCallContext{Task: f.task, Turn: f.turn, Project: domain.Project{ID: f.task.ProjectID}}
}

func collabStringPtr(value string) *string { return &value }

func collabSubmitInput(assignment string) AgentCollabMCPSubmitInput {
	return AgentCollabMCPSubmitInput{
		Actions: []AgentCollabMCPAction{{
			AgentID: collabStringPtr("sub-001"), Title: collabStringPtr("Worker"),
			Assignment: collabStringPtr(assignment),
		}},
		IntegrationInstructions: collabStringPtr("integrate the result"),
	}
}

func collabInsertInbox(t *testing.T, database *store.Store, task domain.Task, roundID, batchID string, kinds ...string) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(database.DataDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, kind := range kinds {
		payload := `{}`
		sourceAgent, sourceKind := "owner", "owner_message"
		switch {
		case strings.HasPrefix(kind, "channel:"):
			mode := strings.TrimPrefix(kind, "channel:")
			encoded, err := json.Marshal(map[string]any{
				"channel_context": map[string]any{
					"schema":          "aha2.channel.v1",
					"instance_id":     "instance-collab",
					"provider":        "test",
					"endpoint":        domain.ChannelEndpointGroupDigitalHuman,
					"conversation_id": "conversation-collab",
					"route":           map[string]any{"mode": mode},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			payload = string(encoded)
			sourceAgent, sourceKind = "channel", "channel_message"
		case strings.HasPrefix(kind, "raw:"):
			payload = strings.TrimPrefix(kind, "raw:")
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := raw.ExecContext(context.Background(), `
			INSERT INTO agent_inbox(
				id,task_id,round_id,target_agent_id,source_agent_id,source_kind,source_turn_id,message_id,
				content,payload_json,status,batch_id,created_at,claimed_at,processed_at
			) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			domain.NewID("inbox"), task.ID, roundID, "main", sourceAgent, sourceKind,
			"", "", "hello", payload, "claimed", batchID, now, now, "",
		); err != nil {
			t.Fatal(err)
		}
	}
}

func collabCreateBatch(t *testing.T, database *store.Store, batchID, taskID, roundID, turnID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := database.CreateCollaborationBatch(context.Background(), domain.CollaborationBatch{
		ID: batchID, TaskID: taskID, RoundID: roundID, ParentTurnID: turnID,
		Status: domain.CollaborationBatchPending, CreatedAt: now, UpdatedAt: now,
	}, []domain.CollaborationMember{{
		BatchID: batchID, AgentID: "sub-001", Required: true,
		Status: domain.CollaborationMemberQueued, CreatedAt: now, UpdatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
}

// TestCollabMCPGateScopesMainAutoAndChannel proves the shared gate is the
// authority, not tool visibility: Main + auto mode + a plain Turn passes, and
// every other combination — including any channel context in the Turn's actual
// batch or an active task route — is denied.
func TestCollabMCPGateScopesMainAutoAndChannel(t *testing.T) {
	f := newCollabMCPFixture(t, "auto", 3)
	call := f.callContext()
	if !f.service.AgentCollabMCPAllowed(f.ctx, call) {
		t.Fatal("auto Main Turn was denied the collab domain")
	}

	subTurn := f.turn
	subTurn.AgentID = "sub-001"
	if f.service.AgentCollabMCPAllowed(f.ctx, AgentCallContext{Task: f.task, Turn: subTurn, Project: call.Project}) {
		t.Fatal("non-Main Turn passed the collab gate")
	}

	single := f.task
	single.CollaborationMode = "single"
	if f.service.AgentCollabMCPAllowed(f.ctx, AgentCallContext{Task: single, Turn: f.turn, Project: call.Project}) {
		t.Fatal("single-agent Task passed the collab gate")
	}
	if f.service.AgentCollabMCPAllowed(f.ctx, AgentCallContext{
		Task: domain.Task{ID: petTaskID, ProjectID: f.task.ProjectID, CollaborationMode: "auto"},
		Turn: f.turn, Project: call.Project,
	}) {
		t.Fatal("pet Task passed the collab gate")
	}

	orders := []struct {
		name  string
		kinds []string
	}{
		{name: "plain-first", kinds: []string{"plain", "channel:task_route"}},
		{name: "channel-first", kinds: []string{"channel:task_route", "plain"}},
	}
	for _, order := range orders {
		batchID := "batch-collab-gate-" + order.name
		collabInsertInbox(t, f.database, f.task, f.turn.RoundID, batchID, order.kinds...)
		turn := f.turn
		turn.InboxBatchID = batchID
		if f.service.AgentCollabMCPAllowed(f.ctx, AgentCallContext{Task: f.task, Turn: turn, Project: call.Project}) {
			t.Fatalf("%s mixed batch passed the collab gate", order.name)
		}
	}

	collabInsertInbox(t, f.database, f.task, f.turn.RoundID, "batch-collab-gate-malformed", "raw:not-json")
	malformedTurn := f.turn
	malformedTurn.InboxBatchID = "batch-collab-gate-malformed"
	if f.service.AgentCollabMCPAllowed(f.ctx, AgentCallContext{Task: f.task, Turn: malformedTurn, Project: call.Project}) {
		t.Fatal("malformed batch did not fail closed")
	}

	// An active task route denies the domain even when the Turn's own batch is
	// plain. Only the three rows hasActiveTaskRoute joins are needed.
	raw, err := sql.Open("sqlite", filepath.Join(f.database.DataDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(f.ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO channel_endpoints(id,instance_id,kind,enabled,config_json,created_at,updated_at) VALUES('collab-route-endpoint','collab-route-instance','group_digital_human',1,'{}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO channel_conversations(id,instance_id,endpoint_id,scope_key_version,scope_key,host_task_id,status,created_at,updated_at) VALUES('collab-route-conversation','collab-route-instance','collab-route-endpoint',1,'collab-route-scope','` + f.task.ID + `','active','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO channel_task_routes(id,instance_id,conversation_id,target_task_id,state,revision,activated_at,exited_at,exit_reason) VALUES('collab-route','collab-route-instance','collab-route-conversation','` + f.task.ID + `','active',1,'2026-01-01T00:00:00Z','','')`,
	} {
		if _, err := raw.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	collabInsertInbox(t, f.database, f.task, f.turn.RoundID, "batch-collab-gate-plain-route", "plain")
	plainTurn := f.turn
	plainTurn.InboxBatchID = "batch-collab-gate-plain-route"
	if f.service.AgentCollabMCPAllowed(f.ctx, AgentCallContext{Task: f.task, Turn: plainTurn, Project: call.Project}) {
		t.Fatal("active task route passed the collab gate")
	}
}

// TestCollabMCPGateFailsClosedOnStoreError closes the database so the shared
// channel determination cannot classify the batch; the gate must deny rather
// than assume a plain Turn.
func TestCollabMCPGateFailsClosedOnStoreError(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(database, nil, nil)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	call := AgentCallContext{
		Task:    domain.Task{ID: "task-store-error", ProjectID: "project-store-error", CollaborationMode: "auto"},
		Turn:    domain.Turn{ID: "turn-store-error", AgentID: "main", InboxBatchID: "batch-store-error"},
		Project: domain.Project{ID: "project-store-error"},
	}
	if service.AgentCollabMCPAllowed(ctx, call) {
		t.Fatal("store error did not fail closed")
	}
}

// TestCollabMCPRuntimeInjectionScopesTools proves the runtime injects the
// domain only for an auto-mode Main Turn with an Agent API URL, and advertises
// exactly the two Main-only tools.
func TestCollabMCPRuntimeInjectionScopesTools(t *testing.T) {
	f := newCollabMCPFixture(t, "auto", 3)
	single := f.task
	single.CollaborationMode = "single"
	scenarios := []struct {
		name    string
		task    domain.Task
		agentID string
		apiURL  string
		wantMCP bool
	}{
		{name: "auto main", task: f.task, agentID: "main", apiURL: "http://127.0.0.1:8766/", wantMCP: true},
		{name: "non-main", task: f.task, agentID: "sub-001", apiURL: "http://127.0.0.1:8766/"},
		{name: "single mode", task: single, agentID: "main", apiURL: "http://127.0.0.1:8766/"},
		{name: "pet task", task: domain.Task{ID: petTaskID, CollaborationMode: "auto"}, agentID: "main", apiURL: "http://127.0.0.1:8766/"},
		{name: "pet project", task: domain.Task{ID: "task-pet-project", ProjectID: petProjectID, CollaborationMode: "auto"}, agentID: "main", apiURL: "http://127.0.0.1:8766/"},
		{name: "no agent API", task: f.task, agentID: "main", apiURL: ""},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			servers, allowed := f.service.mcpRuntimeForTurn(f.ctx, scenario.task, scenario.agentID, scenario.apiURL, "")
			var collabServer bool
			for _, server := range servers {
				if server.Name == collabMCPName {
					collabServer = true
				}
			}
			if !scenario.wantMCP {
				if collabServer {
					t.Fatalf("unexpected collab server for %s", scenario.name)
				}
				for _, name := range allowed {
					if strings.HasPrefix(name, "mcp__"+collabMCPName+"__") {
						t.Fatalf("unexpected collab tool %q for %s", name, scenario.name)
					}
				}
				return
			}
			if !collabServer {
				t.Fatalf("collab server was not injected: %#v", servers)
			}
			found := false
			for _, server := range servers {
				if server.Name != collabMCPName {
					continue
				}
				found = true
				if server.URL != "http://127.0.0.1:8766/api/v1/agent/collab/mcp" ||
					server.URLFromEnv != "AHA2_AGENT_API_URL" ||
					server.URLPath != "/api/v1/agent/collab/mcp" ||
					server.BearerTokenEnv != "AHA2_AGENT_API_TOKEN" {
					t.Fatalf("collab server=%#v", server)
				}
			}
			if !found {
				t.Fatal("collab server disappeared")
			}
			want := make([]string, 0, len(CollabMCPToolNames()))
			for _, name := range CollabMCPToolNames() {
				want = append(want, "mcp__"+collabMCPName+"__"+name)
			}
			got := make([]string, 0, len(want))
			for _, name := range allowed {
				if strings.HasPrefix(name, "mcp__"+collabMCPName+"__") {
					got = append(got, name)
				}
			}
			if len(got) != len(want) {
				t.Fatalf("allowed collab tools=%#v want=%#v", got, want)
			}
			for index := range want {
				if got[index] != want[index] {
					t.Fatalf("allowed collab tools=%#v want=%#v", got, want)
				}
			}
		})
	}
}

// TestCollabMCPRuntimeExcludesChannelTurns proves the runtime uses the Turn's
// actual batch: a mixed batch in either order, a malformed payload and an
// active task route all suppress injection.
func TestCollabMCPRuntimeExcludesChannelTurns(t *testing.T) {
	f := newCollabMCPFixture(t, "auto", 3)
	servers, allowed := f.service.mcpRuntimeForTurn(f.ctx, f.task, "main", "http://127.0.0.1:8766", "")
	if !collabRuntimeHas(servers, allowed) {
		t.Fatalf("plain Turn lost collab MCP: %#v %#v", servers, allowed)
	}
	for _, order := range []struct {
		name  string
		kinds []string
	}{
		{name: "plain-first", kinds: []string{"plain", "channel:conversation_host"}},
		{name: "channel-first", kinds: []string{"channel:conversation_host", "plain"}},
	} {
		batchID := "batch-collab-runtime-" + order.name
		collabInsertInbox(t, f.database, f.task, f.turn.RoundID, batchID, order.kinds...)
		servers, allowed = f.service.mcpRuntimeForTurn(f.ctx, f.task, "main", "http://127.0.0.1:8766", batchID)
		if collabRuntimeHas(servers, allowed) {
			t.Fatalf("%s mixed batch received collab MCP: %#v %#v", order.name, servers, allowed)
		}
	}
	collabInsertInbox(t, f.database, f.task, f.turn.RoundID, "batch-collab-runtime-malformed", "raw:not-json")
	servers, allowed = f.service.mcpRuntimeForTurn(f.ctx, f.task, "main", "http://127.0.0.1:8766", "batch-collab-runtime-malformed")
	if collabRuntimeHas(servers, allowed) {
		t.Fatalf("malformed batch received collab MCP: %#v %#v", servers, allowed)
	}
}

func collabRuntimeHas(servers []backend.MCPServer, allowed []string) bool {
	for _, server := range servers {
		if server.Name == collabMCPName {
			return true
		}
	}
	for _, name := range allowed {
		if strings.HasPrefix(name, "mcp__"+collabMCPName+"__") {
			return true
		}
	}
	return false
}

// TestCollabMCPSubmitAndWaitRules covers the server-side dispatch and polling
// boundaries: a normal Main submit, the Max Agents refusal, the single-agent
// refusal on both surfaces, and a normal timeout instead of an error.
func TestCollabMCPSubmitAndWaitRules(t *testing.T) {
	f := newCollabMCPFixture(t, "auto", 3)
	result, err := f.service.AgentCollabMCPSubmit(f.ctx, f.claims("main"), collabSubmitInput("implement the slice"))
	if err != nil {
		t.Fatal(err)
	}
	if result.BatchID == "" || result.CreatedAgents != 1 {
		t.Fatalf("submit result=%#v", result)
	}
	progress, err := f.service.AgentCollabMCPWait(f.ctx, f.claims("main"), result.BatchID, 0, 2*time.Second)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if progress.BatchID != result.BatchID {
		t.Fatalf("wait batch=%q want=%q", progress.BatchID, result.BatchID)
	}
	// A cursor far ahead must not redeliver earlier events.
	seen, err := f.service.AgentCollabMCPWait(f.ctx, f.claims("main"), result.BatchID, 1<<40, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen.Results) != 0 {
		t.Fatalf("cursor redelivered events: %#v", seen.Results)
	}

	if _, err := f.service.AgentCollabMCPSubmit(f.ctx, f.claims("main"), AgentCollabMCPSubmitInput{
		Actions: []AgentCollabMCPAction{{
			AgentID: collabStringPtr("sub-001"), Title: collabStringPtr("Duplicate"),
			Assignment: collabStringPtr("again"),
		}},
		IntegrationInstructions: collabStringPtr("integrate"),
	}); err == nil {
		t.Fatal("duplicate assignment was silently accepted")
	}

	capped := newCollabMCPFixture(t, "auto", 1)
	if _, err := capped.service.AgentCollabMCPSubmit(capped.ctx, capped.claims("main"), collabSubmitInput("capped")); !errors.Is(err, ErrAgentCollabMCPNothingQueued) {
		t.Fatalf("max agents error=%v", err)
	}

	single := newCollabMCPFixture(t, "single", 1)
	if _, err := single.service.AgentCollabMCPSubmit(single.ctx, single.claims("main"), collabSubmitInput("single")); !errors.Is(err, ErrAgentCollaborationRequiresAuto) {
		t.Fatalf("single-mode MCP submit error=%v", err)
	}
	if _, _, err := single.service.SubmitAgentCollaboration(single.ctx, single.claims("main"), []AgentAction{{
		AgentID: "sub-001", Title: "Single", Assignment: "single", Required: true,
	}}, "integrate"); !errors.Is(err, ErrAgentCollaborationRequiresAuto) {
		t.Fatalf("single-mode HTTP submit error=%v", err)
	}

	if _, err := f.service.AgentCollabMCPSubmit(f.ctx, f.claims("sub-001"), collabSubmitInput("sub")); !errors.Is(err, ErrAgentCallForbidden) {
		t.Fatalf("non-main submit error=%v", err)
	}

	if _, err := f.service.AgentCollabMCPSubmit(f.ctx, f.claims("main"), AgentCollabMCPSubmitInput{
		IntegrationInstructions: collabStringPtr("integrate"),
	}); !errors.Is(err, ErrAgentCollabMCPInvalidInput) {
		t.Fatalf("empty actions error=%v", err)
	}
	if _, err := f.service.AgentCollabMCPSubmit(f.ctx, f.claims("main"), AgentCollabMCPSubmitInput{
		Actions:                 []AgentCollabMCPAction{{Assignment: collabStringPtr("missing ids")}},
		IntegrationInstructions: collabStringPtr("integrate"),
	}); !errors.Is(err, ErrAgentCollabMCPInvalidInput) {
		t.Fatalf("missing fields error=%v", err)
	}
	if _, err := f.service.AgentCollabMCPSubmit(f.ctx, f.claims("main"), AgentCollabMCPSubmitInput{
		Actions: []AgentCollabMCPAction{{
			AgentID: collabStringPtr("sub-002"), Title: collabStringPtr("No instructions"),
			Assignment: collabStringPtr("work"),
		}},
	}); !errors.Is(err, ErrAgentCollabMCPInvalidInput) {
		t.Fatalf("missing instructions error=%v", err)
	}
}

// TestCollabMCPWaitScopesBatchesToTask proves another Task's batch and an
// unknown id produce the same forbidden classification, and that an owned batch
// that never completes returns normally with complete=false.
func TestCollabMCPWaitScopesBatchesToTask(t *testing.T) {
	f := newCollabMCPFixture(t, "auto", 3)
	collabCreateBatch(t, f.database, "batch-collab-foreign", "task-foreign", "round-foreign", "turn-foreign")
	if _, err := f.service.AgentCollabMCPWait(f.ctx, f.claims("main"), "batch-collab-foreign", 0, 50*time.Millisecond); !errors.Is(err, ErrAgentCallForbidden) {
		t.Fatalf("foreign batch error=%v", err)
	}
	if _, err := f.service.AgentCollabMCPWait(f.ctx, f.claims("main"), "batch-collab-unknown", 0, 50*time.Millisecond); !errors.Is(err, ErrAgentCallForbidden) {
		t.Fatalf("unknown batch error=%v", err)
	}
	if _, err := f.service.AgentCollabMCPWait(f.ctx, f.claims("main"), "  ", 0, 50*time.Millisecond); !errors.Is(err, ErrAgentCollabMCPBatchInvalid) {
		t.Fatalf("empty batch error=%v", err)
	}
	if _, err := f.service.AgentCollabMCPWait(f.ctx, f.claims("sub-001"), "batch-collab-unknown", 0, 50*time.Millisecond); !errors.Is(err, ErrAgentCallForbidden) {
		t.Fatalf("non-main wait error=%v", err)
	}

	collabCreateBatch(t, f.database, "batch-collab-own-pending", f.task.ID, f.turn.RoundID, f.turn.ID)
	started := time.Now()
	progress, err := f.service.AgentCollabMCPWait(f.ctx, f.claims("main"), "batch-collab-own-pending", 0, 120*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Complete {
		t.Fatalf("pending batch reported complete: %#v", progress)
	}
	if time.Since(started) < 100*time.Millisecond {
		t.Fatalf("wait returned before its timeout: %s", time.Since(started))
	}
	if progress.BatchID != "batch-collab-own-pending" {
		t.Fatalf("pending batch id=%q", progress.BatchID)
	}
}

func TestCollabMCPIsListedAsAutoInjectedBuiltin(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := NewService(database, nil, nil)

	servers, err := service.MCPServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var collab domain.MCPServer
	found := false
	for _, item := range servers {
		if item.ID == collabMCPName {
			collab = item
			found = true
		}
	}
	if !found {
		t.Fatal("collab MCP is missing from the catalog")
	}
	if !collab.Builtin || !collab.AutoInject || !collab.Enabled || collab.Health != "ok" {
		t.Fatalf("unexpected collab MCP: %#v", collab)
	}
	if len(collab.Tools) != len(CollabMCPToolNames()) {
		t.Fatalf("collab MCP tools=%#v", collab.Tools)
	}
	detail, err := service.MCPServerDetail(ctx, collabMCPName)
	if err != nil || len(detail.Tools) != len(CollabMCPToolNames()) {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if _, err := service.SaveMCPServer(ctx, domain.MCPServer{
		ID: collabMCPName, Name: "x", Transport: "http", URL: "https://example.test",
	}); err == nil {
		t.Fatal("reserved collab MCP save was allowed")
	}
	if err := service.DeleteMCPServer(ctx, collabMCPName); err == nil {
		t.Fatal("collab MCP delete was allowed")
	}
	normalized, err := service.normalizeTaskMCPServers(ctx, domain.Project{ID: "project-normal"}, []string{collabMCPName})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 0 {
		t.Fatalf("collab MCP was assignable to a task: %#v", normalized)
	}
}

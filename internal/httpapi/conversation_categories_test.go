package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestConversationCategoriesQueryNormalization(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"", "[]", true},
		{" , ,", "[]", true},
		{"chat,update", "[chat update]", true},
		{"THINKING", "[thinking]", true},
		{" Update , thinking ,update", "[update thinking]", true},
		{"nope,tool", "[tool]", true},
		{"nope", "", false},
		{"nope, Other", "", false},
	}
	for _, tc := range cases {
		got, ok := conversationCategoriesQuery(tc.raw)
		if ok != tc.ok || (ok && fmt.Sprint(got) != tc.want) {
			t.Fatalf("%q: got %v ok=%t, want %s ok=%t", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

type categoryQueryCase struct {
	query  string
	status int
	want   string
}

// The same normalization runs for a local Task and a read-only mirror. The
// only intended difference is the default for a missing/blank value: every
// category locally, the readable set (no tool) on a mirror.
func TestConversationCategoriesLocalAndMirrorAgree(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Second)
	rows := []struct{ id, category, kind string }{
		{"cc1-chat", "chat", "agent_message"},
		{"cc2-update", "update", "agent_progress"},
		{"cc3-thinking", "update", domain.ConversationKindReasoning},
		{"cc4-tool", "tool", "agent_command_finished"},
		{"cc5-error", "error", "agent_error"},
	}

	local := createHardwareAPITask(t, database)
	for index, row := range rows {
		if _, err := database.AddConversationItem(ctx, domain.ConversationItem{
			ID: row.id, TaskID: local.ID, AgentID: "main", StreamAgentID: "main",
			Category: row.category, Kind: row.kind, Summary: row.id, CreatedAt: now.Add(time.Duration(index) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	project := domain.Project{ID: "project-categories", Name: "Remote", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	source := domain.Task{ID: "source-categories", ProjectID: project.ID, Title: "Remote categories", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: "remote-categories-owner", ObjectType: "task", ObjectID: source.ID, TaskID: source.ID, ProjectID: project.ID, Payload: mustJSON(t, source), CreatedAt: now, UpdatedAt: now})
	for index, row := range rows {
		item := domain.ConversationItem{
			ID: row.id, TaskID: source.ID, AgentID: "main", StreamAgentID: "main",
			Category: row.category, Kind: row.kind, Summary: row.id, CreatedAt: now.Add(time.Duration(index) * time.Second),
		}
		putRemoteTaskObject(t, database, store.RemoteTaskObject{
			OwnerDeviceID: "remote-categories-owner", ObjectType: "conversation", ObjectID: item.ID, TaskID: source.ID,
			ProjectID: project.ID, Payload: mustJSON(t, item), CreatedAt: item.CreatedAt, UpdatedAt: item.CreatedAt,
		})
	}
	mirrors, err := database.RemoteTaskMirrors(ctx, "")
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("mirrors=%d err=%v", len(mirrors), err)
	}
	mirrorID := mirrors[0].Task.ID

	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	_ = registerOwner(t, client, server.URL)

	shared := []categoryQueryCase{
		{"categories=chat,error,update", http.StatusOK, "[cc1-chat cc2-update cc5-error]"},
		{"categories=THINKING", http.StatusOK, "[cc3-thinking]"},
		{"categories=%20Update%20,Thinking", http.StatusOK, "[cc2-update cc3-thinking]"},
		{"categories=nope,tool", http.StatusOK, "[cc4-tool]"},
		{"categories=nope,Thinking,", http.StatusOK, "[cc3-thinking]"},
		{"categories=nope", http.StatusBadRequest, ""},
		{"categories=NOPE,other", http.StatusBadRequest, ""},
	}
	localCases := append([]categoryQueryCase{
		{"", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc4-tool cc5-error]"},
		{"categories=", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc4-tool cc5-error]"},
		{"categories=,%20,", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc4-tool cc5-error]"},
	}, shared...)
	mirrorCases := append([]categoryQueryCase{
		{"", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc5-error]"},
		{"categories=", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc5-error]"},
		{"categories=,%20,", http.StatusOK, "[cc1-chat cc2-update cc3-thinking cc5-error]"},
	}, shared...)
	for name, target := range map[string]struct {
		taskID string
		cases  []categoryQueryCase
	}{"local": {local.ID, localCases}, "mirror": {mirrorID, mirrorCases}} {
		for _, tc := range target.cases {
			status, ids := conversationIDs(t, client, server.URL, target.taskID, "limit=50&"+tc.query, "cc")
			if status != tc.status || (status == http.StatusOK && ids != tc.want) {
				t.Fatalf("%s %q: status=%d ids=%s, want %d %s", name, tc.query, status, ids, tc.status, tc.want)
			}
		}
	}
}

// Older synced payloads may lack "kind" or carry null. They are not
// reasoning rows, so they stay in Agent Update and never reach Thinking.
func TestRemoteConversationMissingKindStaysInUpdate(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Second)
	project := domain.Project{ID: "project-missing-kind", Name: "Remote", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	source := domain.Task{ID: "source-missing-kind", ProjectID: project.ID, Title: "Missing kind", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
	owner := "remote-missing-kind-owner"
	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: owner, ObjectType: "task", ObjectID: source.ID, TaskID: source.ID, ProjectID: project.ID, Payload: mustJSON(t, source), CreatedAt: now, UpdatedAt: now})
	payloads := []struct {
		id      string
		payload string
	}{
		{"mk1-nokind", `{"id":"mk1-nokind","task_id":"source-missing-kind","agent_id":"main","category":"update","summary":"no kind"}`},
		{"mk2-nullkind", `{"id":"mk2-nullkind","task_id":"source-missing-kind","agent_id":"main","category":"update","kind":null,"summary":"null kind"}`},
		{"mk3-thinking", `{"id":"mk3-thinking","task_id":"source-missing-kind","agent_id":"main","category":"update","kind":"agent_reasoning_update","summary":"thinking"}`},
	}
	for index, row := range payloads {
		at := now.Add(time.Duration(index) * time.Second)
		putRemoteTaskObject(t, database, store.RemoteTaskObject{
			OwnerDeviceID: owner, ObjectType: "conversation", ObjectID: row.id, TaskID: source.ID,
			ProjectID: project.ID, Payload: json.RawMessage(row.payload), CreatedAt: at, UpdatedAt: at,
		})
	}
	mirrors, err := database.RemoteTaskMirrors(ctx, "")
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("mirrors=%d err=%v", len(mirrors), err)
	}
	for query, want := range map[string]string{
		"update":          "[mk1-nokind mk2-nullkind]",
		"thinking":        "[mk3-thinking]",
		"update,thinking": "[mk1-nokind mk2-nullkind mk3-thinking]",
	} {
		page, err := database.RemoteConversationPageForAgent(ctx, mirrors[0].Task.ID, "main", 0, 0, 50, strings.Split(query, ","))
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if fmt.Sprint(ids) != want {
			t.Fatalf("%s: got %v want %s", query, ids, want)
		}
	}
}

// A filtered incremental read on a mirror must advance only through the rows
// it returned. limit=1 rounds walk every matching row exactly once, also after
// new rows sync in, and a read with nothing new keeps the cursor in place.
func TestRemoteConversationIncrementalCursorDoesNotSkipRows(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Second)
	project := domain.Project{ID: "project-incremental", Name: "Remote", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	source := domain.Task{ID: "source-incremental", ProjectID: project.ID, Title: "Incremental", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
	owner := "remote-incremental-owner"
	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: owner, ObjectType: "task", ObjectID: source.ID, TaskID: source.ID, ProjectID: project.ID, Payload: mustJSON(t, source), CreatedAt: now, UpdatedAt: now})
	cycle := []struct{ bucket, category, kind string }{
		{"chat", "chat", "agent_message"},
		{"update", "update", "agent_progress"},
		{"thinking", "update", domain.ConversationKindReasoning},
		{"tool", "tool", "agent_command_finished"},
		{"thinking", "update", domain.ConversationKindReasoning},
		{"update", "update", "agent_message_update"},
		{"error", "error", "agent_error"},
	}
	count := 0
	add := func(n int) {
		for index := 0; index < n; index++ {
			count++
			row := cycle[(count-1)%len(cycle)]
			item := domain.ConversationItem{
				ID: fmt.Sprintf("in%02d-%s", count, row.bucket), TaskID: source.ID, AgentID: "main", StreamAgentID: "main",
				Category: row.category, Kind: row.kind, Summary: row.bucket, CreatedAt: now.Add(time.Duration(count) * time.Second),
			}
			putRemoteTaskObject(t, database, store.RemoteTaskObject{
				OwnerDeviceID: owner, ObjectType: "conversation", ObjectID: item.ID, TaskID: source.ID,
				ProjectID: project.ID, Payload: mustJSON(t, item), CreatedAt: item.CreatedAt, UpdatedAt: item.CreatedAt,
			})
		}
	}
	add(14)
	mirrors, err := database.RemoteTaskMirrors(ctx, "")
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("mirrors=%d err=%v", len(mirrors), err)
	}
	mirrorID := mirrors[0].Task.ID
	expect := func(from int, buckets ...string) string {
		want := map[string]bool{}
		for _, bucket := range buckets {
			want[bucket] = true
		}
		ids := []string{}
		for index := from + 1; index <= count; index++ {
			row := cycle[(index-1)%len(cycle)]
			if want[row.bucket] {
				ids = append(ids, fmt.Sprintf("in%02d-%s", index, row.bucket))
			}
		}
		return fmt.Sprint(ids)
	}
	walk := func(categories []string, cursor int64) ([]string, int64) {
		t.Helper()
		ids := []string{}
		for guard := 0; guard < 40; guard++ {
			page, err := database.RemoteConversationPageForAgent(ctx, mirrorID, "main", 0, cursor, 1, categories)
			if err != nil {
				t.Fatal(err)
			}
			if page.Latest < cursor {
				t.Fatalf("%v: cursor moved back from %d to %d", categories, cursor, page.Latest)
			}
			for _, item := range page.Items {
				ids = append(ids, item.ID)
			}
			cursor = page.Latest
			if len(page.Items) == 0 {
				return ids, cursor
			}
		}
		t.Fatalf("%v: incremental walk did not finish", categories)
		return nil, cursor
	}
	for _, categories := range [][]string{{"update"}, {"thinking"}, {"update", "thinking"}, {"chat", "error", "update"}} {
		ids, cursor := walk(categories, 1)
		if fmt.Sprint(ids) != expect(1, categories...) {
			t.Fatalf("%v: walked %v want %s", categories, ids, expect(1, categories...))
		}
		// Nothing new: the cursor stays where it is.
		again, stable := walk(categories, cursor)
		if len(again) != 0 || stable != cursor {
			t.Fatalf("%v: empty read returned %v and moved cursor %d -> %d", categories, again, cursor, stable)
		}
		// New rows sync in later; the next walk picks up exactly the new ones.
		before := count
		add(7)
		more, _ := walk(categories, cursor)
		if fmt.Sprint(more) != expect(before, categories...) {
			t.Fatalf("%v: after sync walked %v want %s", categories, more, expect(before, categories...))
		}
	}
}

func conversationIDs(t *testing.T, client *http.Client, baseURL, taskID, query, prefix string) (int, string) {
	t.Helper()
	endpoint := baseURL + "/api/v1/tasks/" + url.PathEscape(taskID) + "/agents/main/conversation?" + query
	response := requestJSON(t, client, http.MethodGet, endpoint, nil, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, ""
	}
	var body struct {
		Conversation domain.ConversationPage `json:"conversation"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, item := range body.Conversation.Items {
		if strings.HasPrefix(item.ID, prefix) {
			ids = append(ids, item.ID)
		}
	}
	return response.StatusCode, fmt.Sprint(ids)
}

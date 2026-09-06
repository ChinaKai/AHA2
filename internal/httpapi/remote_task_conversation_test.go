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
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestRemoteTaskConversationIsPagedAndDefaultsToReadableCategories(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Second)
	project := domain.Project{ID: "project-remote-page", Name: "Remote", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "source-task-page", ProjectID: project.ID, Title: "Paged remote task", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: "remote-page-owner", ObjectType: "task", ObjectID: task.ID, TaskID: task.ID, ProjectID: project.ID, Payload: mustJSON(t, task), CreatedAt: now, UpdatedAt: now})
	for index := 1; index <= 80; index++ {
		category := "chat"
		if index%4 == 0 {
			category = "tool"
		}
		item := domain.ConversationItem{
			ID: fmt.Sprintf("message-%03d", index), TaskID: task.ID, AgentID: "main", StreamAgentID: "main",
			Category: category, Kind: "agent_message", Summary: fmt.Sprintf("message %d", index), CreatedAt: now.Add(time.Duration(index) * time.Second),
		}
		putRemoteTaskObject(t, database, store.RemoteTaskObject{
			OwnerDeviceID: "remote-page-owner", ObjectType: "conversation", ObjectID: item.ID, TaskID: task.ID,
			ProjectID: project.ID, Payload: mustJSON(t, item), CreatedAt: item.CreatedAt, UpdatedAt: item.CreatedAt,
		})
	}
	mirrors, err := database.RemoteTaskMirrors(ctx, "")
	if err != nil || len(mirrors) != 1 {
		t.Fatalf("mirrors=%d err=%v", len(mirrors), err)
	}
	publicID := mirrors[0].Task.ID

	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	_ = registerOwner(t, client, server.URL)

	first := getRemoteConversationPage(t, client, server.URL, publicID, "limit=20")
	if len(first.Items) != 20 || !first.HasMore || first.NextBefore == 0 || first.Latest != 80 {
		t.Fatalf("unexpected first page: len=%d has_more=%t next=%d latest=%d", len(first.Items), first.HasMore, first.NextBefore, first.Latest)
	}
	for index, item := range first.Items {
		if item.Category == "tool" {
			t.Fatalf("default remote page included tool item %#v", item)
		}
		if item.TaskID != publicID {
			t.Fatalf("item %d retained source task id %q", index, item.TaskID)
		}
		if index > 0 && first.Items[index-1].Sequence >= item.Sequence {
			t.Fatalf("page is not chronological: %#v", first.Items)
		}
	}
	second := getRemoteConversationPage(t, client, server.URL, publicID, fmt.Sprintf("limit=20&before=%d", first.NextBefore))
	if len(second.Items) != 20 || !second.HasMore || second.Items[len(second.Items)-1].Sequence >= first.Items[0].Sequence {
		t.Fatalf("unexpected older page: first=%#v second=%#v", first, second)
	}
	toolPage := getRemoteConversationPage(t, client, server.URL, publicID, "limit=20&categories=tool")
	if len(toolPage.Items) != 20 || toolPage.HasMore {
		t.Fatalf("unexpected tool page: len=%d has_more=%t", len(toolPage.Items), toolPage.HasMore)
	}
	for _, item := range toolPage.Items {
		if item.Category != "tool" {
			t.Fatalf("explicit tool page included %q", item.Category)
		}
	}
}

func TestRemoteTaskDetailDoesNotMaterializeConversation(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	project := domain.Project{ID: "project-detail", Name: "Remote", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	task := domain.Task{ID: "source-detail", ProjectID: project.ID, Title: "Detail without history", Status: domain.TaskWaitingUser, CreatedAt: now, UpdatedAt: now}
	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: "remote-detail-owner", ObjectType: "task", ObjectID: task.ID, TaskID: task.ID, ProjectID: project.ID, Payload: mustJSON(t, task), CreatedAt: now, UpdatedAt: now})
	putRemoteTaskObject(t, database, store.RemoteTaskObject{OwnerDeviceID: "remote-detail-owner", ObjectType: "conversation", ObjectID: "broken-history", TaskID: task.ID, ProjectID: project.ID, Payload: json.RawMessage(`not-json`), CreatedAt: now, UpdatedAt: now})
	mirrors, _ := database.RemoteTaskMirrors(ctx, "")

	authService := auth.NewService(database, "setup-test", time.Hour)
	server := httptest.NewServer(New(Config{Store: database, Auth: authService, App: app.NewService(database, nil, app.StubExecutor{})}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	_ = registerOwner(t, client, server.URL)
	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/tasks/"+mirrors[0].Task.ID, nil, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("remote detail status=%d", response.StatusCode)
	}
}

func putRemoteTaskObject(t *testing.T, database *store.Store, item store.RemoteTaskObject) {
	t.Helper()
	if err := database.UpsertRemoteTaskObject(context.Background(), item); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func getRemoteConversationPage(t *testing.T, client *http.Client, baseURL, taskID, query string) domain.ConversationPage {
	t.Helper()
	endpoint := baseURL + "/api/v1/tasks/" + url.PathEscape(taskID) + "/agents/main/conversation?" + query
	response := requestJSON(t, client, http.MethodGet, endpoint, nil, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("conversation status=%d", response.StatusCode)
	}
	var body struct {
		Conversation domain.ConversationPage `json:"conversation"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Conversation
}

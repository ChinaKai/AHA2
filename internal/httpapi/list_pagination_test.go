package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestProjectListPaginationAndSummaryAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"project-a", "project-b", "project-c"} {
		if err := database.CreateProject(ctx, domain.Project{
			ID: id, Name: id, Description: "description", ProjectType: "git",
			RepositoryIdentity: "repository/" + id, KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour)}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/projects?limit=2&view=summary", nil, "")
	var first map[string]any
	decodeResponse(t, response, &first)
	projects := first["projects"].([]any)
	if response.StatusCode != http.StatusOK || len(projects) != 2 || first["has_more"] != true || first["next_cursor"] == "" {
		t.Fatalf("first page status=%d response=%#v", response.StatusCode, first)
	}
	if _, found := projects[0].(map[string]any)["repository_identity"]; found {
		t.Fatalf("summary leaked repository identity: %#v", projects[0])
	}
	cursor := first["next_cursor"].(string)
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/projects?limit=2&cursor="+cursor, nil, "")
	var second map[string]any
	decodeResponse(t, response, &second)
	if len(second["projects"].([]any)) != 1 || second["has_more"] != false || second["next_cursor"] != "" {
		t.Fatalf("second page=%#v", second)
	}
	response = requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/projects", nil, "")
	var legacy map[string]any
	decodeResponse(t, response, &legacy)
	if _, found := legacy["has_more"]; found || len(legacy["projects"].([]any)) != 3 {
		t.Fatalf("legacy response changed: %#v", legacy)
	}
	if legacy["projects"].([]any)[0].(map[string]any)["repository_identity"] == "" {
		t.Fatalf("legacy detail fields missing: %#v", legacy)
	}
}

func TestKnowledgePaginationIncludesGlobalNavigationContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Add(5 * time.Minute)
	for index, id := range []string{"global-recent-a", "global-recent-b"} {
		item := domain.KnowledgeEntry{
			ID: id, Scope: "global", ParentID: store.GlobalGeneralKnowledgeID, Slug: id,
			Type: "practice", Title: id, Body: "body", Status: domain.KnowledgeVerified,
			Confidence: 1, Revision: 1, CreatedAt: now.Add(time.Duration(index) * time.Minute),
			UpdatedAt: now.Add(time.Duration(index) * time.Minute), LastVerifiedAt: now,
		}
		if err := database.CreateKnowledge(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour)}).Handler())
	defer server.Close()
	client := newCookieClient(t)
	registerOwner(t, client, server.URL)

	response := requestJSON(t, client, http.MethodGet, server.URL+"/api/v1/knowledge?limit=1&summary=true", nil, "")
	var payload map[string]any
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || payload["has_more"] != true || payload["next_cursor"] == "" {
		t.Fatalf("knowledge page status=%d payload=%#v", response.StatusCode, payload)
	}
	entries := payload["knowledge"].([]any)
	byID := map[string]map[string]any{}
	for _, raw := range entries {
		item := raw.(map[string]any)
		byID[item["id"].(string)] = item
	}
	for _, id := range []string{
		store.GlobalKnowledgeRootID,
		store.GlobalGeneralKnowledgeID,
		store.GlobalAgentLessonsKnowledgeID,
		store.GlobalTechnicalLessonsKnowledgeID,
		store.GlobalBehaviorLessonsKnowledgeID,
		"global-recent-b",
	} {
		if byID[id] == nil {
			t.Fatalf("knowledge page omitted %s: %#v", id, byID)
		}
	}
	if body := strings.TrimSpace(byID[store.GlobalGeneralKnowledgeID]["body"].(string)); body == "" {
		t.Fatalf("global navigation summary omitted its description: %#v", byID[store.GlobalGeneralKnowledgeID])
	}
	if _, found := byID["global-recent-a"]; found {
		t.Fatalf("navigation context changed the one-item page boundary: %#v", byID)
	}
}

func TestPaginateByUpdatedUsesStableKeysetCursor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	items := []domain.Project{
		{ID: "project-b", UpdatedAt: now},
		{ID: "project-old", UpdatedAt: now.Add(-time.Minute)},
		{ID: "project-c", UpdatedAt: now},
		{ID: "project-new", UpdatedAt: now.Add(time.Minute)},
		{ID: "project-a", UpdatedAt: now},
	}
	options := listOptions{Paged: true, Limit: 2}
	first, err := paginateByUpdated(items, "projects", options, func(item domain.Project) (time.Time, string) {
		return item.UpdatedAt, item.ID
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := projectIDs(first.Items); !reflect.DeepEqual(got, []string{"project-new", "project-c"}) || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page=%v has_more=%t cursor=%q", got, first.HasMore, first.NextCursor)
	}
	// A newer row inserted between requests must not shift the continuation or
	// duplicate an item from the first page.
	items = append(items, domain.Project{ID: "project-newer", UpdatedAt: now.Add(2 * time.Minute)})
	options.Cursor = first.NextCursor
	second, err := paginateByUpdated(items, "projects", options, func(item domain.Project) (time.Time, string) {
		return item.UpdatedAt, item.ID
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := projectIDs(second.Items); !reflect.DeepEqual(got, []string{"project-b", "project-a"}) || !second.HasMore {
		t.Fatalf("second page=%v has_more=%t", got, second.HasMore)
	}
	options.Cursor = second.NextCursor
	last, err := paginateByUpdated(items, "projects", options, func(item domain.Project) (time.Time, string) {
		return item.UpdatedAt, item.ID
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := projectIDs(last.Items); !reflect.DeepEqual(got, []string{"project-old"}) || last.HasMore || last.NextCursor != "" {
		t.Fatalf("last page=%v has_more=%t cursor=%q", got, last.HasMore, last.NextCursor)
	}
}

func TestListOptionsBoundariesAndCursorKinds(t *testing.T) {
	t.Parallel()
	for _, rawURL := range []string{
		"/api/v1/tasks?limit=0",
		"/api/v1/tasks?limit=201",
		"/api/v1/tasks?limit=2&page_size=3",
		"/api/v1/tasks?summary=maybe",
		"/api/v1/tasks?view=unknown",
	} {
		if _, err := parseListOptions(httptest.NewRequest("GET", rawURL, nil)); err == nil {
			t.Fatalf("invalid options accepted: %s", rawURL)
		}
	}
	options, err := parseListOptions(httptest.NewRequest("GET", "/api/v1/tasks?page_size=200&view=summary", nil))
	if err != nil || !options.Paged || !options.Summary || options.Limit != 200 {
		t.Fatalf("valid options=%#v err=%v", options, err)
	}
	cursor := encodeListCursor(listCursor{Kind: "projects", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano), ID: "project-a"})
	if _, err := paginateByUpdated([]domain.Task{}, "tasks", listOptions{Paged: true, Limit: 10, Cursor: cursor}, func(item domain.Task) (time.Time, string) {
		return item.UpdatedAt, item.ID
	}); err == nil {
		t.Fatal("cross-resource cursor was accepted")
	}
}

// TestPaginateTasksByAttentionMatchesStoreOrder covers the in-memory path used
// when remote mirrors are merged in: unfinished and failed Tasks come first even
// though every completed Task is newer, and the cursor crosses the group edge.
func TestPaginateTasksByAttentionMatchesStoreOrder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	items := []domain.Task{
		{ID: "done-new", Status: domain.TaskCompleted, CreatedAt: now.Add(3 * time.Minute)},
		{ID: "open-old", Status: domain.TaskActive, CreatedAt: now.Add(-time.Minute)},
		{ID: "done-old", Status: domain.TaskCompleted, CreatedAt: now.Add(time.Minute)},
		{ID: "failed-b", Status: domain.TaskFailed, CreatedAt: now},
		{ID: "blocked-a", Status: domain.TaskBlocked, CreatedAt: now},
	}
	options := listOptions{Paged: true, Limit: 2}
	var got []string
	for pageCount := 0; pageCount < 5; pageCount++ {
		page, err := paginateTasksByAttention(append([]domain.Task(nil), items...), options)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			got = append(got, item.ID)
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatalf("last page kept a cursor: %q", page.NextCursor)
			}
			break
		}
		options.Cursor = page.NextCursor
	}
	if want := []string{"failed-b", "blocked-a", "open-old", "done-new", "done-old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attention order=%v want %v", got, want)
	}
	unpaged, err := paginateTasksByAttention(append([]domain.Task(nil), items...), listOptions{})
	if err != nil || len(unpaged.Items) != len(items) || unpaged.Items[0].ID != "failed-b" || unpaged.HasMore {
		t.Fatalf("unpaged attention list=%#v err=%v", unpaged, err)
	}
}

// TestTaskListOrderCursorsDoNotCrossOrders keeps the default creation-order
// cursor working as before and rejects a cursor continued under another order,
// which would otherwise skip or repeat Tasks silently.
func TestTaskListOrderCursorsDoNotCrossOrders(t *testing.T) {
	t.Parallel()
	for rawURL, want := range map[string]string{
		"/api/v1/tasks":                    "",
		"/api/v1/tasks?order=created":      "",
		"/api/v1/tasks?order=%20Attention": taskOrderAttention,
	} {
		order, err := parseTaskListOrder(httptest.NewRequest("GET", rawURL, nil))
		if err != nil || order != want {
			t.Fatalf("%s order=%q err=%v want %q", rawURL, order, err, want)
		}
	}
	if _, err := parseTaskListOrder(httptest.NewRequest("GET", "/api/v1/tasks?order=updated", nil)); err == nil {
		t.Fatal("unknown order accepted")
	}

	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	legacy := encodeListCursor(listCursor{Kind: "tasks", UpdatedAt: at, ID: "task-a"})
	created := encodeListCursor(listCursor{Kind: "tasks", At: at, ID: "task-a"})
	attention := taskAttentionCursor(domain.Task{ID: "task-a", Status: domain.TaskCompleted, CreatedAt: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)})
	for _, cursor := range []string{legacy, created} {
		if _, _, err := listCursorPosition(listOptions{Paged: true, Limit: 10, Cursor: cursor}, "tasks"); err != nil {
			t.Fatalf("creation-order cursor rejected: %v", err)
		}
		if _, _, _, err := taskAttentionCursorPosition(listOptions{Paged: true, Limit: 10, Cursor: cursor}); err == nil {
			t.Fatal("creation-order cursor accepted by the attention order")
		}
	}
	if _, _, err := listCursorPosition(listOptions{Paged: true, Limit: 10, Cursor: attention}, "tasks"); err == nil {
		t.Fatal("attention cursor accepted by the creation order")
	}
	rank, position, id, err := taskAttentionCursorPosition(listOptions{Paged: true, Limit: 10, Cursor: attention})
	if err != nil || rank != 1 || id != "task-a" || position.Format(time.RFC3339Nano) != at {
		t.Fatalf("attention cursor rank=%d at=%s id=%s err=%v", rank, position, id, err)
	}
	badRank := encodeListCursor(listCursor{Kind: "tasks", Order: taskOrderAttention, Rank: 2, At: at, ID: "task-a"})
	if _, _, _, err := taskAttentionCursorPosition(listOptions{Paged: true, Limit: 10, Cursor: badRank}); err == nil {
		t.Fatal("out-of-range attention rank accepted")
	}
}

func TestSummaryViewsOmitHeavyDetailFields(t *testing.T) {
	t.Parallel()
	projectsJSON, err := json.Marshal(summarizeProjects([]domain.Project{{ID: "project-a", RepositoryIdentity: "large-repository-identity"}}))
	if err != nil {
		t.Fatal(err)
	}
	tasksJSON, err := json.Marshal(summarizeTasks([]domain.Task{{ID: "task-a", OriginalRequest: "large original request", SkillIDs: []string{"skill-a"}}}))
	if err != nil {
		t.Fatal(err)
	}
	knowledgeJSON, err := json.Marshal(summarizeKnowledge([]domain.KnowledgeEntry{{ID: "knowledge-a", Body: "large body", ContentHash: "hash"}}))
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]string{"projects": string(projectsJSON), "tasks": string(tasksJSON), "knowledge": string(knowledgeJSON)} {
		for _, forbidden := range []string{"large-repository-identity", "large original request", "skill-a", "large body", "\"content_hash\""} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("%s summary retained %q: %s", name, forbidden, payload)
			}
		}
	}
}

func projectIDs(items []domain.Project) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result
}

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

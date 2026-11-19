package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
)

func TestPromptScenarioPreviewRendersAllStableScenariosReadOnly(t *testing.T) {
	t.Parallel()
	fixture := newPromptReadOnlyFixture(t)
	ctx := context.Background()
	beforeOverrides, err := fixture.database.PromptTemplateOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeFacets, err := fixture.database.TaskFacetCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeStats, err := fixture.database.DatabaseStats(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	beforeRows := databaseRowSnapshot(beforeStats)

	const message = "预览测试消息：请检查这条记录"
	catalog := prompt.ScenarioCatalog()
	if len(catalog) != 10 {
		t.Fatalf("expected 10 stable scenarios, got %d", len(catalog))
	}
	for _, scenario := range catalog {
		if len(scenario.MessageSources) == 0 {
			t.Fatalf("scenario %s declares no message source", scenario.ID)
		}
		for _, source := range scenario.MessageSources {
			query := url.Values{"scenario": {scenario.ID}, "message": {message}, "source": {source.ID}}
			response := requestJSON(t, fixture.client, http.MethodGet,
				fixture.server.URL+"/api/v1/prompts/scenario-preview?"+query.Encode(), nil, "")
			var payload struct {
				OK      bool                   `json:"ok"`
				Preview prompt.ScenarioPreview `json:"preview"`
			}
			decodeResponse(t, response, &payload)
			if response.StatusCode != http.StatusOK || !payload.OK {
				t.Fatalf("scenario %s/%s failed: status=%d payload=%#v", scenario.ID, source.ID, response.StatusCode, payload)
			}
			preview := payload.Preview
			if preview.Scenario != scenario.ID || preview.ScenarioName != scenario.Name || !preview.Demo {
				t.Fatalf("scenario %s lost metadata: %#v", scenario.ID, preview)
			}
			if preview.MessageSource != source.ID || preview.MessageSourceName != source.Name {
				t.Fatalf("scenario %s source=%q/%q want=%q/%q",
					scenario.ID, preview.MessageSource, preview.MessageSourceName, source.ID, source.Name)
			}
			if strings.TrimSpace(preview.Prompt) == "" {
				t.Fatalf("scenario %s returned an empty prompt", scenario.ID)
			}
			if preview.PromptChars != len([]rune(preview.Prompt)) {
				t.Fatalf("scenario %s prompt_chars=%d want=%d", scenario.ID, preview.PromptChars, len([]rune(preview.Prompt)))
			}
			if !strings.Contains(preview.Prompt, "## Current Inbox Batch") || !strings.Contains(preview.Prompt, message) {
				t.Fatalf("scenario %s did not put the test message in Current Inbox Batch", scenario.ID)
			}
			if preview.TestMessage != message || len(preview.ContextFiles) == 0 {
				t.Fatalf("scenario %s returned incomplete preview data: %#v", scenario.ID, preview)
			}
		}
	}

	afterOverrides, err := fixture.database.PromptTemplateOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterFacets, err := fixture.database.TaskFacetCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterStats, err := fixture.database.DatabaseStats(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	afterRows := databaseRowSnapshot(afterStats)
	if !reflect.DeepEqual(beforeOverrides, afterOverrides) {
		t.Fatal("scenario preview mutated prompt overrides")
	}
	if !reflect.DeepEqual(beforeFacets, afterFacets) {
		t.Fatalf("scenario preview changed Task counts:\nbefore=%#v\nafter=%#v", beforeFacets, afterFacets)
	}
	if !reflect.DeepEqual(beforeRows, afterRows) {
		t.Fatalf("scenario preview changed database row counts:\nbefore=%#v\nafter=%#v", beforeRows, afterRows)
	}
}

// databaseRowSnapshot intentionally compares row counts only. Session
// bookkeeping may change row sizes on each authenticated request, but the
// preview contract is that it creates no Task, Turn, channel message, or other
// database row.
func databaseRowSnapshot(stats domain.DatabaseStats) map[string]int64 {
	rows := make(map[string]int64)
	for _, category := range stats.Categories {
		rows["category:"+category.ID] = category.Rows
		for _, table := range category.Tables {
			rows["table:"+table.Name] = table.Rows
		}
	}
	return rows
}

// The group scenarios expose both production sender shapes through the same
// channel_message path. The API must return a distinct source id/name and a
// distinct rendered sender header for each, without creating any new source
// kind.
func TestPromptScenarioPreviewDistinguishesGroupMemberAndBotSources(t *testing.T) {
	t.Parallel()
	fixture := newPromptReadOnlyFixture(t)
	read := func(source string) prompt.ScenarioPreview {
		t.Helper()
		query := url.Values{
			"scenario": {prompt.ScenarioChannelGroupTaskSingle},
			"message":  {"请检查构建结果"},
			"source":   {source},
		}
		response := requestJSON(t, fixture.client, http.MethodGet,
			fixture.server.URL+"/api/v1/prompts/scenario-preview?"+query.Encode(), nil, "")
		var payload struct {
			Preview prompt.ScenarioPreview `json:"preview"`
		}
		decodeResponse(t, response, &payload)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("source %s failed: status=%d", source, response.StatusCode)
		}
		return payload.Preview
	}

	member := read(prompt.ScenarioSourceChannelGroupMember)
	if member.MessageSource != prompt.ScenarioSourceChannelGroupMember || member.MessageSourceName != "渠道群聊成员" {
		t.Fatalf("member source metadata = %q/%q", member.MessageSource, member.MessageSourceName)
	}
	if !strings.Contains(member.Prompt, "Channel sender: 张三 (role=participant, NOT Owner)") {
		t.Fatal("member preview lost the group member sender header")
	}

	bot := read(prompt.ScenarioSourceChannelGroupBot)
	if bot.MessageSource != prompt.ScenarioSourceChannelGroupBot || bot.MessageSourceName != "群聊机器人" {
		t.Fatalf("bot source metadata = %q/%q", bot.MessageSource, bot.MessageSourceName)
	}
	if !strings.Contains(bot.Prompt, "Channel sender: 构建机器人 (role=participant, bot, NOT Owner)") {
		t.Fatal("bot preview lost the group bot sender header")
	}
	if member.Prompt == bot.Prompt {
		t.Fatal("member and bot previews rendered identical prompts")
	}
}

func TestPromptScenarioPreviewReflectsSavedLegacyOverride(t *testing.T) {
	t.Parallel()
	fixture := newPromptReadOnlyFixture(t)
	response := requestJSON(t, fixture.client, http.MethodPut,
		fixture.server.URL+"/api/v1/prompts/templates/role.main", map[string]any{"content": "CUSTOM MAIN SCENARIO"}, fixture.csrf)
	var updated map[string]any
	decodeResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated["ok"] != true {
		t.Fatalf("legacy PUT failed: status=%d payload=%#v", response.StatusCode, updated)
	}

	query := url.Values{"scenario": {prompt.ScenarioWebTaskSingleMain}, "message": {"override check"}}
	response = requestJSON(t, fixture.client, http.MethodGet,
		fixture.server.URL+"/api/v1/prompts/scenario-preview?"+query.Encode(), nil, "")
	var payload struct {
		Preview prompt.ScenarioPreview `json:"preview"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusOK || !strings.Contains(payload.Preview.Prompt, "CUSTOM MAIN SCENARIO") {
		t.Fatalf("saved legacy override is not visible in the scenario preview: status=%d", response.StatusCode)
	}
}

func TestPromptScenarioPreviewRejectsBadInputAndWrites(t *testing.T) {
	t.Parallel()
	fixture := newPromptReadOnlyFixture(t)

	cases := []struct {
		name   string
		query  url.Values
		status int
		code   string
	}{
		{"blank scenario", url.Values{}, http.StatusBadRequest, "invalid_prompt_scenario"},
		{"unknown scenario", url.Values{"scenario": {"nope"}}, http.StatusNotFound, "prompt_scenario_not_found"},
		{"oversized message", url.Values{
			"scenario": {prompt.ScenarioWebTaskSingleMain},
			"message":  {strings.Repeat("长", prompt.MaxScenarioTestMessageRunes+1)},
		}, http.StatusBadRequest, "prompt_preview_message_too_long"},
		{"bad source", url.Values{
			"scenario": {prompt.ScenarioWebTaskSingleMain}, "source": {"system"},
		}, http.StatusBadRequest, "invalid_message_source"},
		{"source from another scenario family", url.Values{
			"scenario": {prompt.ScenarioChannelGroupTaskSingle}, "source": {prompt.ScenarioSourceOwner},
		}, http.StatusBadRequest, "invalid_message_source"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := requestJSON(t, fixture.client, http.MethodGet,
				fixture.server.URL+"/api/v1/prompts/scenario-preview?"+testCase.query.Encode(), nil, "")
			var payload map[string]any
			decodeResponse(t, response, &payload)
			if response.StatusCode != testCase.status || payload["ok"] != false || payload["error"] != testCase.code {
				t.Fatalf("expected %d/%s, got status=%d payload=%#v", testCase.status, testCase.code, response.StatusCode, payload)
			}
			if _, partial := payload["preview"]; partial {
				t.Fatalf("error response leaked a partial preview: %#v", payload)
			}
		})
	}

	response := requestJSON(t, fixture.client, http.MethodPost,
		fixture.server.URL+"/api/v1/prompts/scenario-preview", map[string]any{"scenario": prompt.ScenarioWebTaskSingleMain}, fixture.csrf)
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("scenario preview accepted a write method: %d", response.StatusCode)
	}
	if allow := response.Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Fatalf("read-only rejection did not advertise GET: %q", allow)
	}
	response.Body.Close()

	anonymous := &http.Client{}
	query := url.Values{"scenario": {prompt.ScenarioWebTaskSingleMain}}
	response = requestJSON(t, anonymous, http.MethodGet,
		fixture.server.URL+"/api/v1/prompts/scenario-preview?"+query.Encode(), nil, "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("scenario preview is not behind session auth: %d", response.StatusCode)
	}
	response.Body.Close()
}

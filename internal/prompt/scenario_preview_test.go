package prompt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestScenarioPreviewUsesProductionBuilderForEveryStableShape(t *testing.T) {
	t.Parallel()
	repository := &fixturePromptRepository{}
	engine := NewEngine(repository)
	ctx := context.Background()
	message := "这是一条测试消息"

	catalog := ScenarioCatalog()
	if len(catalog) != 10 {
		t.Fatalf("expected 10 stable scenarios, got %d", len(catalog))
	}
	seen := map[string]bool{}
	for _, spec := range catalog {
		if seen[spec.ID] {
			t.Fatalf("duplicate scenario ID %s", spec.ID)
		}
		seen[spec.ID] = true
		if len(spec.MessageSources) == 0 {
			t.Fatalf("scenario %s declares no message source", spec.ID)
		}
		shape, ok := scenarioShapeFor(spec.ID)
		if !ok {
			t.Fatalf("scenario %s has no shape", spec.ID)
		}
		for _, source := range spec.MessageSources {
			preview, err := engine.PreviewScenario(ctx, spec.ID, message, source.ID, domain.LanguageEnglish)
			if err != nil {
				t.Fatalf("preview %s/%s: %v", spec.ID, source.ID, err)
			}
			if !preview.Demo || preview.Scenario != spec.ID || preview.ScenarioName != spec.Name {
				t.Fatalf("preview %s lost metadata: %#v", spec.ID, preview)
			}
			if preview.MessageSource != source.ID || preview.MessageSourceName != source.Name {
				t.Fatalf("preview %s source=%q/%q want=%q/%q",
					spec.ID, preview.MessageSource, preview.MessageSourceName, source.ID, source.Name)
			}
			if strings.TrimSpace(preview.Prompt) == "" {
				t.Fatalf("preview %s/%s produced an empty prompt", spec.ID, source.ID)
			}
			if preview.PromptChars != len([]rune(preview.Prompt)) {
				t.Fatalf("preview %s prompt_chars=%d want=%d", spec.ID, preview.PromptChars, len([]rune(preview.Prompt)))
			}
			if !strings.Contains(preview.Prompt, message) {
				t.Fatalf("preview %s does not contain the test message", spec.ID)
			}
			if len(preview.ContextFiles) == 0 {
				t.Fatalf("preview %s produced no context file summary", spec.ID)
			}

			// The preview must be byte-identical to calling the real builder with
			// the same synthetic input; this is the strongest guard against the
			// preview growing its own rendering path.
			input, effectiveSource, err := engine.scenarioBuildInput(ctx, shape, message, source.ID, domain.LanguageEnglish)
			if err != nil {
				t.Fatalf("build input %s/%s: %v", spec.ID, source.ID, err)
			}
			result, err := engine.Build(ctx, input)
			if err != nil {
				t.Fatalf("build %s/%s: %v", spec.ID, source.ID, err)
			}
			if preview.Prompt != result.EffectivePrompt {
				t.Fatalf("scenario %s/%s preview diverged from Build", spec.ID, source.ID)
			}
			if preview.MessageSource != effectiveSource {
				t.Fatalf("scenario %s source=%q want=%q", spec.ID, preview.MessageSource, effectiveSource)
			}
		}
	}
	if repository.upserts != 0 || repository.deletes != 0 {
		t.Fatalf("scenario preview wrote to the repository: upserts=%d deletes=%d", repository.upserts, repository.deletes)
	}
}

func TestScenarioPreviewSelectsTheExpectedIdentityAndPolicy(t *testing.T) {
	t.Parallel()
	engine := NewEngine(&fixturePromptRepository{})
	ctx := context.Background()
	message := "这是一条测试消息"
	render := func(id string) string {
		t.Helper()
		spec, ok := scenarioSpecByID(id)
		if !ok {
			t.Fatalf("unknown scenario %s", id)
		}
		preview, err := engine.PreviewScenario(ctx, id, message, spec.DefaultMessageSource().ID, domain.LanguageEnglish)
		if err != nil {
			t.Fatalf("preview %s: %v", id, err)
		}
		return preview.Prompt
	}

	single := render(ScenarioWebTaskSingleMain)
	if !strings.Contains(single, "## Current Inbox Batch") || !strings.Contains(single, message) {
		t.Fatal("web single main prompt lost the Current Inbox Batch")
	}
	if !strings.Contains(single, "single-agent mode") {
		t.Fatal("web single main prompt lost the single-agent policy")
	}
	if !strings.Contains(single, "Channel: AHA Web") {
		t.Fatal("web single main prompt lost the Web channel template")
	}

	multi := render(ScenarioWebTaskMultiMain)
	if !strings.Contains(multi, "auto collaboration with at most 3 total Agents") {
		t.Fatal("web multi main prompt lost the auto collaboration policy")
	}

	sub := render(ScenarioWebTaskMultiSub)
	if !strings.Contains(sub, "You are sub-001") {
		t.Fatal("web multi sub prompt lost the sub identity")
	}
	if strings.Contains(sub, "auto collaboration with at most") {
		t.Fatal("sub prompt must not include the collaboration policy section")
	}

	pet := render(ScenarioWebPetTaskSingleMain)
	if !strings.Contains(pet, "You are AHA2's pet") {
		t.Fatal("pet prompt lost the pet identity template")
	}
	if !strings.Contains(pet, "Core digest (core_version demo)") {
		t.Fatal("pet prompt lost the demo core digest supplement")
	}

	privateAssistant := render(ScenarioChannelPrivateUnboundAssistant)
	if !strings.Contains(privateAssistant, "private AHA channel assistant") {
		t.Fatal("unbound private channel lost the assistant identity")
	}
	if strings.Contains(privateAssistant, "Identity: AHA Task Agent.") {
		t.Fatal("unbound private channel must not use the task agent identity")
	}

	privateTask := render(ScenarioChannelPrivateTaskSingle)
	if !strings.Contains(privateTask, "Identity: AHA Task Agent.") {
		t.Fatal("task_route private channel lost the task agent identity")
	}
	if !strings.Contains(privateTask, "You are communicating through an external channel") {
		t.Fatal("task_route private channel lost the external channel template")
	}

	groupUnbound := render(ScenarioChannelGroupUnboundDigital)
	if !strings.Contains(groupUnbound, "public group digital human") {
		t.Fatal("unbound group lost the digital human identity")
	}
	if !strings.Contains(groupUnbound, "## Group request intent") {
		t.Fatal("unbound group lost the group rules")
	}
	if strings.Contains(groupUnbound, "Owner permission rules for group task routes") {
		t.Fatal("unbound group must not materialize task-route group permissions")
	}

	groupTask := render(ScenarioChannelGroupTaskSingle)
	if !strings.Contains(groupTask, "Owner permission rules for group task routes") {
		t.Fatal("task-routed group lost the group permissions resource")
	}
}

// The production renderer selects protocol.knowledge whenever knowledge is
// enabled. This assertion builds the expected static section from the
// registered production template, independently of scenarioBuildInput, so a
// demo input that silently drops KnowledgeEnabled cannot hide the regression.
func TestScenarioPreviewIncludesProductionKnowledgeProtocol(t *testing.T) {
	t.Parallel()
	engine := NewEngine(&fixturePromptRepository{})
	ctx := context.Background()
	templates, err := engine.TemplatesForLanguage(ctx, domain.LanguageEnglish)
	if err != nil {
		t.Fatal(err)
	}
	var knowledge domain.PromptTemplate
	for _, item := range templates {
		if item.ID == "protocol.knowledge" {
			knowledge = item
			break
		}
	}
	if knowledge.ID == "" {
		t.Fatal("protocol.knowledge is not registered")
	}
	rendered, err := renderTemplate(knowledge.ID, knowledge.Content, sampleTemplateData())
	if err != nil {
		t.Fatal(err)
	}
	expected := "## " + knowledge.Name + "\n" + rendered

	for _, spec := range ScenarioCatalog() {
		preview, err := engine.PreviewScenario(ctx, spec.ID, "", "", domain.LanguageEnglish)
		if err != nil {
			t.Fatalf("preview %s: %v", spec.ID, err)
		}
		if !strings.Contains(preview.Prompt, expected) {
			t.Fatalf("scenario %s is missing the production Knowledge Protocol section", spec.ID)
		}
	}
}

func TestScenarioPreviewMessageSourceSwitch(t *testing.T) {
	t.Parallel()
	engine := NewEngine(&fixturePromptRepository{})
	ctx := context.Background()

	ownerPreview, err := engine.PreviewScenario(ctx, ScenarioWebTaskSingleMain, "请检查日志", "owner", domain.LanguageEnglish)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ownerPreview.Prompt, "[owner_message from owner]") {
		t.Fatal("owner preview did not render the owner inbox source")
	}
	if ownerPreview.MessageSource != ScenarioSourceOwner || ownerPreview.MessageSourceName != "Owner" {
		t.Fatalf("owner source metadata = %q/%q", ownerPreview.MessageSource, ownerPreview.MessageSourceName)
	}
	petPreview, err := engine.PreviewScenario(ctx, ScenarioWebTaskSingleMain, "请检查日志", "pet", domain.LanguageEnglish)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(petPreview.Prompt, "[pet_delegate_message from pet]") {
		t.Fatal("pet preview did not render the pet inbox source")
	}
	if petPreview.MessageSource != ScenarioSourcePet || petPreview.MessageSourceName != "宠物" {
		t.Fatalf("pet source metadata = %q/%q", petPreview.MessageSource, petPreview.MessageSourceName)
	}

	// A Web Task has no group conversation, so group sources are rejected
	// instead of being relabeled into an owner message.
	if _, err := engine.PreviewScenario(ctx, ScenarioWebTaskSingleMain, "请检查日志", ScenarioSourceChannelGroupBot, domain.LanguageEnglish); !errors.Is(err, ErrInvalidMessageSource) {
		t.Fatalf("web scenario accepted a group bot source: %v", err)
	}
}

// The group scenarios are the stable full Snapshot for both sender shapes. The
// inbox header and channel-context.json must differ by actor role/bot flag, not
// just by a display label.
func TestScenarioPreviewGroupMemberAndBotSources(t *testing.T) {
	t.Parallel()
	engine := NewEngine(&fixturePromptRepository{})
	ctx := context.Background()
	message := "请检查构建结果"

	for _, id := range []string{
		ScenarioChannelGroupUnboundDigital,
		ScenarioChannelGroupTaskSingle,
		ScenarioChannelGroupTaskMulti,
	} {
		t.Run(id, func(t *testing.T) {
			member, err := engine.PreviewScenario(ctx, id, message, ScenarioSourceChannelGroupMember, domain.LanguageEnglish)
			if err != nil {
				t.Fatalf("member preview %s: %v", id, err)
			}
			bot, err := engine.PreviewScenario(ctx, id, message, ScenarioSourceChannelGroupBot, domain.LanguageEnglish)
			if err != nil {
				t.Fatalf("bot preview %s: %v", id, err)
			}
			if member.MessageSource != ScenarioSourceChannelGroupMember || member.MessageSourceName != "渠道群聊成员" {
				t.Fatalf("member source metadata = %q/%q", member.MessageSource, member.MessageSourceName)
			}
			if bot.MessageSource != ScenarioSourceChannelGroupBot || bot.MessageSourceName != "群聊机器人" {
				t.Fatalf("bot source metadata = %q/%q", bot.MessageSource, bot.MessageSourceName)
			}
			if !strings.Contains(member.Prompt, "Channel sender: 张三 (role=participant, NOT Owner)") {
				t.Fatalf("member preview %s lost the member sender header", id)
			}
			if !strings.Contains(bot.Prompt, "Channel sender: 构建机器人 (role=participant, bot, NOT Owner)") {
				t.Fatalf("bot preview %s lost the bot sender header", id)
			}
			if !strings.Contains(member.Prompt, "## Main Agent") || !strings.Contains(bot.Prompt, "## Main Agent") {
				t.Fatalf("group preview %s lost the role section", id)
			}
			if !strings.Contains(member.Prompt, "## Group request intent") || !strings.Contains(bot.Prompt, "## Group request intent") {
				t.Fatalf("group preview %s lost the group rules", id)
			}
			if member.Prompt == bot.Prompt {
				t.Fatalf("member and bot preview %s rendered identical prompts", id)
			}

			memberContext := channelContextForTest(t, engine, id, message, ScenarioSourceChannelGroupMember)
			botContext := channelContextForTest(t, engine, id, message, ScenarioSourceChannelGroupBot)
			if !strings.Contains(memberContext, `"is_bot": false`) || strings.Contains(memberContext, `"bot_dialogue"`) {
				t.Fatalf("member channel-context is not a plain group member: %s", memberContext)
			}
			if !strings.Contains(botContext, `"is_bot": true`) || !strings.Contains(botContext, `"bot_dialogue"`) {
				t.Fatalf("bot channel-context lost the bot provenance: %s", botContext)
			}
			if !strings.Contains(botContext, `"active": true`) {
				t.Fatalf("bot channel-context lost the active bot dialogue: %s", botContext)
			}
		})
	}
}

func channelContextForTest(t *testing.T, engine *Engine, scenarioID, message, source string) string {
	t.Helper()
	shape, ok := scenarioShapeFor(scenarioID)
	if !ok {
		t.Fatalf("scenario %s has no shape", scenarioID)
	}
	input, _, err := engine.scenarioBuildInput(context.Background(), shape, message, source, domain.LanguageEnglish)
	if err != nil {
		t.Fatalf("build input %s: %v", scenarioID, err)
	}
	result, err := engine.Build(context.Background(), input)
	if err != nil {
		t.Fatalf("build %s: %v", scenarioID, err)
	}
	for _, item := range result.ContextManifest {
		if item.ID == "channel-context" {
			return item.Content
		}
	}
	t.Fatalf("scenario %s did not materialize channel-context.json", scenarioID)
	return ""
}

func TestScenarioPreviewReflectsLegacyIDOverride(t *testing.T) {
	t.Parallel()
	repository := &fixturePromptRepository{overrides: map[string]domain.PromptTemplate{
		"role.main": {ID: "role.main", Content: "CUSTOM MAIN PREVIEW BODY", Version: 7},
	}}
	engine := NewEngine(repository)
	preview, err := engine.PreviewScenario(context.Background(), ScenarioWebTaskSingleMain, "", "", domain.LanguageEnglish)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Prompt, "CUSTOM MAIN PREVIEW BODY") {
		t.Fatal("legacy role.main override is not reflected in the scenario preview")
	}
	shape, _ := scenarioShapeFor(ScenarioWebTaskSingleMain)
	input, _, err := engine.scenarioBuildInput(context.Background(), shape, DefaultScenarioTestMessage, "owner", domain.LanguageEnglish)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.EffectivePrompt, "CUSTOM MAIN PREVIEW BODY") {
		t.Fatal("legacy override is not reflected in the real builder output")
	}
}

func TestScenarioPreviewRejectsUnknownOversizedAndBadSource(t *testing.T) {
	t.Parallel()
	engine := NewEngine(&fixturePromptRepository{})
	ctx := context.Background()

	if _, err := engine.PreviewScenario(ctx, "does-not-exist", "", "", ""); !errors.Is(err, ErrUnknownScenario) {
		t.Fatalf("unknown scenario error = %v", err)
	}
	overlong := strings.Repeat("长", MaxScenarioTestMessageRunes+1)
	if _, err := engine.PreviewScenario(ctx, ScenarioWebTaskSingleMain, overlong, "", ""); !errors.Is(err, ErrScenarioMessageTooLong) {
		t.Fatalf("overlong message error = %v", err)
	}
	if _, err := engine.PreviewScenario(ctx, ScenarioWebTaskSingleMain, "ok", "system", ""); !errors.Is(err, ErrInvalidMessageSource) {
		t.Fatalf("invalid source error = %v", err)
	}
	if _, err := engine.PreviewScenario(ctx, ScenarioChannelGroupTaskSingle, "ok", ScenarioSourceOwner, ""); !errors.Is(err, ErrInvalidMessageSource) {
		t.Fatalf("group scenario accepted an owner source: %v", err)
	}
}

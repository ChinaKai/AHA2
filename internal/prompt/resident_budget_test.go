package prompt

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every layer below is rendered in full on each Turn that selects it, and holds
// rules that must stay in view: permission, identity, intent, and delivery
// boundaries whose violation is a boundary breach. Everything else belongs in a
// resource the Agent reads on demand.
//
// This guardrail is deliberately a ceiling, not a target. It exists so that a
// future template edit has to argue with a number instead of quietly growing
// the per-Turn cost again — the same drift that produced the current baseline.
// Limits sit just above the current size, so the guardrail fails on the next
// material addition rather than on formatting noise.
//
// Coverage is the point: an unlisted layer is an unguarded one, and the
// largest layer in the system was unlisted until it was added here. Adding a
// template should mean adding it to this map in the same change.
var residentPromptBudget = map[string]int{
	"core-default":                 750,
	"identity-task-agent":          220,
	"role-main":                    800,
	"channel-web":                  250,
	"protocol-agent-api":           850,
	"protocol-attachment-delivery": 500,
	// Selected by scenario rather than always, but each is the largest block in
	// its scenario, so an edit here costs more per Turn than one in the always-on
	// layers above.
	"channel-external-channel":       3700,
	"protocol-knowledge":             1300,
	"policy-auto":                    450,
	"identity-channel-digital-human": 1250,
	"identity-channel-assistant":     850,
	"role-sub":                       700,
	"policy-single":                  150,
}

// The Available context list is the other per-Turn block, and it is rendered
// from live paths rather than from a fixed template, so growing it is invisible
// to the per-layer budget above. It was 1,465 runes, of which 924 were the same
// shared-snapshot prefix repeated on six consecutive lines; the roots are now
// printed once and the entries are relative. This ceiling keeps the next entry
// that carries a full path from re-inflating it.
var availableContextBudget = 1100

func TestAvailableContextBlockStaysWithinBudget(t *testing.T) {
	t.Parallel()
	data, err := templateFiles.ReadFile("templates/section-available-context.md")
	if err != nil {
		t.Fatal(err)
	}
	root := "/home/agent/.aha2-context/task-example"
	shared := root + "/shared-" + strings.Repeat("a", 64)
	entries := []ContextResource{
		{Path: filepath.Join(root, "main", "task.md"), Description: "task.md 的内容模板", Chars: 813},
		{Path: filepath.Join(shared, "agent-api.md"), Description: "agent-api.md 的只读参考模板", Chars: 12964},
		{Path: filepath.Join(shared, "attachment-protocol.md"), Description: "attachment-protocol.md 的只读参考模板；附件上传、绑定与回执规程", Chars: 1561},
		{Path: filepath.Join(shared, "knowledge", "global", "index.md"), Description: "全局知识 entrypoint", Chars: 290},
		{Path: filepath.Join(shared, "knowledge", "global", "agent-lessons.md"), Description: "Agent 经验教训优先索引；按任务相关性继续读取技术诊断或行为教训", Chars: 979},
		{Path: filepath.Join(shared, "knowledge", "project", "index.md"), Description: "项目知识 entrypoint", Chars: 13615},
		{Path: filepath.Join(shared, "knowledge", "project", "navigation", "index.md"), Description: "项目导航 entrypoint", Chars: 326},
	}
	view := templateData{AvailableContext: availableContextEntries(entryPointResources(entries))}
	if len(view.AvailableContext) != 2 {
		t.Fatalf("available context has %d groups, want the shared snapshot grouped and the lone task.md left absolute", len(view.AvailableContext))
	}
	rendered, err := renderTemplate("section.available-context", string(data), view)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range view.AvailableContext {
		for _, entry := range group.Entries {
			// Every relative path must resolve back to the file it names; an
			// entry outside a printed root keeps its absolute path.
			resolved := entry.Path
			if group.Root != "" {
				if filepath.IsAbs(entry.Path) {
					t.Fatalf("available context entry kept an absolute path under a printed root: %s", entry.Path)
				}
				resolved = group.Root + "/" + entry.Path
			}
			if !strings.HasSuffix(resolved, filepath.Base(entry.Path)) {
				t.Fatalf("available context entry %q does not resolve under %q", entry.Path, group.Root)
			}
		}
	}
	// The task.md entry is its own group and must have kept the full path.
	if view.AvailableContext[0].Root != "" {
		t.Fatalf("lone task.md was grouped under %q instead of staying absolute", view.AvailableContext[0].Root)
	}
	// The whole point is that the hash-bearing prefix appears once, not per line.
	if count := strings.Count(rendered, filepath.Base(shared)); count != 1 {
		t.Fatalf("shared snapshot name appears %d times in the context block, want 1:\n%s", count, rendered)
	}
	if got := len([]rune(rendered)); got > availableContextBudget {
		t.Errorf("available context block grew to %d runes (budget %d); make the new entry relative to the printed root instead of carrying a full path", got, availableContextBudget)
	}
}

// entryPointResources presents the paths as the build does, so the guard above
// exercises the same selection and rewriting the real prompt goes through.
func entryPointResources(entries []ContextResource) []ContextResource {
	resources := make([]ContextResource, 0, len(entries))
	for _, entry := range entries {
		entry.EntryPoint = true
		resources = append(resources, entry)
	}
	return resources
}

// The budget map is only as good as its coverage, and coverage maintained by
// hand is how the largest layer in the system stayed unguarded across several
// reviews. This enumerates the resident set from the same helper the renderer
// uses, over every shape a Turn can take, and fails when a resident template has
// no budget entry — so promoting a template to resident requires deciding its
// ceiling in the same change.
func TestEveryResidentTemplateHasABudget(t *testing.T) {
	t.Parallel()
	templateIDs := append([]string(nil),
		residentSectionTemplateIDs...)
	templateIDs = append(templateIDs, residentProtocolTemplateIDs...)
	templateIDs = append(templateIDs, residentConditionalTemplateIDs...)
	for _, role := range []string{"main", "sub"} {
		for _, mode := range []string{"auto", "single"} {
			for _, knowledge := range []bool{true, false} {
				for _, identity := range []string{identityTaskAgent, identityChannelAssistant, identityChannelDigitalHuman} {
					for _, channel := range []string{channelWeb, channelExternal} {
						// The renderer concatenates the "identity."/"channel."
						// prefix at the call site, so pass the same shape it does.
						templateIDs = append(templateIDs,
							residentTemplateIDs(role, mode, knowledge, "identity."+identity, "channel."+channel)...)
					}
				}
			}
		}
	}
	checked := map[string]bool{}
	for _, id := range templateIDs {
		if checked[id] {
			continue
		}
		checked[id] = true
		// The section templates interpolate the runtime payload (paths,
		// workspace, inbox) and the compact handoff is free-form, so none of them
		// is fixed text to put a ceiling on. The Available context block has its
		// own guard above, which measures it as rendered.
		if strings.HasPrefix(id, "section.") {
			continue
		}
		file := templateFileForID(id)
		if _, ok := residentPromptBudget[file]; !ok {
			t.Errorf("resident template %s has no budget entry; add one under %q so it cannot grow unguarded", id, file)
		}
	}
}

// templateFileForID maps a template ID to its embedded file name, which is the
// key the budget map uses.
func templateFileForID(id string) string {
	return strings.ReplaceAll(id, ".", "-")
}

// conditionalMarker matches the {{if ...}} form of a template action. Only the
// opening form is stripped, because {{end}} is a fixed literal; the wrapped text
// itself stays, since a budget must cover the largest render, not the smallest.
var conditionalMarker = regexp.MustCompile(`\{\{if[^}]*\}\}`)

// residentTemplateRunes reports the worst-case rendered size of a resident
// template: the conditional branches counted as if they all applied, and the
// template syntax itself excluded because it never reaches the prompt.
//
// Measuring the raw file would count the ~30-40 runes of {{if}}/{{end}} syntax
// for the templates that have conditionals, so the ceiling would sit partly on
// text no Turn ever sees. A rune count, not bytes: these templates mix English
// and Chinese, and bytes would penalise the Chinese text unfairly.
func residentTemplateRunes(content string) int {
	worstCase := conditionalMarker.ReplaceAllString(content, "")
	worstCase = strings.ReplaceAll(worstCase, "{{end}}", "")
	return len([]rune(strings.TrimSpace(worstCase)))
}

func TestResidentPromptLayersStayWithinBudget(t *testing.T) {
	for id, limit := range residentPromptBudget {
		id, limit := id, limit
		t.Run(id, func(t *testing.T) {
			data, err := templateFiles.ReadFile("templates/" + id + ".md")
			if err != nil {
				t.Fatalf("resident template %s is missing: %v", id, err)
			}
			if got := residentTemplateRunes(string(data)); got > limit {
				t.Errorf("resident template %s grew to %d runes (budget %d); move the new detail into an on-demand resource instead of the per-Turn prompt", id, got, limit)
			}
		})
	}
}

// The Knowledge Protocol used to hand-write relative paths like
// knowledge/project/index.md, which do not exist under the Task workdir: the
// real files live in a per-snapshot directory whose name carries a content
// hash. The protocol only appeared to work because the Agent cross-referenced
// the Available context list. Keep it referencing that list.
func TestKnowledgeProtocolDoesNotHardcodeContextPaths(t *testing.T) {
	data, err := templateFiles.ReadFile("templates/protocol-knowledge.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "Available context") {
		t.Error("knowledge protocol no longer tells the Agent to read the Available context list")
	}
	for _, dead := range []string{
		"knowledge/project/navigation/index.md",
		"knowledge/project/index.md",
		"knowledge/global/agent-lessons.md",
		"knowledge/pending-updates/index.md",
	} {
		if strings.Contains(body, dead) {
			t.Errorf("knowledge protocol hardcodes %q, which does not exist under the Task workdir", dead)
		}
	}
}

// The factoring runs on whichever path shape the workspace transport produces:
// filepath.Join on Windows yields D:\..., and a task whose workdir is reached
// over the WSL share yields \\wsl.localhost\... The UNC shape splits into a
// different number of leading empty segments than an absolute path does, which
// is enough to break both the root matching and the tree itself, so each shape
// is checked end to end rather than only the one this repository happens to run
// under. The UNC case is the one that was broken: no factoring happened at all,
// and the tree builder put a node's directory equal to its parent's.
func TestAvailableContextPathsResolveForEveryTransportShape(t *testing.T) {
	t.Parallel()
	shapes := map[string]struct {
		main, shared string
	}{
		"posix": {
			main:   "/home/agent/.aha2-context/task-x/main",
			shared: "/home/agent/.aha2-context/task-x/shared-abc",
		},
		"windows-drive": {
			main:   `D:\Program Files\AHA2\.aha2-context\task-x\main`,
			shared: `D:\Program Files\AHA2\.aha2-context\task-x\shared-abc`,
		},
		"unc-share": {
			main:   `\\wsl.localhost\Ubuntu\home\k\repo\.aha2-context\task-x\main`,
			shared: `\\wsl.localhost\Ubuntu\home\k\repo\.aha2-context\task-x\shared-abc`,
		},
		// joinRemoteContextPath rewrites a Windows workspace root to a UNC path
		// and deliberately keeps the doubled leading separator, and it keeps
		// forward slashes. This is the shape a remote Task actually renders.
		"wsl-share": {
			main:   "//wsl.localhost/Ubuntu/home/k/repo/.aha2-context/task-x/main",
			shared: "//wsl.localhost/Ubuntu/home/k/repo/.aha2-context/task-x/shared-abc",
		},
		"wsl-admin-share": {
			main:   "//wsl$/Ubuntu/home/k/repo/.aha2-context/task-x/main",
			shared: "//wsl$/Ubuntu/home/k/repo/.aha2-context/task-x/shared-abc",
		},
	}
	for name, shape := range shapes {
		shape := shape
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			entries := []ContextResource{
				{Path: shape.main + separatorFor(shape.main) + "task.md", Description: "内容模板", EntryPoint: true},
				{Path: shape.shared + separatorFor(shape.shared) + "agent-api.md", Description: "只读参考模板", EntryPoint: true},
				{Path: shape.shared + separatorFor(shape.shared) + "attachment-protocol.md", Description: "只读参考模板；附件上传、绑定与回执规程", EntryPoint: true},
				{Path: shape.shared + separatorFor(shape.shared) + "knowledge/global/index.md", Description: "全局知识 entrypoint", EntryPoint: true},
			}
			groups := availableContextEntries(entries)
			grouped := 0
			for _, group := range groups {
				if group.Root == "" {
					continue
				}
				grouped += len(group.Entries)
				for _, entry := range group.Entries {
					original := pathForEntry(entries, entry)
					// The root and the relative path must reconstitute the
					// original exactly, separators aside. Comparing only a
					// suffix would accept a root that lost its leading
					// separator, which is precisely the defect this case was
					// added for, so the whole string is compared.
					resolved := strings.ReplaceAll(group.Root+"/"+entry.Path, `\`, "/")
					if resolved != canonicalContextPath(original) {
						t.Fatalf("entry %q under root %q resolves to %q, not %q", entry.Path, group.Root, resolved, original)
					}
				}
			}
			// The shared directory is carried by three of the four entries, so
			// it must be factored; leaving it absolute means the factoring
			// silently did nothing, which is how the UNC shape failed.
			if grouped < 3 {
				t.Fatalf("only %d of %d entries were factored, so a shared prefix is still repeated:\n%#v", grouped, len(entries), groups)
			}
		})
	}
}

func separatorFor(path string) string {
	if strings.Contains(path, `\`) {
		return `\`
	}
	return "/"
}

func pathForEntry(entries []ContextResource, want ContextResource) string {
	for _, entry := range entries {
		if entry.ID == want.ID && entry.Description == want.Description {
			return entry.Path
		}
	}
	return ""
}

// canonicalContextPath puts an expected path in the shape the renderer emits:
// forward slashes, and a network share with both of its leading separators,
// which is what joinRemoteContextPath preserves.
func canonicalContextPath(path string) string {
	normalized := strings.ReplaceAll(path, `\`, "/")
	if strings.HasPrefix(normalized, "//") {
		return normalized
	}
	return normalized
}

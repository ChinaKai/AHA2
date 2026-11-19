package app

import (
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const petCrossSkillProjectID = "project_bu2_cam1"

func petCrossSkillFiles() map[string]string {
	files := vaultSkillFiles()
	files["projects/bu2-cam1/index.md"] = "---\nid: knowledge_root_project_bu2_cam1\ntitle: BU2_CAM1\nscope: project\nproject_id: " + petCrossSkillProjectID + "\ntype: navigation\nstatus: verified\nis_index: true\n---\nBU2_CAM1 entry.\n"
	files["projects/bu2-cam1/skills/bulbcam-bug-intake/SKILL.md"] = "---\nname: Bulbcam Bug Intake\ndescription: Camera bug intake workflow\nversion: 4\n---\n\n# Bulbcam Bug Intake\n\nCollect the bug report first.\n"
	files["projects/bu2-cam1/skills/disabled-score/SKILL.md"] = "---\nname: Disabled Score\ndescription: disabled skill\nenabled: false\n---\n\ndisabled\n"
	files["projects/pet/skills/pet-own/SKILL.md"] = "---\nname: Pet Own\ndescription: pet project skill\n---\n\npet own\n"
	return files
}

type petCrossSkillFixture struct {
	vaultSkillsFixture
	bu2SkillKey string
	disabledKey string
	globalKey   string
	petOwnKey   string
}

func newPetCrossSkillFixture(t *testing.T) petCrossSkillFixture {
	t.Helper()
	withUnsetEnv(t, knowledgeVaultReadProjectsEnv)
	fixture := newVaultSkillsFixtureForProject(t, buildVaultReadManager(t, true, petCrossSkillFiles()), domain.Project{
		ID: petProjectID, Name: "宠物", ProjectType: "system", KnowledgePolicy: "none",
	})
	now := time.Now().UTC()
	if err := fixture.database.CreateProject(fixture.ctx, domain.Project{ID: petCrossSkillProjectID, Name: "BU2_CAM1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return petCrossSkillFixture{
		vaultSkillsFixture: fixture,
		bu2SkillKey:        "vault-skill:project:" + petCrossSkillProjectID + ":bulbcam-bug-intake",
		disabledKey:        "vault-skill:project:" + petCrossSkillProjectID + ":disabled-score",
		globalKey:          "vault-skill:global:demo",
		petOwnKey:          "vault-skill:project:" + petProjectID + ":pet-own",
	}
}

func (f petCrossSkillFixture) createTask(t *testing.T, taskID string, projectID, workspaceID string, skillIDs []string) domain.Task {
	t.Helper()
	task, err := f.service.CreateTask(f.ctx, CreateTaskInput{
		ID: taskID, ProjectID: projectID, WorkspaceID: workspaceID,
		Title: "cross project skills", Request: "cross project skills",
		SkillIDs: skillIDs, ModelID: "model-vault-skills",
		StartMode:          "manual",
		AllowSystemProject: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// The pet Task may select a project Skill it does not own. The selection is
// stored, read back, and materialized for the next Turn with its real version.
func TestPetTaskAcceptsAndLoadsCrossProjectSkills(t *testing.T) {
	fixture := newPetCrossSkillFixture(t)
	task := fixture.createTask(t, petTaskID, petProjectID, fixture.workspace.ID, []string{fixture.globalKey, fixture.bu2SkillKey})
	want := []string{fixture.globalKey, fixture.bu2SkillKey}
	if !hasTaskSkill(task, fixture.bu2SkillKey) || len(task.SkillIDs) != len(want) {
		t.Fatalf("pet task skill_ids=%#v want %#v", task.SkillIDs, want)
	}

	// A later update replaces the selection and is read back from the store.
	updated, err := fixture.service.UpdateTaskSkills(fixture.ctx, task.ID, []string{fixture.bu2SkillKey})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.SkillIDs) != 1 || updated.SkillIDs[0] != fixture.bu2SkillKey {
		t.Fatalf("updated pet skill_ids=%#v", updated.SkillIDs)
	}
	stored, err := fixture.database.Task(fixture.ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.SkillIDs) != 1 || stored.SkillIDs[0] != fixture.bu2SkillKey {
		t.Fatalf("stored pet skill_ids=%#v", stored.SkillIDs)
	}

	active := fixture.service.activeTaskSkills(fixture.ctx, stored)
	if len(active) != 1 {
		t.Fatalf("active pet skills=%#v want the BU2_CAM1 skill", active)
	}
	skill := active[0]
	if skill.ID != fixture.bu2SkillKey || skill.ProjectID != petCrossSkillProjectID ||
		skill.Name != "Bulbcam Bug Intake" || skill.Version != 4 || skill.Status != "active" || !skill.Enabled {
		t.Fatalf("active cross-project skill=%#v", skill)
	}
	if len(skill.PackageFiles) != 1 || skill.PackageFiles[0].Path != "SKILL.md" ||
		!strings.Contains(skill.PackageFiles[0].Content, "Collect the bug report first") {
		t.Fatalf("cross-project package files=%#v", skill.PackageFiles)
	}
}

// The pet catalog is the system administrator view: every project's skill is
// listed with its project identity, version and status, while a normal project
// keeps its existing single-project visibility.
func TestVaultSkillCatalogForPetListsAllProjects(t *testing.T) {
	fixture := newPetCrossSkillFixture(t)
	catalog, err := fixture.service.VaultSkillCatalog(fixture.ctx, petProjectID)
	if err != nil {
		t.Fatal(err)
	}
	var bu2, disabled *VaultSkillView
	for index := range catalog.Skills {
		view := &catalog.Skills[index]
		switch view.Key {
		case fixture.bu2SkillKey:
			bu2 = view
		case fixture.disabledKey:
			disabled = view
		}
	}
	if bu2 == nil {
		t.Fatalf("pet catalog did not list the BU2_CAM1 skill: %#v", catalog.Skills)
	}
	if bu2.Scope != "project" || bu2.ProjectID != petCrossSkillProjectID ||
		bu2.ProjectName != "BU2_CAM1" || bu2.Name != "Bulbcam Bug Intake" ||
		bu2.Version != 4 || bu2.Status != "active" || !bu2.Enabled {
		t.Fatalf("pet catalog skill fields=%#v", bu2)
	}
	if bu2.UnavailableReason == "project_not_bound" {
		t.Fatalf("pet catalog marked a cross-project skill as unbound: %#v", bu2)
	}
	if disabled == nil || disabled.Enabled {
		t.Fatalf("pet catalog disabled skill=%#v", disabled)
	}

	// A normal project still sees only its own project skills (plus globals).
	normalCatalog, err := fixture.service.VaultSkillCatalog(fixture.ctx, petCrossSkillProjectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range normalCatalog.Skills {
		if view.Key == fixture.petOwnKey {
			t.Fatalf("normal project catalog leaked another project's skill: %#v", view)
		}
	}
	if view := findVaultSkill(normalCatalog, fixture.bu2SkillKey); view == nil || view.ProjectName != "BU2_CAM1" {
		t.Fatalf("normal project catalog lost its own skill: %#v", normalCatalog.Skills)
	}
}

// A Task that is not task_pet keeps the existing rule even when it lives in the
// same system project, so the pet special case cannot be used to widen scope.
func TestNonPetTaskKeepsProjectSkillScope(t *testing.T) {
	fixture := newPetCrossSkillFixture(t)
	other := fixture.createTask(t, "task_pet_peer", petProjectID, fixture.workspace.ID, []string{fixture.globalKey})
	if _, err := fixture.service.UpdateTaskSkills(fixture.ctx, other.ID, []string{fixture.bu2SkillKey}); err == nil || !strings.Contains(err.Error(), fixture.bu2SkillKey) {
		t.Fatalf("non-pet system task accepted a cross-project skill: err=%v", err)
	}
	stored, err := fixture.database.Task(fixture.ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.SkillIDs) != 1 || stored.SkillIDs[0] != fixture.globalKey {
		t.Fatalf("rejected update changed the non-pet selection: %#v", stored.SkillIDs)
	}
}

// Unknown and disabled packages are rejected before anything is written.
func TestPetTaskRejectsUnavailableSkills(t *testing.T) {
	fixture := newPetCrossSkillFixture(t)
	task := fixture.createTask(t, petTaskID, petProjectID, fixture.workspace.ID, []string{fixture.globalKey})
	unknown := "vault-skill:project:" + petCrossSkillProjectID + ":missing"
	for _, id := range []string{unknown, fixture.disabledKey, "skill-legacy-id"} {
		if _, err := fixture.service.UpdateTaskSkills(fixture.ctx, task.ID, []string{fixture.globalKey, id}); err == nil || !strings.Contains(err.Error(), id) {
			t.Fatalf("pet task accepted %s: err=%v", id, err)
		}
		stored, err := fixture.database.Task(fixture.ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored.SkillIDs) != 1 || stored.SkillIDs[0] != fixture.globalKey {
			t.Fatalf("rejected skill %s modified the selection: %#v", id, stored.SkillIDs)
		}
	}
}

func findVaultSkill(catalog VaultSkillCatalog, key string) *VaultSkillView {
	for index := range catalog.Skills {
		if catalog.Skills[index].Key == key {
			return &catalog.Skills[index]
		}
	}
	return nil
}

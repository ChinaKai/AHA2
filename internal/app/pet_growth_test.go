package app

import (
	"strings"
	"testing"
)

// The pet revises its own identity and principles as it learns, and nothing
// blocks that. What the Owner needs is to see when they moved, and the pet
// needs a rejection to stay in view until it has become a principle.
func TestPetPrincipleChangesAreRecordedAndRejectionsBecomeLessons(t *testing.T) {
	fixture := newPetTaskFixture(t)
	fixture.createPetDraft(t, "growth")
	if err := fixture.service.PetUpdateCore(fixture.ctx, "principles", "# 做事准则\n\n- 不替 owner 做决定。\n"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.PetUpdateCore(fixture.ctx, "principles", "# 做事准则\n\n- 撤得回的事自己决定。\n"); err != nil {
		t.Fatal(err)
	}
	documents, err := fixture.service.PetCoreDocuments(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	changes := ""
	for _, document := range documents {
		if document.Name == "changes.md" {
			changes = document.Content
		}
	}
	if !strings.Contains(changes, "principles.md") || !strings.Contains(changes, "-- 不替 owner 做决定。") ||
		!strings.Contains(changes, "+- 撤得回的事自己决定。") {
		t.Fatalf("the Owner cannot see what changed in the principles:\n%s", changes)
	}
	// Newest first, and saving identical text records nothing.
	if strings.Index(changes, "撤得回的事自己决定") > strings.Index(changes, "+- 不替 owner 做决定。") {
		t.Fatalf("the newest change is not first:\n%s", changes)
	}
	before := changes
	if err := fixture.service.PetUpdateCore(fixture.ctx, "principles", "# 做事准则\n\n- 撤得回的事自己决定。\n"); err != nil {
		t.Fatal(err)
	}
	documents, _ = fixture.service.PetCoreDocuments(fixture.ctx)
	for _, document := range documents {
		if document.Name == "changes.md" && document.Content != before {
			t.Fatal("an unchanged save was recorded as a change")
		}
	}
	// The agenda and loops are working notes, not the pet's boundary.
	if err := fixture.service.PetUpdateCore(fixture.ctx, "agenda", "# 自主议程\n\n- next | x | y\n"); err != nil {
		t.Fatal(err)
	}
	documents, _ = fixture.service.PetCoreDocuments(fixture.ctx)
	for _, document := range documents {
		if document.Name == "changes.md" && strings.Contains(document.Content, "agenda.md") {
			t.Fatal("an agenda edit was recorded as a principle change")
		}
	}

	approval, err := fixture.service.CreatePetApproval(fixture.ctx, PetApprovalInput{
		Kind: "owner_decision", Title: "共享控制加租约", Summary: "给共享控制加一个租约", Reason: "防止忘记关闭", Risk: "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ResolvePetApproval(fixture.ctx, approval.ID, "reject", "我只要一个开关，不要需要我持续管理的机制"); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Corrections) != 1 || state.Corrections[0].Title != "共享控制加租约" ||
		!strings.Contains(state.Corrections[0].OwnerNote, "只要一个开关") || state.CorrectionsNote == "" {
		t.Fatalf("the rejection did not stay in front of the pet: %#v", state.Corrections)
	}
	// Writing the lesson into the principles is what closes it.
	if err := fixture.service.PetUpdateCore(fixture.ctx, "principles", "# 做事准则\n\n- owner 要开关，不要需要持续管理的机制。\n"); err != nil {
		t.Fatal(err)
	}
	state, err = fixture.service.PetWorkState(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Corrections) != 0 {
		t.Fatalf("the correction stayed open after the principles were updated: %#v", state.Corrections)
	}
}

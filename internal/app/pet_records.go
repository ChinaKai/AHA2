package app

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/knowledgevault"
	"github.com/ChinaKai/AHA2/internal/store"
)

/* Records page: the Owner's corrections ---------------------------------- */

const petCorrectionHistoryLimit = 50

// Correction states on the records page.
const (
	// PetCorrectionOpen: the pet has not turned it into a principle yet and
	// it is still inside the seven days it is shown to the pet.
	PetCorrectionOpen = "open"
	// PetCorrectionAbsorbed: the pet updated principles.md, which closes
	// every open correction (event status "absorbed").
	PetCorrectionAbsorbed = "absorbed"
	// PetCorrectionExpired: still open but older than seven days; the pet no
	// longer sees it.
	PetCorrectionExpired = "expired"
)

// PetCorrectionRecord is one Owner correction with its state.
type PetCorrectionRecord struct {
	At         time.Time `json:"at"`
	ApprovalID string    `json:"approval_id,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Title      string    `json:"title"`
	OwnerNote  string    `json:"owner_note"`
	State      string    `json:"state"`
}

// PetCorrectionHistory returns the newest corrections in every state. It is
// a records-page read, so it is served by the insights request that only
// runs once the page is opened.
func (s *Service) PetCorrectionHistory(ctx context.Context) ([]PetCorrectionRecord, error) {
	events, err := s.store.ListPetEventsByKind(ctx, petCorrectionEventKind, petCorrectionHistoryLimit)
	if err != nil {
		return nil, err
	}
	cutoff := s.now().UTC().Add(-petCorrectionMaxAge)
	records := make([]PetCorrectionRecord, 0, len(events))
	for _, event := range events {
		var payload struct {
			ApprovalID string `json:"approval_id"`
			Kind       string `json:"kind"`
			Title      string `json:"title"`
			OwnerNote  string `json:"owner_note"`
		}
		// Same rule as the pet's open list: a correction is the Owner's
		// written reason, so a row without one is not shown.
		if json.Unmarshal([]byte(event.Detail), &payload) != nil || strings.TrimSpace(payload.OwnerNote) == "" {
			continue
		}
		record := PetCorrectionRecord{
			At: event.At, ApprovalID: payload.ApprovalID, Kind: payload.Kind,
			Title: payload.Title, OwnerNote: payload.OwnerNote,
		}
		switch {
		case event.Status == "open" && !event.At.Before(cutoff):
			record.State = PetCorrectionOpen
		case event.Status == "open":
			record.State = PetCorrectionExpired
		default:
			// "absorbed" is the only other status the server writes.
			record.State = PetCorrectionAbsorbed
		}
		records = append(records, record)
	}
	return records, nil
}

/* Records page: what the pet promised ------------------------------------- */

// PetLedgerCounts is the number of entries per status in one ledger file.
type PetLedgerCounts struct {
	Name   string         `json:"name"`
	Counts map[string]int `json:"counts"`
	Total  int            `json:"total"`
}

// PetLedgerStatusCounts counts core/loops.md and core/agenda.md entries by
// status with the shared ledger parser. loops.md is counted outside its
// closed sections, the same part the desk and the core digest read.
func (s *Service) PetLedgerStatusCounts(ctx context.Context) []PetLedgerCounts {
	root := strings.TrimSpace(s.petWorkspaceRoot(ctx))
	result := []PetLedgerCounts{}
	for _, source := range []struct {
		name, content string
	}{
		{"loops.md", activePetLoops(petReadCoreFile(root, petCoreLoopsPath))},
		{"agenda.md", petReadCoreFile(root, petCoreAgendaPath)},
	} {
		counts := PetLedgerCounts{Name: source.name, Counts: map[string]int{}}
		for _, entry := range parsePetLedger(source.content) {
			counts.Counts[entry.Status]++
			counts.Total++
		}
		result = append(result, counts)
	}
	return result
}

/* Knowledge health -------------------------------------------------------- */

const (
	petKnowledgePendingLimit = 10
	// petKnowledgeProjectLimit bounds the per-project reads. Each project is
	// one index or table read; the limit keeps an installation with hundreds
	// of projects from stretching the insights request.
	petKnowledgeProjectLimit = 50
)

// PetKnowledgeSpaceHealth is one knowledge space: the global library or a
// project's space.
type PetKnowledgeSpaceHealth struct {
	Scope     string `json:"scope"`
	ProjectID string `json:"project_id,omitempty"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Entries   int    `json:"entries"`
	// Pending counts entries marked stale or wrong: they wait for a revision.
	Pending int `json:"pending"`
	// Helped is the total "helped" feedback. HelpedKnown is false for vault
	// spaces: the vault records helped feedback in its write log, not on the
	// entry, so there is no total to read.
	Helped      int    `json:"helped"`
	HelpedKnown bool   `json:"helped_known"`
	Error       string `json:"error,omitempty"`
}

// PetKnowledgePending is one entry waiting for a revision.
type PetKnowledgePending struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Scope     string `json:"scope"`
	ProjectID string `json:"project_id,omitempty"`
	Space     string `json:"space"`
	Status    string `json:"status"`
}

// PetKnowledgeHealth is the records page's knowledge line.
type PetKnowledgeHealth struct {
	Entries int `json:"entries"`
	Pending int `json:"pending"`
	Helped  int `json:"helped"`
	// HelpedPartial is true when some space could not report helped feedback
	// (vault spaces), so Helped covers only the other spaces.
	HelpedPartial bool                      `json:"helped_partial"`
	Spaces        []PetKnowledgeSpaceHealth `json:"spaces"`
	PendingItems  []PetKnowledgePending     `json:"pending_items"`
	// Truncated is true when projects beyond petKnowledgeProjectLimit were
	// not read.
	Truncated bool `json:"truncated"`
	// Failed counts spaces whose read failed; they are listed with Error and
	// left out of the totals.
	Failed int `json:"failed"`
	// Projects is how many project spaces were read.
	Projects  int   `json:"projects"`
	ElapsedMS int64 `json:"elapsed_ms"`
}

// petKnowledgeHealthCounts is what one space contributes.
type petKnowledgeHealthCounts struct {
	entries, pending, helped int
	helpedKnown              bool
	pendingItems             []PetKnowledgePending
}

// PetKnowledgeHealth reads the global library and every project that has a
// knowledge space (system-managed projects have none and are skipped).
// Each space is read from its authoritative source, using the same read
// decision as every other knowledge read.
func (s *Service) PetKnowledgeHealth(ctx context.Context) (PetKnowledgeHealth, error) {
	started := time.Now()
	health := PetKnowledgeHealth{Spaces: []PetKnowledgeSpaceHealth{}, PendingItems: []PetKnowledgePending{}}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return PetKnowledgeHealth{}, err
	}
	spaces := []PetKnowledgeSpaceHealth{{Scope: "global", Name: "global"}}
	readable := 0
	for _, project := range projects {
		if domain.IsSystemManagedProject(project) {
			continue
		}
		if readable == petKnowledgeProjectLimit {
			health.Truncated = true
			break
		}
		readable++
		spaces = append(spaces, PetKnowledgeSpaceHealth{Scope: "project", ProjectID: project.ID, Name: project.Name})
	}
	health.Projects = readable
	pending := []PetKnowledgePending{}
	for _, space := range spaces {
		counts, source, readErr := s.petKnowledgeSpaceCounts(ctx, space)
		space.Source = source
		if readErr != nil {
			space.Error = "knowledge_read_failed"
			health.Failed++
			health.Spaces = append(health.Spaces, space)
			continue
		}
		space.Entries, space.Pending, space.Helped, space.HelpedKnown = counts.entries, counts.pending, counts.helped, counts.helpedKnown
		health.Entries += counts.entries
		health.Pending += counts.pending
		if counts.helpedKnown {
			health.Helped += counts.helped
		} else {
			health.HelpedPartial = true
		}
		health.Spaces = append(health.Spaces, space)
		pending = append(pending, counts.pendingItems...)
	}
	// Global first, then projects in the order above; inside a space by title,
	// so the list is stable between reads.
	if len(pending) > petKnowledgePendingLimit {
		pending = pending[:petKnowledgePendingLimit]
	}
	health.PendingItems = pending
	health.ElapsedMS = time.Since(started).Milliseconds()
	return health, nil
}

func (s *Service) petKnowledgeSpaceCounts(ctx context.Context, space PetKnowledgeSpaceHealth) (petKnowledgeHealthCounts, string, error) {
	decision := s.knowledgeReadDecision(space.Scope, space.ProjectID)
	if decision.Err != nil {
		return petKnowledgeHealthCounts{}, "vault", decision.Err
	}
	if decision.Vault {
		counts, err := s.petVaultKnowledgeCounts(ctx, space)
		return counts, "vault", err
	}
	var entries []domain.KnowledgeEntry
	var err error
	if space.Scope == "global" {
		entries, err = s.store.ListKnowledge(ctx, "global", "", nil)
	} else {
		entries, err = s.store.ListProjectKnowledge(ctx, space.ProjectID, nil)
	}
	if err != nil {
		return petKnowledgeHealthCounts{}, "legacy", err
	}
	counts := petKnowledgeHealthCounts{helpedKnown: true}
	for _, entry := range entries {
		// Index entries are the tree's folders, not knowledge; deprecated
		// entries are retired and drafts are not knowledge yet.
		// The managed global categories (general, agent lessons and its two
		// halves) are folders too, created by AHA.
		if entry.IsIndex || store.IsManagedGlobalKnowledgeCategory(entry.ID) ||
			(entry.Status != domain.KnowledgeVerified && entry.Status != domain.KnowledgeStale) {
			continue
		}
		counts.entries++
		counts.helped += entry.HelpedCount
		if entry.Status == domain.KnowledgeStale || entry.FeedbackState == "stale" || entry.FeedbackState == "wrong" {
			counts.pending++
			status := entry.FeedbackState
			if status != "wrong" {
				status = "stale"
			}
			counts.pendingItems = append(counts.pendingItems, petKnowledgePendingItem(space, entry.ID, entry.Title, status))
		}
	}
	sortPetKnowledgePending(counts.pendingItems)
	return counts, "legacy", nil
}

// petVaultKnowledgeCounts reads the vault index directly instead of the
// shared snapshot helpers: those drop entries whose status is "wrong",
// which are exactly the ones the health line has to count.
func (s *Service) petVaultKnowledgeCounts(ctx context.Context, space PetKnowledgeSpaceHealth) (petKnowledgeHealthCounts, error) {
	counts := petKnowledgeHealthCounts{}
	err := s.knowledgeVault.WithReader(func(reader *knowledgevault.Reader) error {
		if !reader.IndexReady() {
			return knowledgevault.ErrReaderNotReady
		}
		if space.Scope == "project" && strings.TrimSpace(reader.Manifest().Projects[space.ProjectID]) == "" {
			return knowledgevault.ErrReaderNotReady
		}
		entries, err := reader.Entries(ctx, space.Scope, space.ProjectID)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			// Same eligibility as the knowledge page (mapVaultKnowledgeEntry):
			// no guide, archive or orphan documents, no folder indexes.
			path := strings.TrimPrefix(strings.ReplaceAll(entry.Path, "\\", "/"), "./")
			if entry.Archive || entry.IsIndex || path == knowledgevault.GuideRelativePath ||
				strings.HasPrefix(path, "archive/") || strings.HasPrefix(path, "orphans/") {
				continue
			}
			status := strings.ToLower(strings.TrimSpace(entry.Status))
			if status == "" {
				status = string(domain.KnowledgeVerified)
			}
			switch status {
			case string(domain.KnowledgeVerified):
				counts.entries++
			case "stale", "wrong":
				counts.entries++
				counts.pending++
				counts.pendingItems = append(counts.pendingItems, petKnowledgePendingItem(space, entry.ID, entry.Title, status))
			}
		}
		return nil
	})
	if err != nil {
		return petKnowledgeHealthCounts{}, err
	}
	sortPetKnowledgePending(counts.pendingItems)
	return counts, nil
}

func petKnowledgePendingItem(space PetKnowledgeSpaceHealth, id, title, status string) PetKnowledgePending {
	if strings.TrimSpace(title) == "" {
		title = id
	}
	return PetKnowledgePending{
		ID: id, Title: truncateAssistantText(title, 120), Scope: space.Scope,
		ProjectID: space.ProjectID, Space: space.Name, Status: status,
	}
}

func sortPetKnowledgePending(items []PetKnowledgePending) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Title < items[j].Title })
}

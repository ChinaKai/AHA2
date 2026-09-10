package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const maxListPageSize = 200

type listOptions struct {
	Cursor  string
	Limit   int
	Paged   bool
	Summary bool
}

type listCursor struct {
	Kind      string `json:"kind"`
	UpdatedAt string `json:"updated_at"`
	ID        string `json:"id"`
}

type listPage[T any] struct {
	Items      []T
	NextCursor string
	HasMore    bool
}

func parseListOptions(request *http.Request) (listOptions, error) {
	query := request.URL.Query()
	rawLimit := strings.TrimSpace(query.Get("limit"))
	rawPageSize := strings.TrimSpace(query.Get("page_size"))
	if rawLimit != "" && rawPageSize != "" && rawLimit != rawPageSize {
		return listOptions{}, fmt.Errorf("limit and page_size disagree")
	}
	if rawLimit == "" {
		rawLimit = rawPageSize
	}
	options := listOptions{Cursor: strings.TrimSpace(query.Get("cursor"))}
	options.Paged = rawLimit != "" || options.Cursor != ""
	if options.Paged {
		options.Limit = 50
	}
	if rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxListPageSize {
			return listOptions{}, fmt.Errorf("limit must be between 1 and %d", maxListPageSize)
		}
		options.Limit = limit
	}
	switch strings.ToLower(strings.TrimSpace(query.Get("summary"))) {
	case "", "0", "false":
	case "1", "true":
		options.Summary = true
	default:
		return listOptions{}, fmt.Errorf("summary must be true or false")
	}
	switch strings.ToLower(strings.TrimSpace(query.Get("view"))) {
	case "", "detail", "full":
	case "summary":
		options.Summary = true
	default:
		return listOptions{}, fmt.Errorf("view must be summary or detail")
	}
	return options, nil
}

func paginateByUpdated[T any](items []T, kind string, options listOptions, key func(T) (time.Time, string)) (listPage[T], error) {
	if !options.Paged {
		return listPage[T]{Items: items}, nil
	}
	sort.SliceStable(items, func(left, right int) bool {
		leftTime, leftID := key(items[left])
		rightTime, rightID := key(items[right])
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		return leftID > rightID
	})
	start := 0
	if options.Cursor != "" {
		cursor, err := decodeListCursor(options.Cursor, kind)
		if err != nil {
			return listPage[T]{}, err
		}
		cursorTime, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
		if err != nil {
			return listPage[T]{}, fmt.Errorf("invalid cursor timestamp")
		}
		start = sort.Search(len(items), func(index int) bool {
			updatedAt, id := key(items[index])
			return updatedAt.Before(cursorTime) || updatedAt.Equal(cursorTime) && id < cursor.ID
		})
	}
	end := start + options.Limit
	if end > len(items) {
		end = len(items)
	}
	page := listPage[T]{Items: items[start:end], HasMore: end < len(items)}
	if page.HasMore && len(page.Items) > 0 {
		updatedAt, id := key(page.Items[len(page.Items)-1])
		page.NextCursor = encodeListCursor(listCursor{Kind: kind, UpdatedAt: updatedAt.UTC().Format(time.RFC3339Nano), ID: id})
	}
	return page, nil
}

func listCursorPosition(options listOptions, kind string) (time.Time, string, error) {
	if options.Cursor == "" {
		return time.Time{}, "", nil
	}
	cursor, err := decodeListCursor(options.Cursor, kind)
	if err != nil {
		return time.Time{}, "", err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor timestamp")
	}
	return updatedAt, cursor.ID, nil
}

func storeListPage[T any](items []T, hasMore bool, kind string, key func(T) (time.Time, string)) listPage[T] {
	page := listPage[T]{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		updatedAt, id := key(items[len(items)-1])
		page.NextCursor = encodeListCursor(listCursor{Kind: kind, UpdatedAt: updatedAt.UTC().Format(time.RFC3339Nano), ID: id})
	}
	return page
}

func encodeListCursor(cursor listCursor) string {
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeListCursor(value, kind string) (listCursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return listCursor{}, fmt.Errorf("invalid cursor")
	}
	var cursor listCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Kind != kind || cursor.UpdatedAt == "" || cursor.ID == "" {
		return listCursor{}, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}

type projectSummary struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	ProjectType        string    `json:"project_type,omitempty"`
	DefaultWorkspaceID string    `json:"default_workspace_id,omitempty"`
	KnowledgePolicy    string    `json:"knowledge_policy"`
	KnowledgeRevision  int       `json:"knowledge_revision"`
	ChannelInstanceID  string    `json:"channel_instance_id,omitempty"`
	ChannelRetired     bool      `json:"channel_retired,omitempty"`
	ReadOnlyReason     string    `json:"read_only_reason,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func summarizeProjects(items []domain.Project) []projectSummary {
	result := make([]projectSummary, 0, len(items))
	for _, item := range items {
		result = append(result, projectSummary{
			ID: item.ID, Name: item.Name, Description: item.Description, ProjectType: item.ProjectType,
			DefaultWorkspaceID: item.DefaultWorkspaceID, KnowledgePolicy: item.KnowledgePolicy,
			KnowledgeRevision: item.KnowledgeRevision, ChannelInstanceID: item.ChannelInstanceID,
			ChannelRetired: item.ChannelRetired, ReadOnlyReason: item.ReadOnlyReason, UpdatedAt: item.UpdatedAt,
		})
	}
	return result
}

type taskSummary struct {
	ID                string            `json:"id"`
	Code              string            `json:"code,omitempty"`
	ProjectID         string            `json:"project_id"`
	WorkspaceID       string            `json:"workspace_id"`
	Title             string            `json:"title"`
	CurrentGoal       string            `json:"current_goal,omitempty"`
	Status            domain.TaskStatus `json:"status"`
	TotalTokens       int64             `json:"total_tokens"`
	OwnerDeviceID     string            `json:"owner_device_id,omitempty"`
	ReadOnly          bool              `json:"read_only,omitempty"`
	ReadOnlyReason    string            `json:"read_only_reason,omitempty"`
	ChannelInstanceID string            `json:"channel_instance_id,omitempty"`
	ChannelRetired    bool              `json:"channel_retired,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

func summarizeTasks(items []domain.Task) []taskSummary {
	result := make([]taskSummary, 0, len(items))
	for _, item := range items {
		result = append(result, taskSummary{
			ID: item.ID, Code: item.Code, ProjectID: item.ProjectID, WorkspaceID: item.WorkspaceID,
			Title: item.Title, CurrentGoal: item.CurrentGoal, Status: item.Status, TotalTokens: item.TotalTokens,
			OwnerDeviceID: item.OwnerDeviceID, ReadOnly: item.ReadOnly, ReadOnlyReason: item.ReadOnlyReason,
			ChannelInstanceID: item.ChannelInstanceID, ChannelRetired: item.ChannelRetired, UpdatedAt: item.UpdatedAt,
		})
	}
	return result
}

type knowledgeSummary struct {
	ID                 string                 `json:"id"`
	Scope              string                 `json:"scope"`
	ProjectID          string                 `json:"project_id,omitempty"`
	BoundProjectID     string                 `json:"bound_project_id,omitempty"`
	BindingMode        string                 `json:"binding_mode,omitempty"`
	CanProposeRevision bool                   `json:"can_propose_revision"`
	ParentID           string                 `json:"parent_id,omitempty"`
	Slug               string                 `json:"slug,omitempty"`
	SortOrder          int                    `json:"sort_order"`
	IsIndex            bool                   `json:"is_index"`
	Type               string                 `json:"type"`
	Title              string                 `json:"title"`
	Status             domain.KnowledgeStatus `json:"status"`
	ProductLineID      string                 `json:"product_line_id,omitempty"`
	Revision           int                    `json:"revision"`
	HelpedCount        int                    `json:"helped_count"`
	StaleCount         int                    `json:"stale_count"`
	FeedbackState      string                 `json:"feedback_state,omitempty"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

func summarizeKnowledge(items []domain.KnowledgeEntry) []knowledgeSummary {
	result := make([]knowledgeSummary, 0, len(items))
	for _, item := range items {
		result = append(result, knowledgeSummary{
			ID: item.ID, Scope: item.Scope, ProjectID: item.ProjectID, BoundProjectID: item.BoundProjectID,
			BindingMode: item.BindingMode, CanProposeRevision: item.CanProposeRevision, ParentID: item.ParentID,
			Slug: item.Slug, SortOrder: item.SortOrder, IsIndex: item.IsIndex, Type: item.Type, Title: item.Title,
			Status: item.Status, ProductLineID: item.ProductLineID, Revision: item.Revision,
			HelpedCount: item.HelpedCount, StaleCount: item.StaleCount, FeedbackState: item.FeedbackState,
			UpdatedAt: item.UpdatedAt,
		})
	}
	return result
}

type knowledgeProposalSummary struct {
	ID           string                         `json:"id"`
	EntryID      string                         `json:"entry_id"`
	BaseRevision int                            `json:"base_revision"`
	Proposed     knowledgeSummary               `json:"proposed"`
	SourceTaskID string                         `json:"source_task_id,omitempty"`
	SourceTurnID string                         `json:"source_turn_id,omitempty"`
	Status       domain.KnowledgeProposalStatus `json:"status"`
	ReviewMode   string                         `json:"review_mode"`
	CreatedAt    time.Time                      `json:"created_at"`
	UpdatedAt    time.Time                      `json:"updated_at"`
	DecidedAt    time.Time                      `json:"decided_at,omitempty"`
}

func summarizeKnowledgeProposals(items []domain.KnowledgeProposal) []knowledgeProposalSummary {
	result := make([]knowledgeProposalSummary, 0, len(items))
	for _, item := range items {
		proposed := summarizeKnowledge([]domain.KnowledgeEntry{item.Proposed})[0]
		result = append(result, knowledgeProposalSummary{
			ID: item.ID, EntryID: item.EntryID, BaseRevision: item.BaseRevision, Proposed: proposed,
			SourceTaskID: item.SourceTaskID, SourceTurnID: item.SourceTurnID, Status: item.Status,
			ReviewMode: item.ReviewMode, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, DecidedAt: item.DecidedAt,
		})
	}
	return result
}

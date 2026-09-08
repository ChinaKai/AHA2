package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Server) knowledgeReviewSettings(writer http.ResponseWriter, request *http.Request) {
	item, err := s.store.KnowledgeReviewSettings(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "knowledge_review_settings_failed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "review_settings": item})
}

func (s *Server) updateKnowledgeReviewSettings(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		AutoApprove bool `json:"auto_approve"`
	}
	if decodeJSON(request, &payload) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	item, err := s.store.UpdateKnowledgeReviewSettings(request.Context(), domain.KnowledgeReviewSettings{
		AutoApprove: payload.AutoApprove, UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "update_knowledge_review_settings_failed")
		return
	}
	s.audit(request, "knowledge.review.settings.update", "settings", "knowledge_review", map[string]any{"auto_approve": item.AutoApprove})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "review_settings": item})
}

type knowledgeBatchFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

func (s *Server) batchKnowledgeProposalReviews(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Action      string   `json:"action"`
		ProposalIDs []string `json:"proposal_ids"`
		LegacyIDs   []string `json:"legacy_ids"`
	}
	if decodeJSON(request, &payload) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	payload.Action = strings.TrimSpace(payload.Action)
	if payload.Action != "approve" && payload.Action != "reject" || len(payload.ProposalIDs)+len(payload.LegacyIDs) == 0 || len(payload.ProposalIDs)+len(payload.LegacyIDs) > 100 {
		writeError(writer, http.StatusBadRequest, "knowledge_batch_invalid")
		return
	}
	processed := []string{}
	failures := []knowledgeBatchFailure{}
	now := time.Now().UTC()
	for _, id := range uniqueKnowledgeBatchIDs(payload.ProposalIDs) {
		var err error
		if payload.Action == "approve" {
			var proposal domain.KnowledgeProposal
			var entry domain.KnowledgeEntry
			proposal, entry, err = s.store.ApproveKnowledgeProposal(request.Context(), id, now)
			if err == nil {
				s.audit(request, "knowledge.proposal.batch_approve", "knowledge_proposal", proposal.ID, map[string]any{"entry_id": entry.ID, "revision": entry.Revision})
			}
		} else {
			var proposal domain.KnowledgeProposal
			proposal, err = s.store.RejectKnowledgeProposal(request.Context(), id, now)
			if err == nil {
				s.audit(request, "knowledge.proposal.batch_reject", "knowledge_proposal", proposal.ID, map[string]any{"entry_id": proposal.EntryID})
			}
		}
		if err != nil {
			failures = append(failures, knowledgeBatchFailure{ID: id, Error: knowledgeBatchError(err)})
		} else {
			processed = append(processed, id)
		}
	}
	for _, id := range uniqueKnowledgeBatchIDs(payload.LegacyIDs) {
		entry, err := s.store.Knowledge(request.Context(), id)
		if err == nil && (entry.IsIndex || entry.Status != domain.KnowledgeCandidate) {
			err = errors.New("knowledge entry is not a pending legacy candidate")
		}
		if err == nil {
			if payload.Action == "approve" {
				err = s.store.VerifyKnowledge(request.Context(), id, now.Format(time.RFC3339Nano))
			} else {
				err = s.store.DeleteKnowledge(request.Context(), id)
			}
		}
		if err != nil {
			failures = append(failures, knowledgeBatchFailure{ID: id, Error: knowledgeBatchError(err)})
		} else {
			processed = append(processed, id)
			s.audit(request, "knowledge.legacy.batch_"+payload.Action, "knowledge", id, nil)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": len(failures) == 0, "processed": processed, "failures": failures})
}

func uniqueKnowledgeBatchIDs(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func knowledgeBatchError(err error) string {
	if errors.Is(err, sql.ErrNoRows) {
		return "not found"
	}
	return err.Error()
}

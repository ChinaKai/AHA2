package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const knowledgeLibraryColumns = `library.id,library.container_project_id,library.name,library.description,library.source_identity,COALESCE(binding.project_id,''),COALESCE(binding.binding_mode,''),
    (SELECT COUNT(*) FROM knowledge_entries entry WHERE entry.project_id=library.container_project_id AND entry.is_index=0),
    (SELECT COUNT(*) FROM skills skill WHERE skill.project_id=library.container_project_id),
    library.created_at,library.updated_at`

func knowledgeLibraryID(projectID string) string {
	return "library_" + projectID
}

func scanKnowledgeLibrary(scanner interface{ Scan(...any) error }) (domain.KnowledgeLibrary, error) {
	var item domain.KnowledgeLibrary
	var createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.ContainerProjectID, &item.Name, &item.Description, &item.SourceIdentity,
		&item.BoundProjectID, &item.BindingMode, &item.KnowledgeCount, &item.SkillCount, &createdAt, &updatedAt,
	)
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return item, err
}

func (s *Store) EnsureKnowledgeLibraryForProject(ctx context.Context, project domain.Project) (domain.KnowledgeLibrary, error) {
	if project.ID == "" || project.ProjectType != "knowledge" {
		return domain.KnowledgeLibrary{}, fmt.Errorf("knowledge library container project is invalid")
	}
	now := project.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	createdAt := project.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	id := knowledgeLibraryID(project.ID)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO knowledge_libraries(id,container_project_id,name,description,source_identity,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,
			source_identity=excluded.source_identity,updated_at=excluded.updated_at`,
		id, project.ID, project.Name, project.Description, project.RepositoryIdentity, timeString(createdAt), timeString(now),
	)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	return s.KnowledgeLibrary(ctx, id)
}

func (s *Store) KnowledgeLibrary(ctx context.Context, id string) (domain.KnowledgeLibrary, error) {
	return scanKnowledgeLibrary(s.db.QueryRowContext(ctx, `SELECT `+knowledgeLibraryColumns+`
		FROM knowledge_libraries library
		LEFT JOIN project_knowledge_bindings binding ON binding.library_id=library.id
		WHERE library.id=?`, id))
}

func (s *Store) ListKnowledgeLibraries(ctx context.Context) ([]domain.KnowledgeLibrary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+knowledgeLibraryColumns+`
		FROM knowledge_libraries library
		LEFT JOIN project_knowledge_bindings binding ON binding.library_id=library.id
		ORDER BY CASE WHEN binding.project_id IS NULL THEN 0 ELSE 1 END,library.name,library.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.KnowledgeLibrary{}
	for rows.Next() {
		item, err := scanKnowledgeLibrary(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func normalizeKnowledgeBindingMode(value string) (string, error) {
	if value == "" {
		return "external", nil
	}
	if value != "project" && value != "external" {
		return "", fmt.Errorf("knowledge library binding mode is invalid")
	}
	return value, nil
}

func (s *Store) BindKnowledgeLibrary(ctx context.Context, libraryID, projectID, bindingMode string, now time.Time) (domain.KnowledgeLibrary, error) {
	library, err := s.KnowledgeLibrary(ctx, libraryID)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	project, err := s.Project(ctx, projectID)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	if project.ProjectType == "knowledge" || project.ID == library.ContainerProjectID {
		return domain.KnowledgeLibrary{}, fmt.Errorf("knowledge library target project is invalid")
	}
	bindingMode, err = normalizeKnowledgeBindingMode(bindingMode)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = s.sharedTimeVersion(ctx, "knowledge_binding", library.ID, now)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO project_knowledge_bindings(library_id,project_id,binding_mode,created_at,updated_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(library_id) DO UPDATE SET project_id=excluded.project_id,binding_mode=excluded.binding_mode,updated_at=excluded.updated_at`,
		library.ID, project.ID, bindingMode, timeString(now), timeString(now),
	)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	return s.KnowledgeLibrary(ctx, library.ID)
}

func (s *Store) UnbindKnowledgeLibrary(ctx context.Context, libraryID string) (domain.KnowledgeLibrary, error) {
	library, err := s.KnowledgeLibrary(ctx, libraryID)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	var updatedAt string
	if err := s.db.QueryRowContext(ctx, `SELECT updated_at FROM project_knowledge_bindings WHERE library_id=?`, libraryID).Scan(&updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return domain.KnowledgeLibrary{}, sql.ErrNoRows
		}
		return domain.KnowledgeLibrary{}, err
	}
	if _, err := s.deleteSharedObject(ctx, "knowledge_binding", libraryID, updatedAt, time.Now().UTC()); err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	library.BoundProjectID = ""
	library.BindingMode = ""
	return library, nil
}

func (s *Store) ListKnowledgeLibraryBindings(ctx context.Context) ([]domain.KnowledgeLibraryBinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT library_id,project_id,binding_mode,created_at,updated_at FROM project_knowledge_bindings ORDER BY library_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.KnowledgeLibraryBinding{}
	for rows.Next() {
		var item domain.KnowledgeLibraryBinding
		var createdAt, updatedAt string
		if err := rows.Scan(&item.LibraryID, &item.ProjectID, &item.BindingMode, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) KnowledgeLibraryBinding(ctx context.Context, libraryID string) (domain.KnowledgeLibraryBinding, error) {
	var item domain.KnowledgeLibraryBinding
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT library_id,project_id,binding_mode,created_at,updated_at FROM project_knowledge_bindings WHERE library_id=?`, libraryID).
		Scan(&item.LibraryID, &item.ProjectID, &item.BindingMode, &createdAt, &updatedAt)
	item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
	return item, err
}

func (s *Store) DetachProjectKnowledge(ctx context.Context, projectID string) (domain.KnowledgeLibrary, error) {
	project, err := s.Project(ctx, projectID)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	if project.ProjectType == "knowledge" {
		return domain.KnowledgeLibrary{}, fmt.Errorf("knowledge library container cannot be detached")
	}
	containerID := "knowledge_detached_" + project.ID
	container, err := s.Project(ctx, containerID)
	if err == sql.ErrNoRows {
		now := time.Now().UTC()
		container = domain.Project{
			ID: containerID, Name: project.Name + " 项目知识库",
			Description: "从 Project 解绑并保留的待绑定项目知识库。", ProjectType: "knowledge",
			KnowledgePolicy: "enabled", CreatedAt: now, UpdatedAt: now,
		}
		if err := s.CreateProject(ctx, container); err != nil {
			return domain.KnowledgeLibrary{}, err
		}
	} else if err != nil {
		return domain.KnowledgeLibrary{}, err
	} else if container.ProjectType != "knowledge" {
		return domain.KnowledgeLibrary{}, fmt.Errorf("detached knowledge target is not a library")
	}
	containerRoot, err := s.EnsureKnowledgeRoot(ctx, "project", container.ID)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	projectRoot, err := s.EnsureKnowledgeRoot(ctx, "project", project.ID)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	entries, err := s.ListKnowledge(ctx, "project", project.ID, nil)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	byID := map[string]domain.KnowledgeEntry{}
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := knowledgeEntryDepth(entries[i], byID), knowledgeEntryDepth(entries[j], byID)
		if left != right {
			return left < right
		}
		return entries[i].ID < entries[j].ID
	})
	moved := map[string]domain.KnowledgeEntry{}
	for _, entry := range entries {
		if entry.IsIndex {
			continue
		}
		entry.ProjectID = container.ID
		entry.BoundProjectID = ""
		if entry.ParentID == projectRoot.ID {
			entry.ParentID = containerRoot.ID
		}
		entry.ProductLineID = ""
		entry.Revision++
		entry.UpdatedAt = time.Now().UTC()
		if err := s.UpdateKnowledge(ctx, entry); err != nil {
			return domain.KnowledgeLibrary{}, err
		}
		moved[entry.ID] = entry
	}
	proposals, err := s.ListKnowledgeProposals(ctx, "project", project.ID)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	for _, proposal := range proposals {
		if proposal.Proposed.ProjectID != project.ID {
			continue
		}
		proposal.Proposed.ProjectID = container.ID
		proposal.Proposed.BoundProjectID = ""
		proposal.Proposed.ProductLineID = ""
		if proposal.Proposed.ParentID == projectRoot.ID {
			proposal.Proposed.ParentID = containerRoot.ID
		}
		if proposal.BaseRevision > 0 {
			proposal.BaseRevision++
			proposal.Proposed.Revision = proposal.BaseRevision + 1
			if entry, ok := moved[proposal.EntryID]; ok {
				base := entry
				proposal.BaseEntry = &base
			}
		}
		proposal.UpdatedAt = time.Now().UTC()
		if err := s.ImportKnowledgeProposal(ctx, proposal); err != nil {
			return domain.KnowledgeLibrary{}, err
		}
	}
	skills, err := s.ListSkills(ctx, "project", project.ID, false)
	if err != nil {
		return domain.KnowledgeLibrary{}, err
	}
	for _, skill := range skills {
		skill.ProjectID = container.ID
		skill.BoundProjectID = ""
		skill.Version++
		skill.UpdatedAt = time.Now().UTC()
		if err := s.UpdateSkill(ctx, skill); err != nil {
			return domain.KnowledgeLibrary{}, err
		}
	}
	return s.KnowledgeLibrary(ctx, knowledgeLibraryID(container.ID))
}

func knowledgeEntryDepth(entry domain.KnowledgeEntry, entries map[string]domain.KnowledgeEntry) int {
	depth := 0
	seen := map[string]bool{entry.ID: true}
	for entry.ParentID != "" {
		parent, ok := entries[entry.ParentID]
		if !ok || seen[parent.ID] {
			break
		}
		seen[parent.ID] = true
		depth++
		entry = parent
	}
	return depth
}

func (s *Store) DeleteKnowledgeLibrary(ctx context.Context, libraryID string) (domain.KnowledgeLibraryDeletion, error) {
	library, err := s.KnowledgeLibrary(ctx, libraryID)
	if err != nil {
		return domain.KnowledgeLibraryDeletion{}, err
	}
	if tasks, err := s.ListTasks(ctx, library.ContainerProjectID); err != nil {
		return domain.KnowledgeLibraryDeletion{}, err
	} else if len(tasks) > 0 {
		return domain.KnowledgeLibraryDeletion{}, fmt.Errorf("knowledge library container has tasks")
	}
	if workspaces, err := s.ListWorkspaces(ctx, library.ContainerProjectID); err != nil {
		return domain.KnowledgeLibraryDeletion{}, err
	} else if len(workspaces) > 0 {
		return domain.KnowledgeLibraryDeletion{}, fmt.Errorf("knowledge library container has workspaces")
	}
	if library.BoundProjectID != "" {
		if _, err := s.UnbindKnowledgeLibrary(ctx, library.ID); err != nil {
			return domain.KnowledgeLibraryDeletion{}, err
		}
	}
	result, err := s.deleteProjectKnowledgeContents(ctx, library.ContainerProjectID)
	if err != nil {
		return result, err
	}
	if err := s.deleteKnowledgeLibraryRoot(ctx, library.ContainerProjectID); err != nil {
		return result, err
	}
	result.Knowledge++
	if err := s.DeleteProject(ctx, library.ContainerProjectID); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) DeleteProjectKnowledge(ctx context.Context, projectID string) (domain.KnowledgeLibraryDeletion, error) {
	project, err := s.Project(ctx, projectID)
	if err != nil {
		return domain.KnowledgeLibraryDeletion{}, err
	}
	if project.ProjectType == "knowledge" {
		return domain.KnowledgeLibraryDeletion{}, fmt.Errorf("use DeleteKnowledgeLibrary for a library container")
	}
	return s.deleteProjectKnowledgeContents(ctx, projectID)
}

func (s *Store) deleteProjectKnowledgeContents(ctx context.Context, projectID string) (domain.KnowledgeLibraryDeletion, error) {
	result := domain.KnowledgeLibraryDeletion{}
	proposals, err := s.ListKnowledgeProposals(ctx, "project", projectID)
	if err != nil {
		return result, err
	}
	for _, proposal := range proposals {
		if proposal.Proposed.ProjectID != projectID {
			continue
		}
		if err := s.DeleteKnowledgeProposal(ctx, proposal.ID); err != nil {
			return result, err
		}
		result.Proposals++
	}
	for {
		entries, err := s.ListKnowledge(ctx, "project", projectID, nil)
		if err != nil {
			return result, err
		}
		parents := map[string]bool{}
		for _, entry := range entries {
			parents[entry.ParentID] = true
		}
		deleted := 0
		for _, entry := range entries {
			if entry.IsIndex || parents[entry.ID] {
				continue
			}
			if err := s.DeleteKnowledge(ctx, entry.ID); err != nil {
				return result, err
			}
			result.Knowledge++
			deleted++
		}
		if deleted == 0 {
			break
		}
	}
	skills, err := s.ListSkills(ctx, "project", projectID, false)
	if err != nil {
		return result, err
	}
	for _, skill := range skills {
		if err := s.DeleteSkill(ctx, skill.ID); err != nil {
			return result, err
		}
		result.Skills++
	}
	return result, nil
}

func (s *Store) deleteKnowledgeLibraryRoot(ctx context.Context, projectID string) error {
	root, err := scanKnowledge(s.db.QueryRowContext(ctx, `SELECT `+knowledgeColumns+` FROM knowledge_entries WHERE project_id=? AND is_index=1`, projectID))
	if err != nil {
		return err
	}
	project, err := s.Project(ctx, projectID)
	if err != nil || project.ProjectType != "knowledge" {
		return ErrKnowledgeRootManaged
	}
	deletedAt := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM knowledge_entries WHERE id=? AND project_id=? AND is_index=1`, root.ID, projectID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return sql.ErrNoRows
	}
	if err := insertSyncTombstone(ctx, tx, domain.SyncTombstone{
		ObjectType: "knowledge", ObjectID: root.ID, Version: fmt.Sprint(root.Revision),
		SyncKey: fmt.Sprintf("tombstone:v1:knowledge:%s:%d", root.ID, deletedAt.UnixNano()), DeletedAt: deletedAt,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) knowledgeLibraryBindings(ctx context.Context) (map[string]domain.KnowledgeLibraryBinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT library.container_project_id,binding.library_id,binding.project_id,binding.binding_mode,binding.created_at,binding.updated_at
		FROM project_knowledge_bindings binding
		JOIN knowledge_libraries library ON library.id=binding.library_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]domain.KnowledgeLibraryBinding{}
	for rows.Next() {
		var containerProjectID, createdAt, updatedAt string
		var binding domain.KnowledgeLibraryBinding
		if err := rows.Scan(&containerProjectID, &binding.LibraryID, &binding.ProjectID, &binding.BindingMode, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		binding.CreatedAt, binding.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result[containerProjectID] = binding
	}
	return result, rows.Err()
}

func (s *Store) CanContributeKnowledgeLibrary(ctx context.Context, sourceProjectID, targetProjectID string) (bool, error) {
	bindings, err := s.knowledgeLibraryBindings(ctx)
	if err != nil {
		return false, err
	}
	binding, ok := bindings[sourceProjectID]
	return ok && binding.ProjectID == targetProjectID && binding.BindingMode == "project", nil
}

func (s *Store) HasContributingKnowledgeBinding(ctx context.Context, projectID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_knowledge_bindings WHERE project_id=? AND binding_mode='project')`, projectID).Scan(&exists)
	return exists, err
}

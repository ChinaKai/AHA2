package store

import (
	"context"
)

// PruneImportedAHA1Worklogs removes only entries created by the AHA1 importer.
// Deletions use the normal Knowledge path so sync tombstones are emitted and
// other devices cannot replay the removed approval backlog.
func (s *Store) PruneImportedAHA1Worklogs(ctx context.Context) (int, error) {
	deleted := 0
	for {
		rows, err := s.db.QueryContext(ctx, `
			SELECT id FROM knowledge_entries
			WHERE id LIKE 'knowledge_aha1_%' AND type='task_worklog'
			ORDER BY id`)
		if err != nil {
			return deleted, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return deleted, err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return deleted, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := s.DeleteKnowledge(ctx, id); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
	for {
		rows, err := s.db.QueryContext(ctx, `
			SELECT entry.id FROM knowledge_entries entry
			WHERE entry.id LIKE 'knowledge_aha1_dir_%'
			  AND entry.body LIKE 'AHA1 导入目录：`+"`"+`worklog%'
			  AND NOT EXISTS(SELECT 1 FROM knowledge_entries child WHERE child.parent_id=entry.id)
			ORDER BY entry.id`)
		if err != nil {
			return deleted, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return deleted, err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return deleted, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := s.DeleteKnowledge(ctx, id); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
	return deleted, nil
}

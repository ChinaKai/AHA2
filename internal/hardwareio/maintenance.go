package hardwareio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Policy bounds the store. Zero values disable the matching rule.
type Policy struct {
	// RetentionDays deletes closed segments whose newest record is older.
	RetentionDays int
	// MaxBytes deletes the oldest closed segments, across every stream, until
	// the store is at or below this size. Writes are never refused.
	MaxBytes int64
	// KeepTask reports whether a Task still exists; streams of other Tasks
	// are removed. Nil keeps every stream.
	KeepTask func(taskID string) (bool, error)
}

// PruneResult reports one maintenance pass.
type PruneResult struct {
	RotatedSegments   int   `json:"rotated_segments"`
	RetentionSegments int   `json:"retention_segments"`
	RetentionBytes    int64 `json:"retention_bytes"`
	CapacitySegments  int   `json:"capacity_segments"`
	CapacityBytes     int64 `json:"capacity_bytes"`
	OrphanTasks       int   `json:"orphan_tasks"`
	OrphanBytes       int64 `json:"orphan_bytes"`
	TotalBytes        int64 `json:"total_bytes"`
	OverLimit         bool  `json:"over_limit"`
}

// Stats summarizes the store for the data management page.
type Stats struct {
	Root           string     `json:"root"`
	Streams        int        `json:"streams"`
	Segments       int        `json:"segments"`
	ActiveSegments int        `json:"active_segments"`
	Records        int64      `json:"records"`
	Bytes          int64      `json:"bytes"`
	LastSequence   int64      `json:"last_sequence"`
	CorruptLines   int64      `json:"corrupt_lines"`
	OldestAt       *time.Time `json:"oldest_at,omitempty"`
	NewestAt       *time.Time `json:"newest_at,omitempty"`
}

func (s *Store) snapshotStreams() ([]*stream, error) {
	if err := s.ensureInit(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	items := make([]*stream, 0, len(s.streams))
	for _, item := range s.streams {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key.less(items[j].key) })
	return items, nil
}

// Stats walks the in-memory index; it does not read segment contents.
func (s *Store) Stats() (Stats, error) {
	result := Stats{Root: s.options.Root}
	items, err := s.snapshotStreams()
	if err != nil {
		return result, err
	}
	var oldest, newest int64
	for _, item := range items {
		item.mu.RLock()
		if item.removed {
			item.mu.RUnlock()
			continue
		}
		counted := false
		for _, seg := range item.closed {
			counted = true
			result.Segments++
			result.Records += seg.rows
			result.Bytes += seg.bytes
			if oldest == 0 || seg.lastUnix < oldest {
				oldest = seg.lastUnix
			}
			newest = max(newest, seg.lastUnix)
		}
		if item.active != nil && item.active.rows > 0 {
			counted = true
			result.Segments++
			result.ActiveSegments++
			result.Records += item.active.rows
			result.Bytes += item.active.size
			if oldest == 0 || item.active.lastUnix < oldest {
				oldest = item.active.lastUnix
			}
			newest = max(newest, item.active.lastUnix)
		}
		if counted {
			result.Streams++
		}
		item.mu.RUnlock()
	}
	result.LastSequence, _ = s.LastSequence()
	result.CorruptLines = s.corruptLines.Load()
	if oldest > 0 {
		value := time.Unix(oldest, 0).UTC()
		result.OldestAt = &value
	}
	if newest > 0 {
		value := time.Unix(newest, 0).UTC()
		result.NewestAt = &value
	}
	return result, nil
}

// Prune applies policy: it closes active segments from an earlier day,
// removes streams of deleted Tasks, then enforces retention and the size cap.
// Oldest data is always dropped first; appends are never blocked.
func (s *Store) Prune(policy Policy) (PruneResult, error) {
	var result PruneResult
	items, err := s.snapshotStreams()
	if err != nil {
		return result, err
	}
	now := s.options.Now().UTC()
	today := now.Format("2006-01-02")
	var errs []error

	if policy.KeepTask != nil {
		seen := make(map[string]bool)
		for _, item := range items {
			if seen[item.key.TaskID] {
				continue
			}
			seen[item.key.TaskID] = true
			keep, err := policy.KeepTask(item.key.TaskID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !keep {
				freed, err := s.RemoveTask(item.key.TaskID)
				if err != nil {
					errs = append(errs, err)
				}
				result.OrphanTasks++
				result.OrphanBytes += freed
			}
		}
		if items, err = s.snapshotStreams(); err != nil {
			return result, err
		}
	}

	for _, item := range items {
		item.mu.Lock()
		if !item.removed && item.active != nil && item.active.firstDay != today {
			if err := s.rotateLocked(item); err != nil {
				errs = append(errs, err)
			} else {
				result.RotatedSegments++
			}
		}
		item.mu.Unlock()
	}

	if policy.RetentionDays > 0 {
		cutoff := now.Add(-time.Duration(policy.RetentionDays) * 24 * time.Hour).Unix()
		for _, item := range items {
			item.mu.Lock()
			kept := item.closed[:0]
			for _, seg := range item.closed {
				if seg.lastUnix < cutoff {
					if err := os.Remove(seg.path); err != nil && !errors.Is(err, os.ErrNotExist) {
						errs = append(errs, err)
						kept = append(kept, seg)
						continue
					}
					result.RetentionSegments++
					result.RetentionBytes += seg.bytes
					s.totalBytes.Add(-seg.bytes)
					continue
				}
				kept = append(kept, seg)
			}
			item.closed = kept
			item.mu.Unlock()
		}
	}

	type candidate struct {
		item *stream
		seg  segment
	}
	var candidates []candidate
	for _, item := range items {
		item.mu.RLock()
		if !item.removed {
			for _, seg := range item.closed {
				result.TotalBytes += seg.bytes
				candidates = append(candidates, candidate{item: item, seg: seg})
			}
			if item.active != nil {
				result.TotalBytes += item.active.size
			}
		}
		item.mu.RUnlock()
	}
	if policy.MaxBytes > 0 && result.TotalBytes > policy.MaxBytes {
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].seg.lastUnix != candidates[j].seg.lastUnix {
				return candidates[i].seg.lastUnix < candidates[j].seg.lastUnix
			}
			return candidates[i].seg.first < candidates[j].seg.first
		})
		for _, entry := range candidates {
			if result.TotalBytes <= policy.MaxBytes {
				break
			}
			entry.item.mu.Lock()
			for index, seg := range entry.item.closed {
				if seg.first != entry.seg.first {
					continue
				}
				if err := os.Remove(seg.path); err != nil && !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
					break
				}
				entry.item.closed = append(entry.item.closed[:index], entry.item.closed[index+1:]...)
				result.CapacitySegments++
				result.CapacityBytes += seg.bytes
				s.totalBytes.Add(-seg.bytes)
				result.TotalBytes -= seg.bytes
				break
			}
			entry.item.mu.Unlock()
		}
		result.OverLimit = result.TotalBytes > policy.MaxBytes
	}
	s.removeEmptyDirs()
	return result, errors.Join(errs...)
}

// RemoveTask deletes every stream of a Task and returns the bytes freed.
func (s *Store) RemoveTask(taskID string) (int64, error) {
	if taskID == "" {
		return 0, nil
	}
	if err := s.ensureInit(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrClosed
	}
	var removed []*stream
	for key, item := range s.streams {
		if key.TaskID == taskID {
			removed = append(removed, item)
			delete(s.streams, key)
		}
	}
	s.mu.Unlock()
	var freed int64
	for _, item := range removed {
		item.mu.Lock()
		for _, seg := range item.closed {
			freed += seg.bytes
		}
		if item.active != nil {
			freed += item.active.size
			if item.active.file != nil {
				_ = item.active.file.Close()
			}
			item.active = nil
		}
		item.closed = nil
		item.removed = true
		item.mu.Unlock()
	}
	s.totalBytes.Add(-freed)
	dir := filepath.Join(s.treeRoot(), encodePathName(taskID))
	if err := os.RemoveAll(dir); err != nil {
		return freed, fmt.Errorf("remove hardware io for task: %w", err)
	}
	return freed, nil
}

// removeEmptyDirs drops stream directories left empty by pruning so they are
// not reloaded as streams.
func (s *Store) removeEmptyDirs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, item := range s.streams {
		item.mu.Lock()
		if !item.removed && item.active == nil && len(item.closed) == 0 {
			if os.Remove(item.dir) == nil {
				item.removed = true
				delete(s.streams, key)
				_ = os.Remove(filepath.Dir(item.dir))
				_ = os.Remove(filepath.Dir(filepath.Dir(item.dir)))
			}
		}
		item.mu.Unlock()
	}
}

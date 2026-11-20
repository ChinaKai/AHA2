package hardwareio

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// rotateRetryBase and rotateRetryMax bound the back-off after a failed
	// rotation. Until the retry time the active segment keeps growing, so
	// a broken disk does not cost a 4 MiB compression on every append.
	rotateRetryBase = time.Second
	rotateRetryMax  = 5 * time.Minute
	// failureLogInterval rate-limits failure log lines per operation.
	failureLogInterval = time.Minute
)

// faultHooks lets tests inject failures at each step of a rotation. A nil
// hook means "no fault".
type faultHooks struct {
	sync         func() error
	compress     func() error
	rename       func() error
	closeActive  func() error
	removeActive func() error
}

func runFault(hook func() error) error {
	if hook == nil {
		return nil
	}
	return hook()
}

// failureLog counts write-path failures and remembers the last one.
type failureLog struct {
	mu          sync.Mutex
	total       int64
	byOperation map[string]int64
	lastError   string
	lastAt      time.Time
	lastLogged  map[string]time.Time
	suppressed  map[string]int64
}

// FailureStats is the failure part of Stats.
type FailureStats struct {
	Total       int64            `json:"total"`
	ByOperation map[string]int64 `json:"by_operation,omitempty"`
	LastError   string           `json:"last_error,omitempty"`
	LastAt      *time.Time       `json:"last_at,omitempty"`
}

// reportFailure records a failure and logs it at most once per minute per
// operation, with the number of reports suppressed since the last line.
func (s *Store) reportFailure(operation string, err error) {
	if err == nil {
		return
	}
	now := s.options.Now().UTC()
	log := &s.failures
	log.mu.Lock()
	if log.byOperation == nil {
		log.byOperation = make(map[string]int64)
		log.lastLogged = make(map[string]time.Time)
		log.suppressed = make(map[string]int64)
	}
	log.total++
	log.byOperation[operation]++
	log.lastError = operation + ": " + err.Error()
	log.lastAt = now
	emit := now.Sub(log.lastLogged[operation]) >= failureLogInterval
	suppressed := log.suppressed[operation]
	if emit {
		log.lastLogged[operation] = now
		log.suppressed[operation] = 0
	} else {
		log.suppressed[operation]++
	}
	log.mu.Unlock()
	if emit {
		s.options.Logger.Warn("hardware io storage failure",
			"operation", operation, "error", err, "suppressed_since_last_log", suppressed)
	}
}

func (s *Store) failureStats() FailureStats {
	log := &s.failures
	log.mu.Lock()
	defer log.mu.Unlock()
	result := FailureStats{Total: log.total, LastError: log.lastError}
	if len(log.byOperation) > 0 {
		result.ByOperation = make(map[string]int64, len(log.byOperation))
		for key, value := range log.byOperation {
			result.ByOperation[key] = value
		}
	}
	if !log.lastAt.IsZero() {
		value := log.lastAt
		result.LastAt = &value
	}
	return result
}

// rotateLocked turns the active segment into a compressed closed segment.
//
// Order matters for crash safety and for not losing records:
//  1. sync the active file and compress its committed bytes into a temp
//     file, fsync, rename into place. The active file stays open and
//     untouched, so any failure here leaves the stream exactly as it was:
//     the caller keeps appending to the active segment and the rotation is
//     retried after a back-off.
//  2. Only once the gz is installed is the segment registered as closed and
//     the active segment dropped from the stream. Closing the old handle
//     and removing the old file can still fail; those failures are
//     reported and the file is queued for removal, but no record is lost:
//     the gz holds all of them and nothing appends to the old file again.
//
// The caller holds the stream lock.
func (s *Store) rotateLocked(item *stream) error {
	active := item.active
	if active == nil {
		return nil
	}
	if active.rows == 0 {
		if active.file != nil {
			_ = active.file.Close()
		}
		_ = os.Remove(active.path)
		s.totalBytes.Add(-active.size)
		item.active = nil
		return nil
	}
	if err := s.syncActive(active); err != nil {
		return s.rotationFailed(item, "sync", err)
	}
	seg, err := s.compressFile(item.dir, active.path, active.size, active.first, active.last, active.lastUnix, active.rows)
	if err != nil {
		return s.rotationFailed(item, "compress", err)
	}
	item.active = nil
	item.closed = append(item.closed, seg)
	item.rotateFailures = 0
	item.retryAt = time.Time{}
	s.totalBytes.Add(seg.bytes - active.size)
	if active.file != nil {
		closeErr := runFault(s.faults.closeActive)
		if realErr := active.file.Close(); closeErr == nil {
			closeErr = realErr
		}
		active.file = nil
		if closeErr != nil {
			s.reportFailure("close", closeErr)
		}
	}
	if err := s.removeFile(active.path); err != nil {
		s.reportFailure("remove", err)
		item.stale = append(item.stale, active.path)
	}
	return nil
}

// rotationFailed records a failed rotation, schedules the retry and makes
// sure the active segment can still take appends.
func (s *Store) rotationFailed(item *stream, operation string, err error) error {
	item.rotateFailures++
	backoff := rotateRetryBase << min(item.rotateFailures-1, 16)
	if backoff <= 0 || backoff > rotateRetryMax {
		backoff = rotateRetryMax
	}
	item.retryAt = s.options.Now().Add(backoff)
	s.reportFailure(operation, err)
	if operation == "sync" && item.active != nil {
		// A handle that cannot sync may be broken; reopen it so appends do
		// not fail forever on a dead descriptor.
		if reopenErr := s.reopenActive(item.active); reopenErr != nil {
			s.reportFailure("reopen", reopenErr)
		}
	}
	return fmt.Errorf("rotate hardware io segment (%s): %w", operation, err)
}

func (s *Store) syncActive(active *activeSegment) error {
	if active.file == nil {
		return errors.New("active segment has no open handle")
	}
	if err := runFault(s.faults.sync); err != nil {
		return err
	}
	return active.file.Sync()
}

// reopenActive replaces the active segment's handle with a fresh append
// handle. On failure the handle is nil and the next append retries.
func (s *Store) reopenActive(active *activeSegment) error {
	if active.file != nil {
		_ = active.file.Close()
		active.file = nil
	}
	file, err := os.OpenFile(active.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("reopen hardware io segment: %w", err)
	}
	active.file = file
	return nil
}

// compressFile gzips the first size bytes of source into a closed segment
// and installs it. source is neither modified nor removed. The reader uses
// its own handle, so this works while the append handle is still open.
func (s *Store) compressFile(dir, source string, size, first, last, lastUnix, rows int64) (segment, error) {
	if err := runFault(s.faults.compress); err != nil {
		return segment{}, err
	}
	input, err := os.Open(source)
	if err != nil {
		return segment{}, fmt.Errorf("open hardware io segment: %w", err)
	}
	defer input.Close()
	final := filepath.Join(dir, closedSegmentName(first, last, lastUnix, rows))
	written, err := writeGzipAtomic(final, func(writer io.Writer) error {
		copied, copyErr := io.Copy(writer, io.NewSectionReader(input, 0, size))
		if copyErr == nil && copied != size {
			copyErr = fmt.Errorf("short read: %d of %d bytes", copied, size)
		}
		return copyErr
	}, s.faults.rename)
	if err != nil {
		return segment{}, err
	}
	return segment{first: first, last: last, lastUnix: lastUnix, rows: rows, bytes: written, path: final}, nil
}

func (s *Store) removeFile(path string) error {
	if err := runFault(s.faults.removeActive); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// plainSegment registers an uncompressed file as a readable closed segment.
func plainSegment(active *activeSegment) segment {
	if active.file != nil {
		_ = active.file.Close()
		active.file = nil
	}
	return segment{
		first: active.first, last: active.last, lastUnix: active.lastUnix, rows: active.rows,
		bytes: active.size, path: active.path, plain: true,
	}
}

// retryPendingLocked compresses plain closed segments and removes stale
// files left by earlier failures. The caller holds the stream lock.
func (s *Store) retryPendingLocked(item *stream) (compressed int) {
	for index, seg := range item.closed {
		if !seg.plain {
			continue
		}
		installed, err := s.compressFile(item.dir, seg.path, seg.bytes, seg.first, seg.last, seg.lastUnix, seg.rows)
		if err != nil {
			s.reportFailure("compress", err)
			continue
		}
		item.closed[index] = installed
		s.totalBytes.Add(installed.bytes - seg.bytes)
		compressed++
		if err := s.removeFile(seg.path); err != nil {
			s.reportFailure("remove", err)
			item.stale = append(item.stale, seg.path)
		}
	}
	kept := item.stale[:0]
	for _, path := range item.stale {
		if err := s.removeFile(path); err != nil {
			kept = append(kept, path)
		}
	}
	item.stale = kept
	return compressed
}

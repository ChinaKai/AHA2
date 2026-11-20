// Package hardwareio stores hardware terminal IO (serial/network/SSH/ADB
// output and the commands sent to it) in append-only segment files instead of
// SQLite.
//
// Layout under Root:
//
//	sequence.json                         sequence reservation (high watermark)
//	v1/<task>/<hardware>/<transport>/     one directory per stream
//	    seg-<first>.active.jsonl          the segment being appended to
//	    seg-<first>-<last>-<lastUnix>-<rows>.jsonl.gz   closed, compressed
//
// Each line of a segment is one JSON record (see wireRecord). A segment is
// closed when it reaches MaxSegmentBytes or when the UTC day changes, so
// retention can drop whole files. Closed segments are gzip-compressed before
// they are renamed into place; a crash leaves either the active file or the
// complete compressed file, never a half-written closed segment.
//
// Sequences are global and strictly increasing across every stream. They
// continue after the largest legacy SQLite sequence (SeedSequence) so the
// cursors used by the Web terminal, WebSocket replay and Agent tools stay
// valid across the switch. A reservation file keeps sequences from ever being
// reused, even after every segment has been deleted.
package hardwareio

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	// DefaultMaxSegmentBytes is the uncompressed size at which the active
	// segment of a stream is closed and compressed.
	DefaultMaxSegmentBytes = 4 << 20
	// sequenceReserveBlock is how many sequences one reservation write covers.
	sequenceReserveBlock = 4096
	layoutVersion        = "v1"
	activeSuffix         = ".active.jsonl"
	closedSuffix         = ".jsonl.gz"
	tempSuffix           = ".tmp"
	segmentPrefix        = "seg-"
	sequenceFileName     = "sequence.json"
	// maxLineBytes bounds one record line when scanning a segment. A record
	// carries at most one 4 KiB terminal read; JSON escaping can grow it about
	// six times, so 1 MiB leaves a wide margin.
	maxLineBytes = 1 << 20
)

// ErrClosed is returned after Close.
var ErrClosed = errors.New("hardware io store is closed")

// Record is one terminal IO event. CreatedAt is kept as the exact string that
// was written (RFC3339Nano UTC for new records, the original SQLite text for
// migrated ones) so a migration round trip is byte-for-byte verifiable.
type Record struct {
	Sequence   int64
	ID         string
	TaskID     string
	HardwareID string
	Transport  string
	Direction  string
	Data       string
	Encoding   string
	Source     string
	CreatedAt  string
}

// StreamKey identifies the stream a record belongs to.
type StreamKey struct {
	TaskID     string
	HardwareID string
	Transport  string
}

func (r Record) Key() StreamKey {
	return StreamKey{TaskID: r.TaskID, HardwareID: r.HardwareID, Transport: r.Transport}
}

// wireRecord is the on-disk JSON form. The stream key is implied by the
// directory. Data that is not valid UTF-8 is stored base64-encoded in B
// because encoding/json would otherwise replace invalid bytes with U+FFFD.
type wireRecord struct {
	Sequence  int64  `json:"s"`
	ID        string `json:"id"`
	Direction string `json:"d"`
	Data      string `json:"t,omitempty"`
	DataB64   string `json:"b,omitempty"`
	Encoding  string `json:"e,omitempty"`
	Source    string `json:"src,omitempty"`
	CreatedAt string `json:"at"`
}

func encodeRecord(record Record) ([]byte, error) {
	wire := wireRecord{
		Sequence: record.Sequence, ID: record.ID, Direction: record.Direction,
		Encoding: record.Encoding, Source: record.Source, CreatedAt: record.CreatedAt,
	}
	if utf8.ValidString(record.Data) {
		wire.Data = record.Data
	} else {
		wire.DataB64 = base64.StdEncoding.EncodeToString([]byte(record.Data))
	}
	line, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	return append(line, '\n'), nil
}

func decodeRecord(key StreamKey, line []byte) (Record, error) {
	var wire wireRecord
	if err := json.Unmarshal(line, &wire); err != nil {
		return Record{}, err
	}
	data := wire.Data
	if wire.DataB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(wire.DataB64)
		if err != nil {
			return Record{}, fmt.Errorf("decode record %d data: %w", wire.Sequence, err)
		}
		data = string(raw)
	}
	return Record{
		Sequence: wire.Sequence, ID: wire.ID, TaskID: key.TaskID, HardwareID: key.HardwareID,
		Transport: key.Transport, Direction: wire.Direction, Data: data, Encoding: wire.Encoding,
		Source: wire.Source, CreatedAt: wire.CreatedAt,
	}, nil
}

// Options configures a Store.
type Options struct {
	// Root is the directory that holds sequence.json and the v1 tree.
	Root string
	// SeedSequence returns the largest sequence already handed out by the
	// legacy SQLite table. It is called once, on first use.
	SeedSequence func() (int64, error)
	// MaxSegmentBytes closes the active segment once it reaches this size.
	MaxSegmentBytes int64
	// MaxBytes is the disk cap. When an append pushes the store above it,
	// CapacitySignal fires so the owner can prune before the hourly pass.
	MaxBytes int64
	// Now overrides the clock for tests.
	Now func() time.Time
	// Logger receives rate-limited failure reports; nil uses slog.Default.
	Logger *slog.Logger
}

// Store is safe for concurrent use. Only one Store may own a Root at a time;
// AHA2 guarantees that with the data directory instance lock.
type Store struct {
	options Options

	initMu   sync.Mutex
	initDone bool
	initErr  error

	mu      sync.Mutex
	streams map[StreamKey]*stream
	closed  bool

	// corruptLines counts lines readers skipped because they could not be
	// decoded (external damage); a damaged line must not fail the read path.
	corruptLines atomic.Int64

	// totalBytes tracks the on-disk size of every segment (active ones
	// uncompressed) so a cap overrun is noticed on the write path.
	totalBytes atomic.Int64
	capSignal  chan struct{}

	seqMu    sync.Mutex
	next     int64 // last sequence handed out
	reserved int64 // largest sequence covered by sequence.json

	// failures records write-path problems (see reportFailure) so they are
	// visible in Stats and the log instead of being swallowed.
	failures failureLog
	// faults injects errors in tests; every hook is nil in production.
	faults faultHooks
}

type segment struct {
	first, last int64
	lastUnix    int64
	rows        int64
	bytes       int64
	path        string
	// plain marks a closed segment that is still an uncompressed JSONL file
	// because compressing it failed; readers scan it directly and
	// maintenance retries the compression.
	plain bool
}

type activeSegment struct {
	file     *os.File
	path     string
	first    int64
	last     int64
	firstDay string
	lastUnix int64
	rows     int64
	size     int64
	// marks is a sparse index of the active segment: the line offset of
	// every markEvery-th record, so cursor reads seek instead of scanning
	// the whole file on every poll.
	marks []mark
}

type mark struct {
	sequence int64
	offset   int64
}

// markEvery is the record spacing of the active segment's sparse index.
const markEvery = 128

func (active *activeSegment) addMark(sequence int64) {
	if active.rows%markEvery == 0 {
		active.marks = append(active.marks, mark{sequence: sequence, offset: active.size})
	}
}

type stream struct {
	mu      sync.RWMutex
	key     StreamKey
	dir     string
	closed  []segment // sorted by first
	active  *activeSegment
	lastSeq int64
	removed bool
	// rotateFailures and retryAt back off a failing rotation: the active
	// segment keeps taking appends and the rotation is retried later
	// instead of on every append.
	rotateFailures int
	retryAt        time.Time
	// stale lists files whose data is already held by a closed segment but
	// that could not be removed yet (for example a Windows sharing
	// violation). Maintenance retries the removal.
	stale []string
}

// Open returns a Store. Nothing touches the disk until first use.
func Open(options Options) *Store {
	if options.MaxSegmentBytes <= 0 {
		options.MaxSegmentBytes = DefaultMaxSegmentBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Store{options: options, streams: make(map[StreamKey]*stream), capSignal: make(chan struct{}, 1)}
}

// Root returns the configured root directory.
func (s *Store) Root() string { return s.options.Root }

func (s *Store) treeRoot() string { return filepath.Join(s.options.Root, layoutVersion) }

// ensureInit loads every existing stream once, repairs crash leftovers and
// seeds the sequence allocator.
func (s *Store) ensureInit() error {
	s.initMu.Lock()
	defer s.initMu.Unlock()
	if s.initDone {
		return s.initErr
	}
	s.initErr = s.initialize()
	if s.initErr == nil {
		s.initDone = true
	}
	return s.initErr
}

func (s *Store) initialize() error {
	if s.options.Root == "" {
		return errors.New("hardware io root is required")
	}
	if err := os.MkdirAll(s.treeRoot(), 0o700); err != nil {
		return fmt.Errorf("create hardware io directory: %w", err)
	}
	keys, err := s.listStreamKeys()
	if err != nil {
		return err
	}
	var maximum int64
	for _, key := range keys {
		item, err := s.loadStream(key)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.streams[key] = item
		s.mu.Unlock()
		s.totalBytes.Add(item.diskBytes())
		maximum = max(maximum, item.lastSeq)
	}
	reserved, err := s.readReservation()
	if err != nil {
		return err
	}
	maximum = max(maximum, reserved)
	if s.options.SeedSequence != nil {
		seed, err := s.options.SeedSequence()
		if err != nil {
			return fmt.Errorf("seed hardware io sequence: %w", err)
		}
		maximum = max(maximum, seed)
	}
	// Persist the floor right away: the legacy table may be emptied after
	// this point, and the reservation file must still keep new sequences
	// above every cursor a client could hold.
	if maximum > reserved {
		if err := s.writeReservation(maximum); err != nil {
			return fmt.Errorf("record hardware io sequence floor: %w", err)
		}
	}
	s.seqMu.Lock()
	s.next = maximum
	s.reserved = maximum
	s.seqMu.Unlock()
	return nil
}

type reservationFile struct {
	Reserved int64 `json:"reserved"`
}

func (s *Store) readReservation() (int64, error) {
	raw, err := os.ReadFile(filepath.Join(s.options.Root, sequenceFileName))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read hardware io sequence: %w", err)
	}
	var value reservationFile
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("parse hardware io sequence: %w", err)
	}
	return value.Reserved, nil
}

func (s *Store) writeReservation(value int64) error {
	raw, err := json.Marshal(reservationFile{Reserved: value})
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.options.Root, sequenceFileName), raw)
}

// allocate hands out the next sequence. It reserves blocks ahead in
// sequence.json so a restart never reuses a sequence.
func (s *Store) allocate() (int64, error) {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()
	if s.next+1 > s.reserved {
		target := s.next + sequenceReserveBlock
		if err := s.writeReservation(target); err != nil {
			return 0, fmt.Errorf("reserve hardware io sequence: %w", err)
		}
		s.reserved = target
	}
	s.next++
	return s.next, nil
}

// LastSequence reports the last sequence handed out (0 before first use).
func (s *Store) LastSequence() (int64, error) {
	if err := s.ensureInit(); err != nil {
		return 0, err
	}
	s.seqMu.Lock()
	defer s.seqMu.Unlock()
	return s.next, nil
}

// Append stores one record. The sequence is allocated here and returned; the
// caller's Sequence field is ignored.
func (s *Store) Append(record Record) (int64, error) {
	if err := s.ensureInit(); err != nil {
		return 0, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		item, err := s.stream(record.Key(), true)
		if err != nil {
			return 0, err
		}
		item.mu.Lock()
		if item.removed {
			item.mu.Unlock()
			continue
		}
		sequence, err := s.appendLocked(item, record)
		item.mu.Unlock()
		return sequence, err
	}
	return 0, errors.New("hardware io stream was removed while appending")
}

func (s *Store) appendLocked(item *stream, record Record) (int64, error) {
	now := s.options.Now().UTC()
	createdAt := record.CreatedAt
	if createdAt == "" {
		createdAt = now.Format(time.RFC3339Nano)
	}
	created := parseRecordTime(createdAt, now)
	day := created.Format("2006-01-02")
	if item.active != nil && (item.active.size >= s.options.MaxSegmentBytes || item.active.firstDay != day) && !now.Before(item.retryAt) {
		// A failed rotation leaves the active segment intact; this record
		// is still appended to it and the rotation is retried after a
		// back-off (rotateLocked reports the failure).
		_ = s.rotateLocked(item)
	}
	if item.active != nil && item.active.file == nil {
		if err := s.reopenActive(item.active); err != nil {
			s.reportFailure("reopen", err)
			return 0, err
		}
	}
	sequence, err := s.allocate()
	if err != nil {
		return 0, err
	}
	record.Sequence = sequence
	record.CreatedAt = createdAt
	line, err := encodeRecord(record)
	if err != nil {
		return 0, err
	}
	if item.active == nil {
		if err := os.MkdirAll(item.dir, 0o700); err != nil {
			return 0, fmt.Errorf("create hardware io stream: %w", err)
		}
		path := filepath.Join(item.dir, fmt.Sprintf("%s%020d%s", segmentPrefix, sequence, activeSuffix))
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			err = fmt.Errorf("open hardware io segment: %w", err)
			s.reportFailure("append", err)
			return 0, err
		}
		item.active = &activeSegment{file: file, path: path, first: sequence, firstDay: day}
	}
	active := item.active
	if written, err := active.file.Write(line); err != nil {
		// Roll back a partial line so the file stays line-aligned. On
		// Windows an append-only handle may not be allowed to truncate, so
		// fall back to a separate read-write handle.
		if written > 0 {
			if truncateErr := active.file.Truncate(active.size); truncateErr != nil {
				if repair, openErr := os.OpenFile(active.path, os.O_RDWR, 0o600); openErr == nil {
					_ = repair.Truncate(active.size)
					_ = repair.Close()
				}
			}
		}
		err = fmt.Errorf("write hardware io segment: %w", err)
		s.reportFailure("append", err)
		return 0, err
	}
	active.addMark(sequence)
	active.size += int64(len(line))
	if total := s.totalBytes.Add(int64(len(line))); s.options.MaxBytes > 0 && total > s.options.MaxBytes {
		select {
		case s.capSignal <- struct{}{}:
		default:
		}
	}
	active.rows++
	active.last = sequence
	active.lastUnix = created.Unix()
	item.lastSeq = sequence
	return sequence, nil
}

func parseRecordTime(value string, fallback time.Time) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fallback.UTC()
	}
	return parsed.UTC()
}

func closedSegmentName(first, last, lastUnix, rows int64) string {
	return fmt.Sprintf("%s%020d-%020d-%d-%d%s", segmentPrefix, first, last, lastUnix, rows, closedSuffix)
}

// writeGzipAtomic writes a gzip file via a temp file, fsync and rename.
func writeGzipAtomic(final string, fill func(io.Writer) error, renameFault func() error) (int64, error) {
	temp := final + tempSuffix
	output, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create hardware io segment: %w", err)
	}
	cleanup := func() {
		_ = output.Close()
		_ = os.Remove(temp)
	}
	buffered := bufio.NewWriterSize(output, 256<<10)
	compressor, err := gzip.NewWriterLevel(buffered, gzip.BestSpeed)
	if err != nil {
		cleanup()
		return 0, err
	}
	if err := fill(compressor); err != nil {
		cleanup()
		return 0, fmt.Errorf("compress hardware io segment: %w", err)
	}
	if err := compressor.Close(); err != nil {
		cleanup()
		return 0, err
	}
	if err := buffered.Flush(); err != nil {
		cleanup()
		return 0, err
	}
	if err := output.Sync(); err != nil {
		cleanup()
		return 0, err
	}
	info, err := output.Stat()
	if err != nil {
		cleanup()
		return 0, err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(temp)
		return 0, err
	}
	renameErr := runFault(renameFault)
	if renameErr == nil {
		renameErr = os.Rename(temp, final)
	}
	if renameErr != nil {
		_ = os.Remove(temp)
		return 0, fmt.Errorf("install hardware io segment: %w", renameErr)
	}
	return info.Size(), nil
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp := path + tempSuffix
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return os.Rename(temp, path)
}

// stream returns the in-memory stream, loading it from disk on first access.
// With create=false a stream that has no directory returns nil.
func (s *Store) stream(key StreamKey, create bool) (*stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if item, ok := s.streams[key]; ok {
		return item, nil
	}
	dir, err := s.streamDir(key)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		item, loadErr := s.loadStream(key)
		if loadErr != nil {
			return nil, loadErr
		}
		s.streams[key] = item
		s.totalBytes.Add(item.diskBytes())
		return item, nil
	}
	if !create {
		return nil, nil
	}
	item := &stream{key: key, dir: dir}
	s.streams[key] = item
	return item, nil
}

func (s *Store) streamDir(key StreamKey) (string, error) {
	if key.TaskID == "" || key.HardwareID == "" || key.Transport == "" {
		return "", errors.New("hardware io stream key is incomplete")
	}
	return filepath.Join(s.treeRoot(), encodePathName(key.TaskID), encodePathName(key.HardwareID), encodePathName(key.Transport)), nil
}

func (s *Store) listStreamKeys() ([]StreamKey, error) {
	var keys []StreamKey
	tasks, err := os.ReadDir(s.treeRoot())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, task := range tasks {
		taskID, ok := decodePathName(task.Name())
		if !task.IsDir() || !ok {
			continue
		}
		hardware, err := os.ReadDir(filepath.Join(s.treeRoot(), task.Name()))
		if err != nil {
			return nil, err
		}
		for _, hw := range hardware {
			hardwareID, ok := decodePathName(hw.Name())
			if !hw.IsDir() || !ok {
				continue
			}
			transports, err := os.ReadDir(filepath.Join(s.treeRoot(), task.Name(), hw.Name()))
			if err != nil {
				return nil, err
			}
			for _, transport := range transports {
				name, ok := decodePathName(transport.Name())
				if !transport.IsDir() || !ok {
					continue
				}
				keys = append(keys, StreamKey{TaskID: taskID, HardwareID: hardwareID, Transport: name})
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	return keys, nil
}

func (k StreamKey) less(other StreamKey) bool {
	if k.TaskID != other.TaskID {
		return k.TaskID < other.TaskID
	}
	if k.HardwareID != other.HardwareID {
		return k.HardwareID < other.HardwareID
	}
	return k.Transport < other.Transport
}

// loadStream reads a stream directory: it removes temp files, keeps one gz
// per first sequence (the one reaching furthest), resolves a gz and an
// active file with the same first by their last sequence (the gz wins when
// it holds every record, otherwise the active file wins and the outdated gz
// is removed), truncates a torn last line, and reopens the newest active
// segment of today for appending. Older actives are compressed; a file that
// cannot be compressed or reopened stays readable as a plain segment.
func (s *Store) loadStream(key StreamKey) (*stream, error) {
	dir, err := s.streamDir(key)
	if err != nil {
		return nil, err
	}
	item := &stream{key: key, dir: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read hardware io stream: %w", err)
	}
	var actives []string
	// One closed segment per first sequence. Two gz files with the same
	// first can only come from an interrupted re-compression; the one that
	// reaches further holds a superset of the other.
	closedByFirst := make(map[int64]segment)
	discard := func(path string) {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			item.stale = append(item.stale, path)
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, tempSuffix):
			_ = os.Remove(filepath.Join(dir, name))
		case strings.HasSuffix(name, activeSuffix):
			actives = append(actives, name)
		case strings.HasSuffix(name, closedSuffix):
			seg, ok := parseClosedName(name)
			if !ok {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			seg.bytes = info.Size()
			seg.path = filepath.Join(dir, name)
			if existing, found := closedByFirst[seg.first]; found {
				keep, drop := existing, seg
				if seg.last > existing.last || (seg.last == existing.last && seg.rows > existing.rows) {
					keep, drop = seg, existing
				}
				closedByFirst[seg.first] = keep
				discard(drop.path)
				continue
			}
			closedByFirst[seg.first] = seg
		}
	}
	sort.Strings(actives)
	today := s.options.Now().UTC().Format("2006-01-02")
	for index, name := range actives {
		first, ok := parseActiveName(name)
		path := filepath.Join(dir, name)
		if !ok {
			continue
		}
		active, err := scanActive(key, path, first)
		if err != nil {
			// Unreadable: leave the file for inspection and keep the rest
			// of the stream usable.
			s.reportFailure("load", err)
			continue
		}
		if existing, found := closedByFirst[first]; found {
			if existing.last >= active.last {
				// The compressed copy already holds every record (a crash
				// or a failed removal after the gz was installed).
				discard(path)
				continue
			}
			// The active file went on past the gz (an older build kept
			// appending after a failed rotation): the active file wins.
			delete(closedByFirst, first)
			discard(existing.path)
		}
		if active.rows == 0 {
			discard(path)
			continue
		}
		if index < len(actives)-1 || active.firstDay != today {
			seg, err := s.compressFile(dir, path, active.size, active.first, active.last, active.lastUnix, active.rows)
			if err != nil {
				s.reportFailure("compress", err)
				closedByFirst[first] = plainSegment(active)
				continue
			}
			closedByFirst[first] = seg
			if err := s.removeFile(path); err != nil {
				s.reportFailure("remove", err)
				item.stale = append(item.stale, path)
			}
			continue
		}
		if err := s.reopenActive(active); err != nil {
			s.reportFailure("reopen", err)
			closedByFirst[first] = plainSegment(active)
			continue
		}
		item.active = active
	}
	for _, seg := range closedByFirst {
		item.closed = append(item.closed, seg)
	}
	sort.Slice(item.closed, func(i, j int) bool { return item.closed[i].first < item.closed[j].first })
	for _, seg := range item.closed {
		item.lastSeq = max(item.lastSeq, seg.last)
	}
	if item.active != nil {
		item.lastSeq = max(item.lastSeq, item.active.last)
	}
	return item, nil
}

// scanActive counts records and truncates a torn tail left by a crash.
func scanActive(key StreamKey, path string, first int64) (*activeSegment, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open hardware io segment: %w", err)
	}
	defer file.Close()
	active := &activeSegment{path: path, first: first}
	reader := bufio.NewReaderSize(file, 64<<10)
	var offset int64
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			record, decodeErr := decodeRecord(key, bytes.TrimRight(line, "\n"))
			if decodeErr != nil {
				// A complete but damaged line is kept; readers skip and
				// count it. Only an unterminated tail is a torn write.
				offset += int64(len(line))
				continue
			}
			created := parseRecordTime(record.CreatedAt, time.Unix(0, 0))
			if active.rows == 0 {
				active.firstDay = created.Format("2006-01-02")
			}
			active.size = offset
			active.addMark(record.Sequence)
			active.rows++
			active.last = record.Sequence
			active.lastUnix = created.Unix()
			offset += int64(len(line))
			continue
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, readErr
		}
		break
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() != offset {
		if err := file.Truncate(offset); err != nil {
			return nil, fmt.Errorf("repair hardware io segment: %w", err)
		}
	}
	active.size = offset
	return active, nil
}

func parseActiveName(name string) (int64, bool) {
	if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, activeSuffix) {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), activeSuffix), 10, 64)
	return value, err == nil && value > 0
}

func parseClosedName(name string) (segment, bool) {
	if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, closedSuffix) {
		return segment{}, false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), closedSuffix), "-")
	if len(parts) != 4 {
		return segment{}, false
	}
	values := make([]int64, 4)
	for index, part := range parts {
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return segment{}, false
		}
		values[index] = value
	}
	if values[0] <= 0 || values[1] < values[0] || values[3] <= 0 {
		return segment{}, false
	}
	return segment{first: values[0], last: values[1], lastUnix: values[2], rows: values[3]}, true
}

// diskBytes is the stream's current size on disk. Callers hold its lock or
// own it exclusively.
func (item *stream) diskBytes() int64 {
	var total int64
	for _, seg := range item.closed {
		total += seg.bytes
	}
	if item.active != nil {
		total += item.active.size
	}
	return total
}

// CapacitySignal fires (coalesced) when an append pushes the store above
// Options.MaxBytes.
func (s *Store) CapacitySignal() <-chan struct{} { return s.capSignal }

// TotalBytes is the tracked on-disk size of every segment.
func (s *Store) TotalBytes() int64 { return s.totalBytes.Load() }

// Close flushes and closes every active segment. Active segments stay as
// they are and are reopened (or compressed) by the next Open.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	streams := make([]*stream, 0, len(s.streams))
	for _, item := range s.streams {
		streams = append(streams, item)
	}
	s.mu.Unlock()
	var first error
	for _, item := range streams {
		item.mu.Lock()
		if item.active != nil && item.active.file != nil {
			if err := item.active.file.Sync(); err != nil && first == nil {
				first = err
			}
			if err := item.active.file.Close(); err != nil && first == nil {
				first = err
			}
			item.active.file = nil
		}
		item.removed = true
		item.mu.Unlock()
	}
	return first
}

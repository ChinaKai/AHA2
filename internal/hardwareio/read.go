package hardwareio

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync/atomic"
)

// After returns up to limit records of a stream with Sequence > after, in
// sequence order, plus the stream's latest sequence.
func (s *Store) After(key StreamKey, after int64, limit int) ([]Record, int64, error) {
	if err := s.ensureInit(); err != nil {
		return nil, 0, err
	}
	item, err := s.stream(key, false)
	if err != nil || item == nil {
		return []Record{}, 0, err
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	if item.removed {
		return []Record{}, 0, nil
	}
	// The common poll: the client is already at the head of the stream.
	if after >= item.lastSeq {
		return []Record{}, item.lastSeq, nil
	}
	records := make([]Record, 0, min(limit, 256))
	visit := func(record Record) bool {
		if record.Sequence <= after {
			return true
		}
		records = append(records, record)
		return len(records) < limit
	}
	for _, seg := range item.closed {
		if seg.last <= after {
			continue
		}
		more, err := readClosed(&s.corruptLines, key, seg.path, after, visit)
		if err != nil {
			return nil, 0, err
		}
		if !more {
			return records, item.lastSeq, nil
		}
	}
	if item.active != nil && item.active.last > after {
		if _, err := readActive(&s.corruptLines, key, item.active, activeOffsetAfter(item.active, after), after, visit); err != nil {
			return nil, 0, err
		}
	}
	return records, item.lastSeq, nil
}

// activeOffsetAfter returns the offset of the last indexed line whose
// sequence is <= after, so the scan starts just before the first wanted line.
func activeOffsetAfter(active *activeSegment, after int64) int64 {
	index := sort.Search(len(active.marks), func(i int) bool { return active.marks[i].sequence > after })
	if index == 0 {
		return 0
	}
	return active.marks[index-1].offset
}

// Latest returns the newest limit records of a stream in sequence order, plus
// the stream's latest sequence.
func (s *Store) Latest(key StreamKey, limit int) ([]Record, int64, error) {
	if err := s.ensureInit(); err != nil {
		return nil, 0, err
	}
	item, err := s.stream(key, false)
	if err != nil || item == nil {
		return []Record{}, 0, err
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	if item.removed {
		return []Record{}, 0, nil
	}
	// Walk segments newest first; each one is read whole and prepended.
	var chunks [][]Record
	total := 0
	collect := func(read func(func(Record) bool) error) error {
		chunk := make([]Record, 0, 64)
		if err := read(func(record Record) bool {
			chunk = append(chunk, record)
			return true
		}); err != nil {
			return err
		}
		chunks = append(chunks, chunk)
		total += len(chunk)
		return nil
	}
	if item.active != nil && item.active.rows > 0 {
		active := item.active
		// Start at the indexed line at or before the first wanted record.
		var offset int64
		if skip := active.rows - int64(limit); skip > 0 {
			offset = active.marks[min(int(skip/markEvery), len(active.marks)-1)].offset
		}
		if err := collect(func(visit func(Record) bool) error {
			_, err := readActive(&s.corruptLines, key, active, offset, 0, visit)
			return err
		}); err != nil {
			return nil, 0, err
		}
	}
	for index := len(item.closed) - 1; index >= 0 && total < limit; index-- {
		path := item.closed[index].path
		if err := collect(func(visit func(Record) bool) error {
			_, err := readClosed(&s.corruptLines, key, path, 0, visit)
			return err
		}); err != nil {
			return nil, 0, err
		}
	}
	records := make([]Record, 0, min(total, limit))
	for index := len(chunks) - 1; index >= 0; index-- {
		records = append(records, chunks[index]...)
	}
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	return records, item.lastSeq, nil
}

// ReadStream calls visit for every record of a stream in sequence order.
func (s *Store) ReadStream(key StreamKey, visit func(Record) bool) error {
	if err := s.ensureInit(); err != nil {
		return err
	}
	item, err := s.stream(key, false)
	if err != nil || item == nil {
		return err
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	for _, seg := range item.closed {
		more, err := readClosed(&s.corruptLines, key, seg.path, 0, visit)
		if err != nil || !more {
			return err
		}
	}
	if item.active != nil {
		_, err := readActive(&s.corruptLines, key, item.active, 0, 0, visit)
		return err
	}
	return nil
}

func readClosed(corrupt *atomic.Int64, key StreamKey, path string, skipThrough int64, visit func(Record) bool) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open hardware io segment: %w", err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(bufio.NewReaderSize(file, 64<<10))
	if err != nil {
		return false, fmt.Errorf("read hardware io segment %s: %w", path, err)
	}
	defer reader.Close()
	return scanLines(corrupt, key, reader, skipThrough, visit)
}

// readActive reads the committed part of the active segment from offset (a
// line start). The caller holds the stream lock, so no append is in flight.
func readActive(corrupt *atomic.Int64, key StreamKey, active *activeSegment, offset, skipThrough int64, visit func(Record) bool) (bool, error) {
	file, err := os.Open(active.path)
	if err != nil {
		return false, fmt.Errorf("open hardware io segment: %w", err)
	}
	defer file.Close()
	if offset < 0 || offset > active.size {
		offset = 0
	}
	return scanLines(corrupt, key, io.NewSectionReader(file, offset, active.size-offset), skipThrough, visit)
}

// linePrefix is how every encoded record starts: Sequence is the first field
// of wireRecord, so the sequence can be read without decoding the line.
var linePrefix = []byte(`{"s":`)

// lineSequence reads the leading sequence of an encoded line.
func lineSequence(line []byte) (int64, bool) {
	if !bytes.HasPrefix(line, linePrefix) {
		return 0, false
	}
	var value int64
	digits := 0
	for _, char := range line[len(linePrefix):] {
		if char < '0' || char > '9' {
			break
		}
		value = value*10 + int64(char-'0')
		digits++
	}
	return value, digits > 0
}

// scanLines decodes records in order. Lines with a sequence <= skipThrough
// are skipped without a JSON decode.
func scanLines(corrupt *atomic.Int64, key StreamKey, reader io.Reader, skipThrough int64, visit func(Record) bool) (bool, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if skipThrough > 0 {
			if sequence, ok := lineSequence(line); ok && sequence <= skipThrough {
				continue
			}
		}
		record, err := decodeRecord(key, line)
		if err != nil {
			// Damaged input is skipped and counted (Stats.CorruptLines)
			// instead of failing every read of the stream.
			corrupt.Add(1)
			continue
		}
		if !visit(record) {
			return false, nil
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("scan hardware io segment: %w", err)
	}
	return true, nil
}

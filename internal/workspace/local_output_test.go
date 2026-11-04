package workspace

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// io.WriteString prefers StringWriter over Writer, so an embedded
// bytes.Buffer would otherwise capture the whole line stream without applying
// OutputLimit.
func TestOutputBufferWriteStringKeepsTail(t *testing.T) {
	tests := []struct {
		name   string
		limit  int
		writes []string
		want   string
	}{
		{name: "over limit", limit: 4, writes: []string{"abcdefgh"}, want: "efgh"},
		{name: "exactly limit", limit: 5, writes: []string{"abcde"}, want: "abcde"},
		{name: "under limit", limit: 10, writes: []string{"abc"}, want: "abc"},
		{name: "cumulative over limit", limit: 5, writes: []string{"abc", "defgh"}, want: "defgh"},
		{name: "large write discards previous content", limit: 4, writes: []string{"xx", "abcdefgh"}, want: "efgh"},
		{name: "zero limit is unlimited", limit: 0, writes: []string{"abc", "defgh"}, want: "abcdefgh"},
		{name: "negative limit is unlimited", limit: -3, writes: []string{"abc", "defgh"}, want: "abcdefgh"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buffer := newOutputBuffer(test.limit)
			for _, value := range test.writes {
				written, err := io.WriteString(buffer, value)
				if err != nil {
					t.Fatalf("WriteString(%q): %v", value, err)
				}
				if written != len(value) {
					t.Fatalf("WriteString(%q) wrote %d, want %d", value, written, len(value))
				}
			}
			if got := buffer.String(); got != test.want {
				t.Fatalf("content = %q, want %q", got, test.want)
			}
		})
	}
}

// plainReader hides any WriterTo method (strings.Reader has one), so io.Copy
// falls through to the destination's ReadFrom.
type plainReader struct{ source io.Reader }

func (reader plainReader) Read(buffer []byte) (int, error) { return reader.source.Read(buffer) }

// io.Copy falls back to ReaderFrom when the source cannot WriteTo, so ReadFrom
// must enforce the same limit as Write.
func TestOutputBufferReadFromKeepsTail(t *testing.T) {
	tests := []struct {
		name    string
		limit   int
		prefix  string
		content string
		want    string
	}{
		{name: "over limit", limit: 5, content: "abcdefghij", want: "fghij"},
		{name: "exactly limit", limit: 5, content: "abcde", want: "abcde"},
		{name: "under limit", limit: 10, content: "abc", want: "abc"},
		{name: "discards previous content", limit: 4, prefix: "xx", content: "abcdefgh", want: "efgh"},
		{name: "zero limit is unlimited", limit: 0, prefix: "xx", content: "abcdefgh", want: "xxabcdefgh"},
		{name: "negative limit is unlimited", limit: -2, content: "abcdefgh", want: "abcdefgh"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buffer := newOutputBuffer(test.limit)
			if test.prefix != "" {
				if _, err := io.WriteString(buffer, test.prefix); err != nil {
					t.Fatalf("write prefix: %v", err)
				}
			}
			written, err := io.Copy(buffer, plainReader{source: strings.NewReader(test.content)})
			if err != nil {
				t.Fatalf("io.Copy: %v", err)
			}
			if written != int64(len(test.content)) {
				t.Fatalf("io.Copy wrote %d bytes, want %d", written, len(test.content))
			}
			if got := buffer.String(); got != test.want {
				t.Fatalf("content = %q, want %q", got, test.want)
			}
		})
	}
}

// WriteByte and WriteRune are promoted from bytes.Buffer too; they must not
// append past the limit.
func TestOutputBufferByteAndRuneWritesKeepTail(t *testing.T) {
	buffer := newOutputBuffer(3)
	if _, err := io.WriteString(buffer, "ab"); err != nil {
		t.Fatalf("write string: %v", err)
	}
	if err := buffer.WriteByte('c'); err != nil {
		t.Fatalf("write byte: %v", err)
	}
	if _, err := buffer.WriteRune('é'); err != nil {
		t.Fatalf("write rune: %v", err)
	}
	if got, want := buffer.String(), "cé"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

// The production LocalRunner path must keep only the tail of the output while
// still delivering every line to the handler.
func TestRunProcessAppliesOutputLimit(t *testing.T) {
	const limit = 1000
	full := processOutputLimitOutput()
	wantLines := strings.Split(strings.TrimSuffix(full, "\n"), "\n")
	var received []string
	result, err := executeProcessHelper(t, "output-limit", 0, func(command *Command) {
		command.OutputLimit = limit
	}, func(line string) { received = append(received, line) })
	if err != nil {
		t.Fatalf("run helper: %v", err)
	}
	if got, want := result.Stdout, full[len(full)-limit:]; got != want {
		t.Fatalf("stdout = %q, want last %d bytes %q", got, limit, want)
	}
	if len(received) != len(wantLines) {
		t.Fatalf("line handler received %d lines, want %d", len(received), len(wantLines))
	}
	for index, line := range wantLines {
		if received[index] != line {
			t.Fatalf("line handler line %d = %q, want %q", index, received[index], line)
		}
	}
}

func TestScanOutputAppliesOutputLimit(t *testing.T) {
	lines := make([]string, 200)
	for index := range lines {
		lines[index] = fmt.Sprintf("scan-%03d-%s", index, strings.Repeat("x", 30))
	}
	input := strings.Join(lines, "\n") + "\n"
	const limit = 512
	buffer := newOutputBuffer(limit)
	var received []string
	var wait sync.WaitGroup
	wait.Add(1)
	scanOutput(strings.NewReader(input), buffer, func(line string) { received = append(received, line) }, &wait)
	wait.Wait()

	if got, want := buffer.String(), input[len(input)-limit:]; got != want {
		t.Fatalf("content = %q, want last %d bytes %q", got, limit, want)
	}
	if len(received) != len(lines) {
		t.Fatalf("line handler received %d lines, want %d", len(received), len(lines))
	}
	for index, line := range lines {
		if received[index] != line {
			t.Fatalf("line handler line %d = %q, want %q", index, received[index], line)
		}
	}
}

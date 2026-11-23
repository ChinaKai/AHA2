package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const scratchFixtureSize = 46 << 20

type scratchFixtureReader struct{ maxRead int }

func (reader *scratchFixtureReader) Read(buffer []byte) (int, error) {
	reader.maxRead = max(reader.maxRead, len(buffer))
	for index := range buffer {
		buffer[index] = 0xa5
	}
	return len(buffer), nil
}

func attachmentDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func scratchAttachmentFixture(t *testing.T) (string, string) {
	t.Helper()
	workDir := t.TempDir()
	directory, err := EnsureScratchDir(t.Context(), domain.Workspace{Transport: "native"}, workDir, "task-1", "main")
	if err != nil {
		t.Fatal(err)
	}
	return workDir, directory
}

func TestScratchAttachmentStreams46MiBBounded(t *testing.T) {
	workDir, directory := scratchAttachmentFixture(t)
	fixture, err := os.Create(filepath.Join(t.TempDir(), "fixture.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(fixture, hash), io.LimitReader(&scratchFixtureReader{}, scratchFixtureSize))
	if err != nil {
		t.Fatal(err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if _, err = fixture.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	result, err := MaterializeScratchAttachment(t.Context(), domain.Workspace{Transport: "native"}, workDir, "task-1", "main", fixture, scratchFixtureSize, digest, DefaultScratchAttachmentBytes)
	runtime.ReadMemStats(&after)
	if err != nil || result.Size != scratchFixtureSize || result.SHA256 != digest || filepath.Dir(result.Path) != directory {
		t.Fatalf("materialize = %+v, err=%v", result, err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("streamed %d bytes; Go heap allocations during copy: %d bytes", result.Size, allocated)
	if testing.CoverMode() == "" && allocated > 2<<20 {
		t.Fatalf("stream used %d bytes of heap allocations; want under 2 MiB", allocated)
	}
	output, err := os.Open(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	hash.Reset()
	if count, err := io.Copy(hash, output); err != nil || count != scratchFixtureSize || hex.EncodeToString(hash.Sum(nil)) != digest {
		t.Fatalf("copy on disk does not match source: %d bytes, err=%v", count, err)
	}
	if info, err := output.Stat(); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("output permissions: info=%v err=%v", info, err)
	}
}

type interruptedScratchReader struct{}

func (interruptedScratchReader) Read(buffer []byte) (int, error) {
	return copy(buffer, "partial"), io.ErrUnexpectedEOF
}

func assertEmptyScratch(t *testing.T, directory string) {
	t.Helper()
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("scratch residue: %v; err=%v", entries, err)
	}
}

func TestScratchAttachmentRejectsOversizeAndCleansFailures(t *testing.T) {
	workDir, directory := scratchAttachmentFixture(t)
	item := domain.Workspace{Transport: "native"}
	unbounded := &scratchFixtureReader{}
	testCases := []struct {
		name   string
		source io.Reader
		size   int64
		limit  int64
		want   error
	}{
		{"metadata cap", strings.NewReader(""), 11, 10, ErrScratchAttachmentTooLarge},
		{"stream cap without content length", unbounded, 10, 10, ErrScratchAttachmentTooLarge},
		{"unexpected EOF", interruptedScratchReader{}, 10, 10, io.ErrUnexpectedEOF},
		{"truncated content", strings.NewReader("short"), 10, 10, ErrScratchAttachmentIntegrity},
		{"same size but corrupt", strings.NewReader("1234567890"), 10, 10, ErrScratchAttachmentIntegrity},
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			result, err := MaterializeScratchAttachment(t.Context(), item, workDir, "task-1", "main", test.source, test.size, attachmentDigest("0123456789"), test.limit)
			if !errors.Is(err, test.want) || result.Path != "" {
				t.Fatalf("result=%+v err=%v want=%v", result, err, test.want)
			}
			assertEmptyScratch(t, directory)
		})
	}
	if unbounded.maxRead != 10 {
		t.Fatalf("max read is %d, want 10", unbounded.maxRead)
	}
}

func TestScratchAttachmentRejectsSymlinkAndNeverOverwrites(t *testing.T) {
	workDir, directory := scratchAttachmentFixture(t)
	item := domain.Workspace{Transport: "native"}
	segments := []string{".aha2-context", "task-1", "scratch", "main"}
	if _, err := MaterializeScratchAttachment(t.Context(), item, workDir, "../task-1", "main", strings.NewReader(""), 0, attachmentDigest(""), 10); !errors.Is(err, ErrScratchAttachmentDestination) {
		t.Fatalf("invalid task segment: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "existing"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := writeLocalScratchAttachment(t.Context(), workDir, segments, "existing", func(destination io.Writer) (ScratchAttachment, error) {
		return copyScratchAttachment(t.Context(), destination, bytes.NewBufferString("replacement"), 11, attachmentDigest("replacement"), 11)
	})
	if !errors.Is(err, ErrScratchAttachmentDestination) {
		t.Fatalf("collision: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(directory, "existing"))
	if err != nil || string(content) != "original" {
		t.Fatalf("existing file changed: %q err=%v", content, err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 1 {
		t.Fatalf("collision left other files in scratch: %v; err=%v", entries, err)
	}
	sibling := filepath.Join(filepath.Dir(directory), "sibling")
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(directory), "linked")
	if err := os.Symlink(sibling, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("cannot create a symlink on this host: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := MaterializeScratchAttachment(t.Context(), item, workDir, "task-1", "linked", strings.NewReader(""), 0, attachmentDigest(""), 10); !errors.Is(err, ErrScratchAttachmentDestination) {
		t.Fatalf("symlink to a sibling scratch accepted: %v", err)
	}
	assertEmptyScratch(t, sibling)
}

func TestScratchAttachmentSSHStreamAndInterruptedCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec SSH fixture needs a POSIX shell")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("POSIX workspace materialization needs python3")
	}
	host, port := startExecSSHServer(t)
	item := agentProxyTestWorkspace(host, port)
	workDir, directory := scratchAttachmentFixture(t)
	result, err := MaterializeScratchAttachment(t.Context(), item, workDir, "task-1", "main", interruptedScratchReader{}, 10, attachmentDigest("0123456789"), 10)
	if !errors.Is(err, io.ErrUnexpectedEOF) || result.Path != "" {
		t.Fatalf("SSH interrupted stream = %+v, err=%v", result, err)
	}
	assertEmptyScratch(t, directory)
	result, err = MaterializeScratchAttachment(t.Context(), item, workDir, "task-1", "main", &scratchFixtureReader{}, 10, attachmentDigest("0123456789"), 10)
	if !errors.Is(err, ErrScratchAttachmentTooLarge) || result.Path != "" {
		t.Fatalf("SSH over-limit stream = %+v, err=%v", result, err)
	}
	assertEmptyScratch(t, directory)
	sibling := filepath.Join(filepath.Dir(directory), "sibling")
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sibling, filepath.Join(filepath.Dir(directory), "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeScratchAttachment(t.Context(), item, workDir, "task-1", "linked", strings.NewReader("x"), 1, attachmentDigest("x"), 10); !errors.Is(err, ErrScratchAttachmentDestination) {
		t.Fatalf("SSH accepted linked scratch: %v", err)
	}
	assertEmptyScratch(t, sibling)

	cancelContext, cancel := context.WithCancel(t.Context())
	defer cancel()
	result, err = MaterializeScratchAttachment(cancelContext, item, workDir, "task-1", "main", cancelAfterFirstChunk{cancel: cancel}, 64<<10, attachmentDigest(""), 64<<10)
	if err == nil || result.Path != "" {
		t.Fatalf("SSH cancelled stream = %+v, err=%v", result, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertEmptyScratch(t, directory)

	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(&scratchFixtureReader{}, scratchFixtureSize)); err != nil {
		t.Fatal(err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	source := &scratchFixtureReader{}
	result, err = MaterializeScratchAttachment(t.Context(), item, workDir, "task-1", "main", io.LimitReader(source, scratchFixtureSize), scratchFixtureSize, digest, DefaultScratchAttachmentBytes)
	if err != nil || result.SHA256 != digest || result.Size != scratchFixtureSize || filepath.Dir(result.Path) != directory || source.maxRead > 32<<10 {
		t.Fatalf("SSH result=%+v err=%v max read=%d", result, err, source.maxRead)
	}
	t.Logf("streamed %d bytes over SSH in source reads of at most %d bytes", result.Size, source.maxRead)
	output, err := os.Open(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	hash.Reset()
	if count, err := io.Copy(hash, output); err != nil || count != scratchFixtureSize || hex.EncodeToString(hash.Sum(nil)) != digest {
		t.Fatalf("SSH file does not match source: %d bytes, err=%v", count, err)
	}
}

type cancelAfterFirstChunk struct{ cancel context.CancelFunc }

func (source cancelAfterFirstChunk) Read(buffer []byte) (int, error) {
	source.cancel()
	return len(buffer), nil
}

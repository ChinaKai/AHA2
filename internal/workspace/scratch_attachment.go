package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

const (
	DefaultScratchAttachmentBytes = 64 << 20
	MaxScratchAttachmentBytes     = 200 << 20
	scratchAttachmentTimeout      = 2 * time.Minute
)

var (
	ErrScratchAttachmentTooLarge    = errors.New("attachment exceeds the materialization limit")
	ErrScratchAttachmentIntegrity   = errors.New("attachment size or SHA-256 did not match")
	ErrScratchAttachmentDestination = errors.New("attachment scratch directory is unavailable or unsafe")
	ErrScratchAttachmentUnsupported = errors.New("attachment materialization is not supported on this workspace transport")
)

type ScratchAttachment struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func MaterializeScratchAttachment(ctx context.Context, item domain.Workspace, workDir, taskID, agentID string, source io.Reader, size int64, digest string, limit int64) (ScratchAttachment, error) {
	if limit <= 0 || limit > MaxScratchAttachmentBytes || size > limit {
		return ScratchAttachment{}, ErrScratchAttachmentTooLarge
	}
	if size < 0 {
		return ScratchAttachment{}, ErrScratchAttachmentIntegrity
	}
	directory, err := AgentScratchDir(item, workDir, taskID, agentID)
	if err != nil {
		return ScratchAttachment{}, ErrScratchAttachmentDestination
	}
	segments := []string{ahaContextDirectoryName, taskID, scratchDirectoryName, agentID}
	name := domain.NewID("attachment")
	ctx, cancel := context.WithTimeout(ctx, scratchAttachmentTimeout)
	defer cancel()
	copyContent := func(destination io.Writer) (ScratchAttachment, error) {
		return copyScratchAttachment(ctx, destination, source, size, digest, limit)
	}
	switch runner := RunnerFor(item).(type) {
	case LocalRunner:
		if !filepath.IsAbs(workDir) {
			return ScratchAttachment{}, ErrScratchAttachmentDestination
		}
		return writeLocalScratchAttachment(ctx, workDir, segments, name, copyContent)
	case *SSHRunner:
		if IsWindowsWorkspace(item) {
			return ScratchAttachment{}, ErrScratchAttachmentUnsupported
		}
		if !path.IsAbs(workDir) {
			return ScratchAttachment{}, ErrScratchAttachmentDestination
		}
		return writeRemoteScratchAttachment(copyContent, directory, workDir, segments, name, size, func(input io.Reader, args []string) error {
			result, err := runner.runScriptWithStdin(ctx, `exec python3 -I -c "$@"`, append([]string{"aha-attachment"}, args...), input, scratchAttachmentTimeout, 1024)
			if err != nil {
				return err
			}
			if result.ExitCode == 127 {
				return ErrScratchAttachmentUnsupported
			}
			if result.ExitCode != 0 {
				return ErrScratchAttachmentDestination
			}
			return nil
		})
	case WSLRunner:
		if !path.IsAbs(workDir) {
			return ScratchAttachment{}, ErrScratchAttachmentDestination
		}
		return writeRemoteScratchAttachment(copyContent, directory, workDir, segments, name, size, func(input io.Reader, args []string) error {
			process := exec.CommandContext(ctx, findWSLExecutable(), append([]string{"-d", runner.Distro, "-e", "python3", "-I", "-c"}, args...)...)
			process.Env = wslHostEnvironment()
			process.Stdin = input
			err := process.Run()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var exitErr *exec.ExitError
			if errors.Is(err, exec.ErrNotFound) || errors.As(err, &exitErr) && exitErr.ExitCode() == 127 {
				return ErrScratchAttachmentUnsupported
			}
			return err
		})
	default:
		return ScratchAttachment{}, ErrScratchAttachmentUnsupported
	}
}

type scratchAttachmentReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader scratchAttachmentReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}

func copyScratchAttachment(ctx context.Context, destination io.Writer, source io.Reader, size int64, digest string, limit int64) (ScratchAttachment, error) {
	hash := sha256.New()
	reader := scratchAttachmentReader{ctx: ctx, source: source}
	count, err := io.CopyBuffer(io.MultiWriter(destination, hash), io.LimitReader(reader, min(size, limit)), make([]byte, 32<<10))
	result := ScratchAttachment{Size: count}
	if err != nil {
		return result, err
	}
	var extra [1]byte
	if _, err := io.ReadFull(reader, extra[:]); err == nil {
		if count == limit {
			return result, ErrScratchAttachmentTooLarge
		}
		return result, ErrScratchAttachmentIntegrity
	} else if err != io.EOF {
		return result, err
	}
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if count != size || result.SHA256 != digest {
		return result, ErrScratchAttachmentIntegrity
	}
	return result, ctx.Err()
}

func openAgentScratchRoot(workDir string, segments []string) (*os.Root, error) {
	root, err := os.OpenRoot(workDir)
	if err != nil {
		return nil, ErrScratchAttachmentDestination
	}
	for _, segment := range segments {
		info, err := root.Lstat(segment)
		if err != nil || !info.IsDir() {
			root.Close()
			return nil, ErrScratchAttachmentDestination
		}
		child, err := root.OpenRoot(segment)
		root.Close()
		if err != nil {
			return nil, ErrScratchAttachmentDestination
		}
		opened, err := child.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			child.Close()
			return nil, ErrScratchAttachmentDestination
		}
		root = child
	}
	info, err := root.Stat(".")
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		root.Close()
		return nil, ErrScratchAttachmentDestination
	}
	return root, nil
}

func writeLocalScratchAttachment(ctx context.Context, workDir string, segments []string, name string, copyContent func(io.Writer) (ScratchAttachment, error)) (ScratchAttachment, error) {
	root, err := openAgentScratchRoot(workDir, segments)
	if err != nil {
		return ScratchAttachment{}, err
	}
	defer root.Close()
	partial := "." + name + ".partial"
	file, err := root.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ScratchAttachment{}, ErrScratchAttachmentDestination
	}
	defer root.Remove(partial)
	result, copyErr := copyContent(file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr, ctx.Err()); err != nil {
		return result, err
	}
	if err := root.Link(partial, name); err != nil {
		return result, ErrScratchAttachmentDestination
	}
	result.Path = filepath.Join(root.Name(), name)
	return result, nil
}

func writeRemoteScratchAttachment(copyContent func(io.Writer) (ScratchAttachment, error), directory, workDir string, segments []string, name string, size int64, run func(io.Reader, []string) error) (ScratchAttachment, error) {
	reader, writer := io.Pipe()
	done := make(chan struct{})
	var result ScratchAttachment
	var copyErr error
	go func() {
		defer close(done)
		result, copyErr = copyContent(writer)
		if copyErr == nil {
			_, copyErr = io.WriteString(writer, "\x01")
		}
		writer.CloseWithError(copyErr)
	}()
	args := append([]string{scratchAttachmentPython, workDir, name, strconv.FormatInt(size, 10)}, segments...)
	runErr := run(reader, args)
	reader.Close()
	<-done
	if copyErr != nil && !errors.Is(copyErr, io.ErrClosedPipe) {
		return result, copyErr
	}
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, ErrScratchAttachmentUnsupported) {
		return result, runErr
	}
	if runErr != nil || copyErr != nil {
		return result, ErrScratchAttachmentDestination
	}
	result.Path = directory + "/" + name
	return result, nil
}

const scratchAttachmentPython = `
import os, signal, sys

def interrupted(signum, frame):
    sys.exit(1)

for signum in (signal.SIGHUP, signal.SIGTERM):
    signal.signal(signum, interrupted)

workdir, name, size, *segments = sys.argv[1:]
flags = os.O_RDONLY | os.O_DIRECTORY
directory = os.open(workdir, flags)
for segment in segments:
    child = os.open(segment, flags | os.O_NOFOLLOW, dir_fd=directory)
    os.close(directory)
    directory = child
if os.fstat(directory).st_mode & 0o077:
    sys.exit(1)

partial = '.' + name + '.partial'
file = os.open(partial, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=directory)
try:
    with os.fdopen(file, 'wb') as output:
        remaining = int(size)
        while remaining:
            chunk = sys.stdin.buffer.read(min(32768, remaining))
            if not chunk:
                sys.exit(1)
            output.write(chunk)
            remaining -= len(chunk)
        if sys.stdin.buffer.read(2) != b'\x01':
            sys.exit(1)
    os.link(partial, name, src_dir_fd=directory, dst_dir_fd=directory, follow_symlinks=False)
finally:
    os.unlink(partial, dir_fd=directory)
`

package workspaceterminal

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsTerminal struct {
	input, output *os.File
	mu            sync.Mutex
	console       windows.Handle
	job           windows.Handle
	exitCode      uint32
	exitErr       error
	exited        chan struct{}
}

// ConPTY requires synchronous pipe handles. The browser bridge uses the
// other ends in overlapped mode so Close can interrupt a blocked read or write.
func consolePipe(access, direction uint32, security *windows.SecurityAttributes) (windows.Handle, *os.File, error) {
	name, err := windows.UTF16PtrFromString(`\\.\pipe\aha2-terminal-` + rand.Text())
	if err != nil {
		return 0, nil, err
	}
	server, err := windows.CreateNamedPipe(name, access|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 64<<10, 64<<10, 0, security)
	if err != nil {
		return 0, nil, err
	}
	client, err := windows.CreateFile(name, direction, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		_ = windows.CloseHandle(server)
		return 0, nil, err
	}
	if err := windows.ConnectNamedPipe(server, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		_ = windows.CloseHandle(client)
		_ = windows.CloseHandle(server)
		return 0, nil, err
	}
	return server, os.NewFile(uintptr(client), "terminal pipe"), nil
}

func StartLocal(root string, cols, rows int) (Process, error) {
	if !LocalSupported() {
		return nil, windows.ERROR_NOT_SUPPORTED
	}
	identity, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get process token user: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + identity.User.Sid.String() + ")")
	if err != nil {
		return nil, fmt.Errorf("create pipe security descriptor: %w", err)
	}
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	inputRead, inputWrite, err := consolePipe(windows.PIPE_ACCESS_INBOUND, windows.GENERIC_WRITE, &security)
	if err != nil {
		return nil, fmt.Errorf("open ConPTY input pipe: %w", err)
	}
	outputWrite, outputRead, err := consolePipe(windows.PIPE_ACCESS_OUTBOUND, windows.GENERIC_READ, &security)
	if err != nil {
		_ = windows.CloseHandle(inputRead)
		_ = inputWrite.Close()
		return nil, fmt.Errorf("open ConPTY output pipe: %w", err)
	}

	terminal := &windowsTerminal{input: inputWrite, output: outputRead, exited: make(chan struct{})}
	process, err := terminal.start(root, cols, rows, inputRead, outputWrite)
	_ = windows.CloseHandle(inputRead)
	_ = windows.CloseHandle(outputWrite)
	if err != nil {
		_ = terminal.Close()
		return nil, err
	}
	go func() {
		defer windows.CloseHandle(process)
		_, terminal.exitErr = windows.WaitForSingleObject(process, windows.INFINITE)
		if terminal.exitErr == nil {
			terminal.exitErr = windows.GetExitCodeProcess(process, &terminal.exitCode)
		}
		close(terminal.exited)
		terminal.closeConsole()
	}()
	return terminal, nil
}

func (p *windowsTerminal) start(root string, cols, rows int, input, output windows.Handle) (windows.Handle, error) {
	size := windows.Coord{X: int16(cols), Y: int16(rows)}
	if err := windows.CreatePseudoConsole(size, input, output, 0, &p.console); err != nil {
		return 0, fmt.Errorf("create ConPTY: %w", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return 0, fmt.Errorf("allocate process attributes: %w", err)
	}
	defer attributes.Delete()
	// UpdateProcThreadAttribute expects the opaque HPCON value as a pointer.
	consoleAttribute := *(*unsafe.Pointer)(unsafe.Pointer(&p.console))
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, consoleAttribute, unsafe.Sizeof(p.console)); err != nil {
		return 0, fmt.Errorf("set ConPTY process attribute: %w", err)
	}
	p.job, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create job object: %w", err)
	}
	jobInfo := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	jobInfo.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&jobInfo)), uint32(unsafe.Sizeof(jobInfo))); err != nil {
		return 0, fmt.Errorf("set job object limits: %w", err)
	}

	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return 0, err
	}
	shell := filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe")
	executable, err := windows.UTF16PtrFromString(shell)
	if err != nil {
		return 0, err
	}
	command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{shell, "-NoLogo", "-NoProfile"}))
	if err != nil {
		return 0, err
	}
	dir, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	env := shellEnvironment()
	for i, entry := range env {
		// CreateProcess expects a case-insensitively sorted environment block.
		name, value, _ := strings.Cut(entry, "=")
		env[i] = strings.ToUpper(name) + "=" + value
	}
	slices.Sort(env)
	block := utf16.Encode([]rune(strings.Join(env, "\x00") + "\x00\x00"))
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	// Leave the standard handles null so ConPTY supplies them instead of
	// letting the shell use the server's console handles.
	startup.StartupInfo.Flags |= windows.STARTF_USESTDHANDLES
	startup.Cb = uint32(unsafe.Sizeof(startup))
	var info windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(executable, command, nil, nil, false, flags, &block[0], dir, &startup.StartupInfo, &info); err != nil {
		return 0, fmt.Errorf("create shell process: %w", err)
	}
	defer windows.CloseHandle(info.Thread)
	// Assign before resuming so child processes inherit the same lifetime.
	if err := windows.AssignProcessToJobObject(p.job, info.Process); err != nil {
		_ = windows.TerminateProcess(info.Process, 1)
		_ = windows.CloseHandle(info.Process)
		return 0, fmt.Errorf("assign shell to job object: %w", err)
	}
	if _, err := windows.ResumeThread(info.Thread); err != nil {
		_ = windows.CloseHandle(info.Process)
		return 0, fmt.Errorf("resume shell: %w", err)
	}
	return info.Process, nil
}

func (p *windowsTerminal) Read(data []byte) (int, error)  { return p.output.Read(data) }
func (p *windowsTerminal) Write(data []byte) (int, error) { return p.input.Write(data) }

func (p *windowsTerminal) ExitStatus() (int64, error) {
	<-p.exited
	if p.exitErr != nil {
		return -1, p.exitErr
	}
	return int64(p.exitCode), nil
}

func (p *windowsTerminal) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.console == 0 {
		return os.ErrClosed
	}
	return windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (p *windowsTerminal) Close() error {
	_ = p.input.Close()
	// Older Windows builds block in ClosePseudoConsole when the output pipe
	// is full. Closing the reader first breaks the pipe on explicit teardown.
	_ = p.output.Close()
	p.closeConsole()
	return nil
}

func (p *windowsTerminal) closeConsole() {
	p.mu.Lock()
	console, job := p.console, p.job
	p.console, p.job = 0, 0
	p.mu.Unlock()
	if job != 0 {
		_ = windows.CloseHandle(job)
	}
	if console != 0 {
		windows.ClosePseudoConsole(console)
	}
}

var conPTYSupported = sync.OnceValue(func() bool {
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	return kernel.NewProc("CreatePseudoConsole").Find() == nil
})

func LocalSupported() bool { return conPTYSupported() }

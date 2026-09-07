//go:build windows

package tray

import "golang.org/x/sys/windows"

type instanceLock struct{ handle windows.Handle }

func acquireSingleInstance() (*instanceLock, error) {
	name, err := windows.UTF16PtrFromString(SingleInstanceName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, err
	}
	return &instanceLock{handle: handle}, nil
}

func (l *instanceLock) Close() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(l.handle)
	l.handle = 0
	return err
}

//go:build windows

package hardware

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type windowsSerialPort struct {
	handle windows.Handle
	once   sync.Once
}

func (port *windowsSerialPort) Read(buffer []byte) (int, error) {
	var count uint32
	err := windows.ReadFile(port.handle, buffer, &count, nil)
	return int(count), err
}

func (port *windowsSerialPort) Write(data []byte) (int, error) {
	var count uint32
	err := windows.WriteFile(port.handle, data, &count, nil)
	return int(count), err
}

func (port *windowsSerialPort) Close() error {
	var result error
	port.once.Do(func() {
		result = windows.CloseHandle(port.handle)
	})
	return result
}

func openSerial(device string, baudrate int) (io.ReadWriteCloser, error) {
	path := strings.TrimSpace(device)
	upper := strings.ToUpper(path)
	if strings.HasPrefix(upper, "COM") {
		if number, err := strconv.Atoi(strings.TrimPrefix(upper, "COM")); err == nil && number > 9 {
			path = `\\.\` + upper
		}
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return nil, err
	}
	closeHandle := true
	defer func() {
		if closeHandle {
			_ = windows.CloseHandle(handle)
		}
	}()
	_ = windows.SetupComm(handle, 64*1024, 64*1024)
	dcb := windows.DCB{DCBlength: uint32(unsafe.Sizeof(windows.DCB{}))}
	if err := windows.GetCommState(handle, &dcb); err != nil {
		return nil, err
	}
	dcb.BaudRate = uint32(baudrate)
	dcb.ByteSize = 8
	dcb.Parity = 0
	dcb.StopBits = 0
	dcb.Flags = 1 | (1 << 4) | (1 << 12)
	if err := windows.SetCommState(handle, &dcb); err != nil {
		return nil, err
	}
	timeouts := windows.CommTimeouts{
		ReadIntervalTimeout: ^uint32(0), ReadTotalTimeoutConstant: 100,
		WriteTotalTimeoutConstant: 1000,
	}
	if err := windows.SetCommTimeouts(handle, &timeouts); err != nil {
		return nil, err
	}
	_ = windows.PurgeComm(handle, windows.PURGE_RXABORT|windows.PURGE_RXCLEAR|windows.PURGE_TXABORT|windows.PURGE_TXCLEAR)
	closeHandle = false
	return &windowsSerialPort{handle: handle}, nil
}

func listSerialPorts() []domain.SerialPort {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()
	names, err := key.ReadValueNames(-1)
	if err != nil {
		return nil
	}
	result := make([]domain.SerialPort, 0, len(names))
	for _, name := range names {
		device, _, err := key.GetStringValue(name)
		if err == nil && strings.TrimSpace(device) != "" {
			result = append(result, domain.SerialPort{Device: device, Description: device, HardwareID: name})
		}
	}
	sortSerialPorts(result)
	return result
}

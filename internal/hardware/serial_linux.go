//go:build linux

package hardware

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func openSerial(device string, baudrate int) (io.ReadWriteCloser, error) {
	fd, err := unix.Open(device, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	closeFD := true
	defer func() {
		if closeFD {
			_ = unix.Close(fd)
		}
	}()
	settings, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	speed, err := linuxBaudrate(baudrate)
	if err != nil {
		return nil, err
	}
	settings.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF | unix.IXANY
	settings.Oflag &^= unix.OPOST
	settings.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	settings.Cflag &^= unix.CSIZE | unix.PARENB | unix.CSTOPB | unix.CRTSCTS | unix.CBAUD
	settings.Cflag |= unix.CS8 | unix.CLOCAL | unix.CREAD | speed
	settings.Ispeed = speed
	settings.Ospeed = speed
	settings.Cc[unix.VMIN] = 0
	settings.Cc[unix.VTIME] = 1
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, settings); err != nil {
		return nil, err
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), device)
	if file == nil {
		return nil, fmt.Errorf("create serial file for %s", device)
	}
	closeFD = false
	return file, nil
}

func linuxBaudrate(value int) (uint32, error) {
	rates := map[int]uint32{
		50: unix.B50, 75: unix.B75, 110: unix.B110, 134: unix.B134, 150: unix.B150,
		200: unix.B200, 300: unix.B300, 600: unix.B600, 1200: unix.B1200, 1800: unix.B1800,
		2400: unix.B2400, 4800: unix.B4800, 9600: unix.B9600, 19200: unix.B19200,
		38400: unix.B38400, 57600: unix.B57600, 115200: unix.B115200, 230400: unix.B230400,
	}
	speed, ok := rates[value]
	if !ok {
		return 0, fmt.Errorf("unsupported serial baudrate %d", value)
	}
	return speed, nil
}

func listSerialPorts() []domain.SerialPort {
	patterns := []string{"/dev/ttyUSB*", "/dev/ttyACM*", "/dev/ttyS*", "/dev/serial/by-id/*"}
	seen := map[string]struct{}{}
	var result []domain.SerialPort
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, device := range matches {
			if resolved, err := filepath.EvalSymlinks(device); err == nil && strings.Contains(device, "/by-id/") {
				device = resolved
			}
			if _, ok := seen[device]; ok {
				continue
			}
			seen[device] = struct{}{}
			result = append(result, domain.SerialPort{Device: device, Description: filepath.Base(device)})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Device < result[j].Device })
	return result
}

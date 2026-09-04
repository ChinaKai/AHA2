//go:build !windows && !linux

package hardware

import (
	"fmt"
	"io"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func openSerial(device string, baudrate int) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("serial hardware is not implemented on this platform")
}

func listSerialPorts() []domain.SerialPort {
	return nil
}

package hardwareio

import (
	"encoding/hex"
	"strings"
)

// encodePathName turns an ID into a directory name that is safe on Windows
// and Linux. Plain lowercase IDs (every AHA-generated ID) are kept readable;
// anything else, including Windows device names, is hex-encoded behind a
// "~" prefix, which plain names can never contain, so the mapping is
// reversible and collision-free.
func encodePathName(value string) string {
	if plainPathName(value) {
		return value
	}
	return "~" + hex.EncodeToString([]byte(value))
}

func decodePathName(name string) (string, bool) {
	if strings.HasPrefix(name, "~") {
		raw, err := hex.DecodeString(name[1:])
		if err != nil || len(raw) == 0 {
			return "", false
		}
		return string(raw), true
	}
	return name, plainPathName(name)
}

var windowsDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com0": true, "com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt0": true, "lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

func plainPathName(value string) bool {
	if value == "" || len(value) > 96 || windowsDeviceNames[value] {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '_', char == '-':
		default:
			return false
		}
	}
	return true
}

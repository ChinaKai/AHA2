package httpapi

import (
	"errors"
	"github.com/ChinaKai/AHA2/internal/hardware"
)

func hardwarePublicErrorCode(err error, fallback string) string {
	switch {
	case errors.Is(err, hardware.ErrNotConnected):
		return "hardware_not_connected"
	case errors.Is(err, hardware.ErrReadOnly):
		return "hardware_read_only"
	case errors.Is(err, hardware.ErrLoginCredentialsNotConfigured):
		return "hardware_credentials_not_configured"
	case errors.Is(err, hardware.ErrLoginOptionsInvalid):
		return "hardware_login_options_invalid"
	case errors.Is(err, hardware.ErrLoginRetryLimit):
		return "hardware_login_retry_limit"
	case errors.Is(err, hardware.ErrHardwareEncodingInvalid):
		return "hardware_encoding_invalid"
	case errors.Is(err, hardware.ErrADBOutputTooLarge):
		return "adb_output_limit_may_have_executed"
	default:
		return fallback
	}
}

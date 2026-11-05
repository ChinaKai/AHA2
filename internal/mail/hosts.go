package mail

import (
	"strings"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// AllowedSMTPHosts is the complete phase one NetEase enterprise mail host
// allowlist from the design. Accounts may only connect to these exact names.
func AllowedSMTPHosts() []string {
	return []string{domain.MailDefaultSMTPHost, domain.MailBackupSMTPHost}
}

// NormalizeSMTPHost canonicalizes and validates an SMTP hostname. It rejects
// trailing dots, IP literals, ports, whitespace and every host outside the
// design's primary/backup pair so an Owner cannot point the connection test at
// an arbitrary endpoint.
func NormalizeSMTPHost(raw string) (string, bool) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "" || strings.ContainsAny(host, " /\t\r\n") {
		return "", false
	}
	if strings.HasSuffix(host, ".") {
		return "", false
	}
	for _, allowed := range AllowedSMTPHosts() {
		if host == allowed {
			return host, true
		}
	}
	return "", false
}

package mail

import "strings"

// disallowedAttachmentExtensions is the Owner-confirmed phase one policy from
// design section 9.3: executable files and scripts are rejected as mail
// attachments, with no direct-attachment exception.
var disallowedAttachmentExtensions = []string{
	".exe", ".bat", ".cmd", ".ps1", ".js", ".vbs", ".scr", ".msi",
}

// DisallowedAttachmentName reports whether a file name has an executable or
// script extension. The check is suffix based so it also covers names that
// arrive without a path; matching is case-insensitive.
func DisallowedAttachmentName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, extension := range disallowedAttachmentExtensions {
		if strings.HasSuffix(name, extension) {
			return true
		}
	}
	return false
}

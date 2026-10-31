package app

import (
	"fmt"
	"strings"
)

const minPetApprovalReasonRunes = 8

// requirePetApprovalReason is the single gate for approvals that delete data,
// change credentials/environment, or leave the machine. The field name is part
// of the error so the pet can correct the tool call instead of guessing.
func requirePetApprovalReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < minPetApprovalReasonRunes {
		return "", fmt.Errorf("reason 至少需要 %d 个字符", minPetApprovalReasonRunes)
	}
	return reason, nil
}

func requiredPetApprovalReason(values ...string) (string, error) {
	reason := ""
	if len(values) > 0 {
		reason = values[0]
	}
	return requirePetApprovalReason(reason)
}

func optionalPetApprovalReason(reason string) string {
	return strings.TrimSpace(reason)
}

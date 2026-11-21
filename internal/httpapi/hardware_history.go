package httpapi

import (
	"context"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
)

// Every terminal history/replay surface uses this boundary, including retained
// records written before stream sanitization was installed. Secret values are
// resolved internally and never become tool inputs or response fields.
func (s *Server) hardwareHistoryPage(ctx context.Context, group domain.HardwareGroup, transport string, after int64, limit int) (domain.HardwareIOPage, error) {
	page, err := s.store.HardwareIOPage(ctx, group.TaskID, group.ID, transport, after, limit)
	if err != nil {
		return page, err
	}
	if group.CredentialRef != "" && s.secrets != nil {
		if secret, ok := s.secrets.Get(group.CredentialRef); ok {
			return hardware.RedactHistory(page, secret), nil
		}
	}
	return page, nil
}

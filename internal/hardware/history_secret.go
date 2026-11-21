package hardware

import "github.com/ChinaKai/AHA2/internal/domain"

// RedactHistory is a defense for retained records from older versions and for
// stored credentials typed as separate keystroke events. Newly received output
// is already sanitized before persistence and broadcast. A page is only a window
// of each direction's stream, so both ends are treated as unknown context: a
// trailing credential prefix is masked (Finish) and a leading fragment that
// could finish an earlier credential is masked (possibleContinuation). Paging
// with any cursor or limit therefore cannot reassemble a credential. Event
// identity/cursors stay unchanged, including an event emptied by prefix hold.
func RedactHistory(page domain.HardwareIOPage, secret string) domain.HardwareIOPage {
	if secret == "" {
		return page
	}
	page.Items = append([]domain.HardwareIOEvent(nil), page.Items...)
	heads := map[string][]byte{}
	totals := map[string]int{}
	for _, item := range page.Items {
		key := item.Transport + "\x00" + item.Direction
		totals[key] += len(item.Data)
		if room := len(secret) - len(heads[key]); room > 0 {
			heads[key] = append(heads[key], item.Data[:min(room, len(item.Data))]...)
		}
	}
	filters := map[string]*secretStream{}
	last := map[string]int{}
	for index := range page.Items {
		item := &page.Items[index]
		key := item.Transport + "\x00" + item.Direction
		filter := filters[key]
		if filter == nil {
			filter = newSecretStream(secret)
			filter.forced = possibleContinuation(filter.secret, heads[key], totals[key])
			filters[key] = filter
		}
		item.Data = string(filter.Feed([]byte(item.Data)))
		last[key] = index
	}
	for key, filter := range filters {
		if tail := filter.Finish(); len(tail) > 0 {
			page.Items[last[key]].Data += string(tail)
		}
	}
	return page
}

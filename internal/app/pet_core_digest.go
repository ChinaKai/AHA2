package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	petDigestIdentityLimit     = 300
	petDigestIdentityLineLimit = 120
	petDigestPrinciplesLimit   = 500
	petDigestPrinciplesLine    = 80
	petDigestLoopItems         = 3
	petDigestAgendaItems       = 2
	petDigestItemLimit         = 120
)

// PetCoreDigest is a bounded, runtime-only summary of the four core files. It
// is rebuilt from the files every time and never written back, so the files
// stay the single source of truth; the same value is rendered into the pet
// system prompt and returned by aha_pet_work_state.
type PetCoreDigest struct {
	Version             string       `json:"core_version"`
	Identity            string       `json:"identity"`
	IdentityTruncated   bool         `json:"identity_truncated,omitempty"`
	Principles          string       `json:"principles"`
	PrinciplesTruncated bool         `json:"principles_truncated,omitempty"`
	OpenLoops           []string     `json:"open_loops"`
	OpenLoopsTotal      int          `json:"open_loops_total"`
	Agenda              []string     `json:"agenda"`
	AgendaTotal         int          `json:"agenda_total"`
	Paths               PetCorePaths `json:"paths"`
	Missing             []string     `json:"missing,omitempty"`
}

type PetCorePaths struct {
	Identity   string `json:"identity"`
	Principles string `json:"principles"`
	Loops      string `json:"loops"`
	Agenda     string `json:"agenda"`
}

// petCoreDigest reads the core files once and summarizes them. It only reads:
// seeding missing files stays with ensurePetWorkspaceFiles.
func (s *Service) petCoreDigest(ctx context.Context, english bool) PetCoreDigest {
	return buildPetCoreDigest(strings.TrimSpace(s.petWorkspaceRoot(ctx)), english)
}

func buildPetCoreDigest(root string, english bool) PetCoreDigest {
	corePath := func(relative string) string {
		if root == "" {
			return relative
		}
		return filepath.Join(root, relative)
	}
	digest := PetCoreDigest{
		OpenLoops: []string{}, Agenda: []string{},
		Paths: PetCorePaths{
			Identity: corePath(petCoreIdentityPath), Principles: corePath(petCorePrinciplesPath),
			Loops: corePath(petCoreLoopsPath), Agenda: corePath(petCoreAgendaPath),
		},
	}
	hash := sha256.New()
	contents := map[string]string{}
	for _, relative := range []string{petCoreIdentityPath, petCorePrinciplesPath, petCoreLoopsPath, petCoreAgendaPath} {
		var data []byte
		var err error
		if root != "" {
			data, err = os.ReadFile(filepath.Join(root, relative))
		} else {
			err = os.ErrNotExist
		}
		// A missing or unreadable file degrades to an empty section; the digest
		// must never fail the Turn or work_state that carries it.
		if err != nil || strings.TrimSpace(string(data)) == "" {
			digest.Missing = append(digest.Missing, relative)
			fmt.Fprintf(hash, "%s\x01", relative)
			continue
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", relative, len(data))
		hash.Write(data)
		contents[relative] = string(data)
	}
	digest.Version = hex.EncodeToString(hash.Sum(nil))[:12]
	digest.Identity, digest.IdentityTruncated = petDigestSection(contents[petCoreIdentityPath], petDigestIdentityLimit, petDigestIdentityLineLimit, petCoreIdentityPath, english)
	digest.Principles, digest.PrinciplesTruncated = petDigestSection(contents[petCorePrinciplesPath], petDigestPrinciplesLimit, petDigestPrinciplesLine, petCorePrinciplesPath, english)
	digest.OpenLoops, digest.OpenLoopsTotal = petDigestLedgerItems(activePetLoops(contents[petCoreLoopsPath]), petDigestLoopItems, func(status string) bool {
		return !isPetClosedStatus(status)
	})
	digest.Agenda, digest.AgendaTotal = petDigestLedgerItems(contents[petCoreAgendaPath], petDigestAgendaItems, func(status string) bool {
		return status == "next" || status == "waiting"
	})
	return digest
}

// petDigestSection returns the document unchanged (minus its title and blank
// lines) when it fits. Otherwise it keeps top-level lines in order, shortens
// long lines, and always ends with a marker that counts what was left out, so a
// dropped rule is visible instead of silently missing.
func petDigestSection(content string, limit, lineLimit int, relative string, english bool) (string, bool) {
	all := []string{}
	top := []string{}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if level, _ := petMarkdownHeading(trimmed); level == 1 {
			continue
		}
		all = append(all, line)
		if line == trimmed {
			top = append(top, trimmed)
		}
	}
	if full := strings.Join(all, "\n"); utf8.RuneCountInString(full) <= limit {
		return full, false
	}
	budget := limit - utf8.RuneCountInString(petDigestTruncationMarker(999, 999, relative, english)) - 1
	omitted := len(all) - len(top)
	shortened := 0
	kept := []string{}
	used := 0
	for index, line := range top {
		item := line
		clipped := utf8.RuneCountInString(item) > lineLimit
		if clipped {
			item = truncateAssistantText(item, lineLimit)
		}
		cost := utf8.RuneCountInString(item)
		if len(kept) > 0 {
			cost++
		}
		if used+cost > budget {
			omitted += len(top) - index
			break
		}
		if clipped {
			shortened++
		}
		kept = append(kept, item)
		used += cost
	}
	kept = append(kept, petDigestTruncationMarker(omitted, shortened, relative, english))
	return strings.Join(kept, "\n"), true
}

func petDigestTruncationMarker(omitted, shortened int, relative string, english bool) string {
	if english {
		return fmt.Sprintf("(digest truncated: %d lines omitted, %d shortened; read %s for the full text)", omitted, shortened, relative)
	}
	return fmt.Sprintf("（摘要已截断：省略 %d 行、缩短 %d 条；完整内容见 %s）", omitted, shortened, relative)
}

// petDigestLedgerItems picks ledger entries ("- status | item | ...") whose
// status passes keep. Guidance bullets without a "|" are not entries.
func petDigestLedgerItems(content string, limit int, keep func(status string) bool) ([]string, int) {
	items := []string{}
	total := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) != strings.TrimRight(line, "\r") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "* ") {
			continue
		}
		body := strings.TrimSpace(trimmed[2:])
		status, _, found := strings.Cut(body, "|")
		if !found {
			continue
		}
		status = strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(status), "[]")))
		// A status is one short word; this skips the seeded format line
		// "每项写：状态（open/waiting/blocked）| 事项 | ...".
		if !petLedgerStatus.MatchString(status) || !keep(status) {
			continue
		}
		total++
		if len(items) < limit {
			items = append(items, truncateAssistantText(body, petDigestItemLimit))
		}
	}
	return items, total
}

var petLedgerStatus = regexp.MustCompile(`^[\p{L}_-]{1,16}$`)

func isPetClosedStatus(status string) bool {
	switch status {
	case "done", "closed", "completed", "cancelled", "canceled", "已关闭", "已完成", "已闭环", "已取消":
		return true
	}
	return false
}

// petCoreDigestPrompt renders the digest for a system prompt. The hard rules
// stay in the prompt template; this block only carries the pet-owned content.
func petCoreDigestPrompt(digest PetCoreDigest, english bool) string {
	var b strings.Builder
	empty := "（缺失或为空）"
	if english {
		empty = "(missing or empty)"
		fmt.Fprintf(&b, "Core digest (core_version %s), generated from the core files at runtime. It is only a summary: when it is marked truncated or a rule is unclear, read the full file below; the files win over this digest.\n", digest.Version)
		fmt.Fprintf(&b, "Identity:\n%s\n", emptyAs(digest.Identity, empty))
		fmt.Fprintf(&b, "Principles:\n%s\n", emptyAs(digest.Principles, empty))
		fmt.Fprintf(&b, "Open loops: %d open, first %d:\n", digest.OpenLoopsTotal, len(digest.OpenLoops))
	} else {
		fmt.Fprintf(&b, "核心摘要（core_version %s）：运行时从 core 文件生成，只是摘要；标记截断或规则不清时读取下列全文，以文件为准。\n", digest.Version)
		fmt.Fprintf(&b, "身份：\n%s\n", emptyAs(digest.Identity, empty))
		fmt.Fprintf(&b, "做事准则：\n%s\n", emptyAs(digest.Principles, empty))
		fmt.Fprintf(&b, "开放闭环：未闭环 %d 条，前 %d 条：\n", digest.OpenLoopsTotal, len(digest.OpenLoops))
	}
	for _, item := range digest.OpenLoops {
		fmt.Fprintf(&b, "- %s\n", item)
	}
	if english {
		fmt.Fprintf(&b, "Agenda: %d next/waiting, first %d:\n", digest.AgendaTotal, len(digest.Agenda))
	} else {
		fmt.Fprintf(&b, "自主议程：next/waiting %d 条，前 %d 条：\n", digest.AgendaTotal, len(digest.Agenda))
	}
	for _, item := range digest.Agenda {
		fmt.Fprintf(&b, "- %s\n", item)
	}
	if english {
		b.WriteString("Full files:\n")
	} else {
		b.WriteString("全文：\n")
	}
	fmt.Fprintf(&b, "- identity: %s\n- principles: %s\n- loops: %s\n- agenda: %s", digest.Paths.Identity, digest.Paths.Principles, digest.Paths.Loops, digest.Paths.Agenda)
	return b.String()
}

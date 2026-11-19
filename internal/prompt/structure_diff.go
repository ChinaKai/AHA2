package prompt

import (
	"fmt"
	"strings"
)

// This is a deliberately small line diff. It exists only for the read-only
// structure preview, so the repository does not gain a general diff dependency
// for one diagnostic. The output is a standard unified diff; callers use it to
// see that the proposed four-block ordering only moves existing lines.

type diffOpKind byte

const (
	diffSame   diffOpKind = ' '
	diffDelete diffOpKind = '-'
	diffAdd    diffOpKind = '+'
)

type diffOp struct {
	kind     diffOpKind
	line     string
	beforeNo int
	afterNo  int
}

// unifiedDiff returns the diff text plus added and removed line counts. It
// returns an empty diff when the two prompts are byte-identical.
func unifiedDiff(beforeName, afterName, before, after string) (string, int, int) {
	if before == after {
		return "", 0, 0
	}
	ops := buildDiffOps(strings.Split(before, "\n"), strings.Split(after, "\n"))
	var changed []int
	added, removed := 0, 0
	for index := range ops {
		switch ops[index].kind {
		case diffAdd:
			changed = append(changed, index)
			added++
		case diffDelete:
			changed = append(changed, index)
			removed++
		}
	}
	if len(changed) == 0 {
		return "", 0, 0
	}

	const contextLines = 2
	type hunk struct{ start, end int } // end exclusive
	hunks := make([]hunk, 0, len(changed))
	for _, index := range changed {
		start := index - contextLines
		if start < 0 {
			start = 0
		}
		end := index + contextLines + 1
		if end > len(ops) {
			end = len(ops)
		}
		if len(hunks) > 0 && start <= hunks[len(hunks)-1].end {
			if end > hunks[len(hunks)-1].end {
				hunks[len(hunks)-1].end = end
			}
			continue
		}
		hunks = append(hunks, hunk{start: start, end: end})
	}

	var output strings.Builder
	fmt.Fprintf(&output, "--- %s\n+++ %s\n", beforeName, afterName)
	for _, item := range hunks {
		beforeStart, beforeCount := hunkRange(ops[item.start:item.end], true)
		afterStart, afterCount := hunkRange(ops[item.start:item.end], false)
		fmt.Fprintf(&output, "@@ -%d,%d +%d,%d @@\n", beforeStart, beforeCount, afterStart, afterCount)
		for _, op := range ops[item.start:item.end] {
			output.WriteByte(byte(op.kind))
			output.WriteString(op.line)
			output.WriteByte('\n')
		}
	}
	return output.String(), added, removed
}

func hunkRange(ops []diffOp, before bool) (start, count int) {
	if len(ops) == 0 {
		return 0, 0
	}
	start = ops[0].beforeNo
	if !before {
		start = ops[0].afterNo
	}
	for _, op := range ops {
		if before {
			if op.kind == diffSame || op.kind == diffDelete {
				count++
			}
		} else if op.kind == diffSame || op.kind == diffAdd {
			count++
		}
	}
	if count == 0 {
		start--
	}
	if start < 0 {
		start = 0
	}
	return start, count
}

// buildDiffOps computes a longest-common-subsequence line diff and stamps each
// operation with its 1-based line number on both sides.
func buildDiffOps(before, after []string) []diffOp {
	table := make([][]int, len(before)+1)
	for index := range table {
		table[index] = make([]int, len(after)+1)
	}
	for i := len(before) - 1; i >= 0; i-- {
		for j := len(after) - 1; j >= 0; j-- {
			switch {
			case before[i] == after[j]:
				table[i][j] = table[i+1][j+1] + 1
			case table[i+1][j] >= table[i][j+1]:
				table[i][j] = table[i+1][j]
			default:
				table[i][j] = table[i][j+1]
			}
		}
	}
	ops := make([]diffOp, 0, len(before)+len(after))
	i, j := 0, 0
	for i < len(before) && j < len(after) {
		switch {
		case before[i] == after[j]:
			ops = append(ops, diffOp{kind: diffSame, line: before[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, diffOp{kind: diffDelete, line: before[i]})
			i++
		default:
			ops = append(ops, diffOp{kind: diffAdd, line: after[j]})
			j++
		}
	}
	for ; i < len(before); i++ {
		ops = append(ops, diffOp{kind: diffDelete, line: before[i]})
	}
	for ; j < len(after); j++ {
		ops = append(ops, diffOp{kind: diffAdd, line: after[j]})
	}
	beforeNo, afterNo := 0, 0
	for index := range ops {
		switch ops[index].kind {
		case diffSame:
			beforeNo++
			afterNo++
			ops[index].beforeNo = beforeNo
			ops[index].afterNo = afterNo
		case diffDelete:
			beforeNo++
			ops[index].beforeNo = beforeNo
			ops[index].afterNo = afterNo + 1
		case diffAdd:
			afterNo++
			ops[index].beforeNo = beforeNo + 1
			ops[index].afterNo = afterNo
		}
	}
	return ops
}

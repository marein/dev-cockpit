package coder

import (
	"fmt"
	"strings"
)

// The record readers render an activity reading the same way, so a check pays
// the same for every coder: one line per message, the newest last.

// activityEntries is how many recorded messages a reading carries by default,
// activityLine how long one of its older lines may be.
const (
	activityEntries = 30
	activityLine    = 600
)

// ActivityBounds resolves how many entries a reading keeps and how long one
// line may be. The whole reading stays bounded by the budget (only a cut
// notice may stand a few runes past it), so fewer entries simply share the
// same budget among less lines; a budget of zero lifts the cap, and a line
// bound of zero means uncut.
func ActivityBounds(entries, budget int) (keep, line int) {
	keep = activityEntries
	if entries > 0 {
		keep = entries
	}
	if budget <= 0 {
		return keep, 0
	}
	line = activityLine
	if entries > 0 && budget/entries > line {
		line = budget / entries
	}
	return keep, line
}

// SpendActivityBudget spends the budget asymmetrically on the rendered lines.
// The newest line is the one a check judges against, so it gets the whole
// budget first and is cut only when it alone exceeds it; the older lines keep
// the per-line cut and share what is left, newest first, and whatever no
// longer fits falls off the top, oldest first. A budget of zero or less keeps
// every line whole.
func SpendActivityBudget(lines []string, lineRunes, budget int) string {
	if len(lines) == 0 {
		return ""
	}
	if budget <= 0 {
		return strings.Join(lines, "\n")
	}
	newest := CutActivityLine(lines[len(lines)-1], budget)
	budget -= len([]rune(newest))
	kept := []string{newest}
	for i := len(lines) - 2; i >= 0; i-- {
		cut := CutActivityLine(lines[i], lineRunes)
		cost := len([]rune(cut)) + 1
		if cost > budget {
			break
		}
		kept = append(kept, cut)
		budget -= cost
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return strings.Join(kept, "\n")
}

// OneLine folds a text onto one line.
func OneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// CutActivityLine cuts a line to max runes, visibly: a cut line says how much
// of the message is shown and how the rest is reached, never a bare ellipsis.
// A max of zero or less keeps the line whole.
func CutActivityLine(text string, max int) string {
	runes := []rune(text)
	if max <= 0 || len(runes) <= max {
		return text
	}
	return fmt.Sprintf("%s… [cut: %d of %d runes shown, use --full for the whole message]", string(runes[:max]), max, len(runes))
}

package assistant

import (
	"regexp"
	"strings"
)

// Verdict is what a wake concluded.
type Verdict string

const (
	VerdictDone    Verdict = "done"
	VerdictBlocked Verdict = "blocked"
	VerdictWorking Verdict = "working"
	VerdictNothing Verdict = "nothing"
	// VerdictExpired is not something a check answers. It is what the watcher
	// records when a job runs out of checks or out of time, so the message that
	// says nobody is steering any more looks like the other reports.
	VerdictExpired Verdict = "expired"
)

// News reports whether this verdict is worth the user's attention.
func (v Verdict) News() bool { return v == VerdictDone || v == VerdictBlocked }

// verdictPattern reads the verdict at the front of an answer. The separator is
// optional and may be a colon, a dash or a full stop, because that is the
// formatting a model drifts into; the word itself is the contract. A verdict
// that ends its own sentence is the everyday shape of the bare one, "Nothing.",
// and reading the stop as punctuation left it standing in front of the report.
var verdictPattern = regexp.MustCompile(`(?i)^\s*[*_#>\s]*(done|blocked|working|nothing)\b[*_]*\s*[:\-–.]?\s*[*_]*\s*`)

// verdictInText finds the contract form of a verdict after a model talked first:
// upper case and followed by a colon, which prose never is. Everything before it
// is thinking out loud and belongs in no note. The colon is required here, unlike
// at the front of an answer: a sentence may well contain the word in capitals,
// and taking that for a verdict would cut a real report in half.
var verdictInText = regexp.MustCompile(`(DONE|BLOCKED|WORKING|NOTHING)\b[*_]*\s*:\s*[*_]*\s*`)

// bareNothing finds the one verdict the contract spells without a colon after a
// model talked first: NOTHING, upper case, standing as its own word. The case
// is the guard against prose, a sentence that needs the word writes it small.
var bareNothing = regexp.MustCompile(`\bNOTHING\b`)

// otherVerdict is any other verdict word in capitals. An answer that carries
// one may be a real DONE or BLOCKED report whose form just failed to parse,
// and cutting it at a stray NOTHING would cut that report, so bare NOTHING
// only counts in an answer without them.
var otherVerdict = regexp.MustCompile(`\b(DONE|BLOCKED|WORKING)\b`)

// parseVerdict splits an answer into its verdict and the text that belongs to
// the user. The text starts at the verdict: a model that reasons first would
// otherwise put its planning into the job's line and into the report the user
// reads. NOTHING is the one verdict the contract spells bare, so after the
// colon forms failed it is recovered without one. An answer with no verdict at
// all counts as working, because dropping a real report would be worse than a
// line nobody asked for.
func parseVerdict(answer string) (Verdict, string) {
	text := strings.TrimSpace(answer)
	if text == "" {
		return VerdictNothing, ""
	}
	if match := verdictPattern.FindStringSubmatch(text); match != nil {
		return verdictOf(match[1]), strings.TrimSpace(text[len(match[0]):])
	}
	if match := verdictInText.FindStringSubmatchIndex(text); match != nil {
		word := text[match[2]:match[3]]
		return verdictOf(word), strings.TrimSpace(text[match[1]:])
	}
	if match := bareNothing.FindStringIndex(text); match != nil && !otherVerdict.MatchString(text) {
		return VerdictNothing, strings.TrimSpace(text[match[1]:])
	}
	return VerdictWorking, text
}

func verdictOf(word string) Verdict {
	switch strings.ToLower(word) {
	case "done":
		return VerdictDone
	case "blocked":
		return VerdictBlocked
	case "nothing":
		return VerdictNothing
	default:
		return VerdictWorking
	}
}

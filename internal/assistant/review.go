package assistant

import (
	"fmt"
	"strings"

	"github.com/marein/dev-cockpit/internal/plaintext"
)

// ReviewReport is a note on the change request of the branch the forge
// service polls of a bound project, written by somebody other than the
// account of the host's token, as runServe hands it here once per note. The
// assistant knows no forge, so every word the forge has for it travels in
// the report: Forge is what the forge is called and Address the
// repository's page without its scheme. Author wrote Body, URL is the note's
// page. A note on the diff names its file by Path and the Line it stands at
// on Side, 0 for one on the whole file, and Outdated says the diff moved on
// from it; a general one names no path. Reply says the note answers its
// thread, which Opener began, empty for an author the forge no longer
// names, and Resolved whether the thread is. Thread is the forge's id of the
// thread, which the writes on a thread take, "" for a note the forge keeps
// alone, and Resolvable says the forge resolves it too.
type ReviewReport struct {
	Project    string
	Branch     string
	Forge      string
	Address    string
	Change     ChangeRequest
	Author     string
	Body       string
	URL        string
	Path       string
	Line       int
	Side       string
	Outdated   bool
	Reply      bool
	Opener     string
	Resolved   bool
	Thread     string
	Resolvable bool
}

// reviewBodyRunes bounds what an event quotes of a note, so the line that
// reads the rest still fits the event; forge-show reads it whole.
const reviewBodyRunes = 2000

// reviewAuthor is what an event calls who wrote a note, somebody where the
// forge names nobody, a deleted account.
func reviewAuthor(author string) string {
	if strings.TrimSpace(author) == "" {
		return "somebody"
	}
	return author
}

// reviewHeadline is the one line a review comment event is read by: who
// commented, the branch, the project and the change request, "Review
// comment: alice on feature in shop, merge request !12".
func reviewHeadline(report ReviewReport) string {
	return "Review comment: " + oneLine(reviewAuthor(report.Author)) + " on " + oneLine(report.Branch) + " in " + oneLine(report.Project) +
		", " + oneLine(report.Change.Noun) + " " + oneLine(report.Change.Ref)
}

// reviewEventData is what the event's text is handed. Every word from the
// forge or the repository is written visible, the note's text as a quote
// whose every line starts with the quote's mark, so none of them starts a
// line of the prompt it stands in.
type reviewEventData struct {
	Project, Branch, Forge, Address, Author, Did, Where, URL, Thread string
	Change                                                           ChangeRequest
	Resolved, Resolvable, Cut                                        bool
	Text                                                             string
	Cockpit                                                          string
}

// reviewDid is what the author did, commented or answered a thread, and
// whose.
func reviewDid(report ReviewReport) string {
	opener := strings.TrimSpace(report.Opener)
	switch {
	case !report.Reply:
		return "commented"
	case opener == "":
		return "answered a thread"
	case opener == strings.TrimSpace(report.Author):
		return "answered their own thread"
	}
	return "answered " + plaintext.Visible(opener) + "'s thread"
}

// reviewWhere is where a note stands, in the words of the event's text: at
// a line of a file of the diff, on a whole file, or on the conversation.
func reviewWhere(report ReviewReport) string {
	path := strings.TrimSpace(report.Path)
	switch {
	case path == "":
		return "on the conversation"
	case report.Line <= 0:
		path = fmt.Sprintf("on the whole file %q", plaintext.Visible(path))
	default:
		path = fmt.Sprintf("on %q %s line %d", plaintext.Visible(path), plaintext.Visible(report.Side), report.Line)
	}
	if report.Outdated {
		path += " as an earlier version of the diff had it"
	}
	return path
}

// reviewQuote is the note's text as a quote, cut at reviewBodyRunes, and
// whether it was cut.
func reviewQuote(body string) (string, bool) {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if body == "" {
		return "> (no text)", false
	}
	cut := truncateRunes(body, reviewBodyRunes)
	lines := strings.Split(cut, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight("> "+plaintext.Visible(line), " ")
	}
	return strings.Join(lines, "\n"), cut != body
}

// reviewBody is the rest of the event, what a reaction reads under the
// headline: who wrote the note where, the change request with its title,
// the note's page and text, and the command that reads every thread.
func reviewBody(report ReviewReport) string {
	data := reviewEventData{
		Project:    plaintext.Visible(report.Project),
		Branch:     plaintext.Visible(report.Branch),
		Forge:      plaintext.Visible(report.Forge),
		Address:    plaintext.Visible(report.Address),
		Author:     plaintext.Visible(reviewAuthor(report.Author)),
		Did:        reviewDid(report),
		Where:      reviewWhere(report),
		URL:        plaintext.Visible(report.URL),
		Thread:     plaintext.Visible(report.Thread),
		Change:     report.Change.visible(),
		Resolved:   report.Resolved,
		Resolvable: report.Resolvable,
		Cockpit:    shortCockpit,
	}
	data.Text, data.Cut = reviewQuote(report.Body)
	return strings.TrimSuffix(render("review_event.md.tmpl", data), "\n")
}

// ReviewComment publishes a note on a change request as an event. It is
// everybody's, a trigger of any assistant on the project takes it.
func (r *Reactor) ReviewComment(report ReviewReport) {
	r.Publish(CockpitEvent{
		Source:   EventForge,
		Kind:     ReviewKindComment,
		Project:  report.Project,
		Branch:   report.Branch,
		Time:     r.now().UTC(),
		Headline: reviewHeadline(report),
		Body:     reviewBody(report),
	})
}

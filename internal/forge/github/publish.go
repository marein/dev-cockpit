package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// pullFile is one file of a pull request's diff, its hunks in patch. A
// binary file or one too large comes without them.
type pullFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Patch            string `json:"patch"`
}

// ChangeDiff reads the commit the pull request's head stands at and its
// files page by page, the first ones of more than one read takes.
func (Provider) ChangeDiff(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change) (forge.Diff, error) {
	a := api(c, b.Host, token)
	at := fmt.Sprintf("%s/pulls/%d", repository(b), ch.Number)
	var p pull
	if _, err := a.Get(ctx, at, &p); err != nil {
		return forge.Diff{}, readRefusal(a, b, err)
	}
	files, partial, err := linked[pullFile](ctx, a, at+"/files")
	if err != nil {
		return forge.Diff{}, readRefusal(a, b, err)
	}
	d := forge.Diff{Head: p.Head.SHA, Files: map[string]forge.DiffFile{}, Partial: partial}
	for _, f := range files {
		if f.Status == "removed" {
			continue
		}
		old := f.Filename
		if f.PreviousFilename != "" {
			old = f.PreviousFilename
		}
		d.Files[f.Filename] = forge.DiffFile{OldPath: old, Lines: forge.HunkLines(f.Patch)}
	}
	return d, nil
}

// PublishNote posts a review comment of its own at the line p names on the
// right side, the new one, of the diff at the head commit.
func (Provider) PublishNote(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change, d forge.Diff, p forge.Placed) (forge.Note, error) {
	a := api(c, b.Host, token)
	body := map[string]any{"body": p.Body, "commit_id": d.Head, "path": p.Path, "line": p.Line, "side": "RIGHT"}
	var made struct {
		NodeID    string    `json:"node_id"`
		Body      string    `json:"body"`
		HTMLURL   string    `json:"html_url"`
		CreatedAt time.Time `json:"created_at"`
		User      *login    `json:"user"`
	}
	if err := a.Post(ctx, fmt.Sprintf("%s/pulls/%d/comments", repository(b), ch.Number), body, &made); err != nil {
		return forge.Note{}, commentRefusal(a, b, ch.Number, err)
	}
	if made.NodeID == "" {
		return forge.Note{}, fmt.Errorf("GitHub answered the comment on pull request #%d without its id.", ch.Number)
	}
	return forge.Note{ID: made.NodeID, Author: made.User.name(), Body: made.Body, Created: made.CreatedAt, URL: made.HTMLURL}, nil
}

// PublishGeneral posts a comment on the pull request's conversation, which
// GitHub keeps as a comment of its issue, a note alone and no thread.
func (Provider) PublishGeneral(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change, body string) (forge.Note, error) {
	a := api(c, b.Host, token)
	var made issueComment
	if err := a.Post(ctx, fmt.Sprintf("%s/issues/%d/comments", repository(b), ch.Number), map[string]any{"body": body}, &made); err != nil {
		return forge.Note{}, commentRefusal(a, b, ch.Number, err)
	}
	if made.NodeID == "" {
		return forge.Note{}, fmt.Errorf("GitHub answered the comment on pull request #%d without its id.", ch.Number)
	}
	return forge.Note{ID: made.NodeID, Author: made.User.name(), Body: made.Body, Created: made.CreatedAt, URL: made.HTMLURL}, nil
}

// commentRefusal says what an answer GitHub gave to a review comment means:
// a token that may not, which GitHub answers with 403, or with 404 where it
// hides the repository from the token; a comment GitHub refuses in words of
// its own; else what readRefusal says.
func commentRefusal(a forge.API, b forge.Binding, number int, err error) error {
	var status *forge.StatusError
	if !errors.As(err, &status) {
		return readRefusal(a, b, err)
	}
	scope := tokenScope("Pull requests")
	message := status.Message()
	switch {
	case status.Code == http.StatusForbidden && (message == "" || strings.HasPrefix(message, "Resource not accessible")):
		return fmt.Errorf("The token may not comment on pull requests in %s: %s", b.Address(), scope)
	case status.Code == http.StatusNotFound:
		return fmt.Errorf("GitHub knows no pull request #%d in %s, or the token may not comment on it: %s", number, b.Address(), scope)
	case message != "" && (status.Code == http.StatusForbidden || status.Code == http.StatusUnprocessableEntity):
		return fmt.Errorf("GitHub refused the comment on pull request #%d: %s.", number, strings.TrimRight(message, "."))
	}
	return readRefusal(a, b, err)
}

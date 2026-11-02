package github

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// The bounds of one read of a pull request's notes: the review threads of
// one query and the comments read of each, and the pages of a REST list,
// the conversation's comments and the reviews.
const (
	threadsPerQuery = 50
	notesPerThread  = 50
	maxThreadPages  = 10
	notesPerPage    = 100
	maxNotePages    = 10
)

// threadsQuery reads review threads with where they stand and their
// comments. Only GraphQL knows whether a thread is resolved; a thread on a
// whole file has no line, an outdated one only the line it was written at.
// The diff hunk of the first comment, which ends at the line it was written
// on, is read alone, the other comments' hunks repeat it.
const threadsQuery = `query($owner: String!, $name: String!, $number: Int!, $first: Int!, $notes: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: $first, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id isResolved isOutdated path line originalLine diffSide
          comments(first: $notes) {
            pageInfo { hasNextPage }
            nodes { id state body url createdAt author { login } }
          }
          opening: comments(first: 1) { nodes { diffHunk } }
        }
      }
    }
  }
}`

type reviewThread struct {
	ID           string `json:"id"`
	IsResolved   bool   `json:"isResolved"`
	IsOutdated   bool   `json:"isOutdated"`
	Path         string `json:"path"`
	Line         *int   `json:"line"`
	OriginalLine *int   `json:"originalLine"`
	DiffSide     string `json:"diffSide"`
	Comments     struct {
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
		Nodes []struct {
			ID        string    `json:"id"`
			State     string    `json:"state"`
			Body      string    `json:"body"`
			URL       string    `json:"url"`
			CreatedAt time.Time `json:"createdAt"`
			Author    *login    `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
	Opening struct {
		Nodes []struct {
			DiffHunk string `json:"diffHunk"`
		} `json:"nodes"`
	} `json:"opening"`
}

type login struct {
	Login string `json:"login"`
}

func (l *login) name() string {
	if l == nil {
		return ""
	}
	return l.Login
}

// issueComment is a comment of a pull request's conversation, which GitHub
// keeps as the comments of its issue.
type issueComment struct {
	NodeID    string    `json:"node_id"`
	Body      string    `json:"body"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	User      *login    `json:"user"`
}

// pullReview is a review of a pull request, whose body is a note of the
// conversation. A pending one is its author's alone and not submitted.
type pullReview struct {
	NodeID      string    `json:"node_id"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	HTMLURL     string    `json:"html_url"`
	SubmittedAt time.Time `json:"submitted_at"`
	User        *login    `json:"user"`
}

// graphql is the GraphQL API of the host, github.com's on its own host, an
// Enterprise Server's below its web address.
func graphql(c *forge.Client, h forge.Host, token string) forge.API {
	base := strings.TrimRight(h.WebURL, "/") + "/api/graphql"
	if h.WebURL == webURL {
		base = "https://api.github.com/graphql"
	}
	return c.API(base, h, token, nil)
}

// Threads reads the review threads of the pull request over GraphQL, and its
// conversation, the comments and the bodies of the submitted reviews, over
// REST, as general threads of one note each in the order they were written,
// each a note alone that takes no reply. A comment of a review still pending
// is left out.
func (Provider) Threads(ctx context.Context, c *forge.Client, b forge.Binding, token string, number int) (forge.Review, error) {
	owner, name, ok := strings.Cut(b.Path, "/")
	if !ok || strings.Contains(name, "/") {
		return forge.Review{}, fmt.Errorf("A GitHub repository path is owner/name, %s is none.", b.Address())
	}
	review, err := reviewThreads(ctx, graphql(c, b.Host, token), b, owner, name, number)
	if err != nil {
		return forge.Review{}, err
	}
	a := api(c, b.Host, token)
	repo := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	comments, commentsPartial, err := linked[issueComment](ctx, a, fmt.Sprintf("%s/issues/%d/comments", repo, number))
	if err != nil {
		return forge.Review{}, notesRefusal(a, b, err)
	}
	reviews, reviewsPartial, err := linked[pullReview](ctx, a, fmt.Sprintf("%s/pulls/%d/reviews", repo, number))
	if err != nil {
		return forge.Review{}, notesRefusal(a, b, err)
	}
	for _, c := range comments {
		review.General = append(review.General, single(c.NodeID, c.User, c.Body, c.CreatedAt, c.HTMLURL))
	}
	for _, r := range reviews {
		if r.State == "PENDING" || strings.TrimSpace(r.Body) == "" {
			continue
		}
		review.General = append(review.General, single(r.NodeID, r.User, r.Body, r.SubmittedAt, r.HTMLURL))
	}
	slices.SortStableFunc(review.General, func(x, y forge.Thread) int {
		return cmp.Compare(x.Notes[0].Created.UnixNano(), y.Notes[0].Created.UnixNano())
	})
	review.Partial = review.Partial || commentsPartial || reviewsPartial
	return review, nil
}

func single(id string, author *login, body string, at time.Time, page string) forge.Thread {
	return forge.Thread{ID: id, Single: true, Notes: []forge.Note{{ID: id, Author: author.name(), Body: body, Created: at, URL: page}}}
}

// reviewThreads reads the review threads page by page.
func reviewThreads(ctx context.Context, a forge.API, b forge.Binding, owner, name string, number int) (forge.Review, error) {
	var review forge.Review
	after := ""
	for page := 1; ; page++ {
		variables := map[string]any{"owner": owner, "name": name, "number": number, "first": threadsPerQuery, "notes": notesPerThread, "after": nil}
		if after != "" {
			variables["after"] = after
		}
		var data struct {
			Repository *struct {
				PullRequest *struct {
					ReviewThreads struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []reviewThread `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		if err := a.Query(ctx, threadsQuery, variables, &data); err != nil {
			return forge.Review{}, threadsRefusal(a, b, number, err)
		}
		if data.Repository == nil || data.Repository.PullRequest == nil {
			return forge.Review{}, fmt.Errorf("GitHub knows no pull request #%d in %s, or the token may not read it.", number, b.Address())
		}
		threads := data.Repository.PullRequest.ReviewThreads
		for _, t := range threads.Nodes {
			if thread, ok := t.neutral(); ok {
				review.Diff = append(review.Diff, thread)
			}
			review.Partial = review.Partial || t.Comments.PageInfo.HasNextPage
		}
		if !threads.PageInfo.HasNextPage || threads.PageInfo.EndCursor == "" {
			return review, nil
		}
		if page == maxThreadPages {
			review.Partial = true
			return review, nil
		}
		after = threads.PageInfo.EndCursor
	}
}

// neutral is the thread with its submitted comments, none when it has
// nothing else. Its line is the one it stands at now, else the one it was
// written at, and its text the last line of the first comment's diff hunk,
// which a thread GitHub keeps at its line unchanged.
func (t reviewThread) neutral() (forge.Thread, bool) {
	thread := forge.Thread{ID: t.ID, Path: t.Path, Outdated: t.IsOutdated, Resolvable: true, Resolved: t.IsResolved}
	line := t.Line
	if line == nil {
		line = t.OriginalLine
	}
	if line != nil {
		thread.Line, thread.Side = *line, forge.NewSide
		if t.DiffSide == "LEFT" {
			thread.Side = forge.OldSide
		}
		if len(t.Opening.Nodes) > 0 {
			thread.Quote = hunkLine(t.Opening.Nodes[0].DiffHunk)
		}
	}
	for _, c := range t.Comments.Nodes {
		if c.State == "PENDING" {
			continue
		}
		thread.Notes = append(thread.Notes, forge.Note{ID: c.ID, Author: c.Author.name(), Body: c.Body, Created: c.CreatedAt, URL: c.URL})
	}
	return thread, len(thread.Notes) > 0
}

// hunkLine is the text of the last line of a diff hunk, without the mark
// that says which side it stands on.
func hunkLine(hunk string) string {
	hunk = strings.TrimRight(hunk, "\n")
	last := hunk[strings.LastIndexByte(hunk, '\n')+1:]
	if strings.HasPrefix(last, "@@") || last == "" {
		return ""
	}
	return last[1:]
}

// linked reads a REST list page by page while the answer's Link names a next
// one, and says whether it had more than the pages it reads. The next page is
// asked for at the list's own path, never at an address the answer names.
func linked[T any](ctx context.Context, a forge.API, path string) ([]T, bool, error) {
	var all []T
	for page := 1; ; page++ {
		var found []T
		header, err := a.Get(ctx, fmt.Sprintf("%s?per_page=%d&page=%d", path, notesPerPage, page), &found)
		if err != nil {
			return nil, false, err
		}
		all = append(all, found...)
		if !strings.Contains(header.Get("Link"), `rel="next"`) || len(found) == 0 {
			return all, false, nil
		}
		if page == maxNotePages {
			return all, true, nil
		}
	}
}

// threadsRefusal says what an answer of the GraphQL API to the read of the
// review threads means.
func threadsRefusal(a forge.API, b forge.Binding, number int, err error) error {
	var refused *forge.GraphQLError
	if errors.As(err, &refused) {
		switch refused.Type {
		case "NOT_FOUND":
			return fmt.Errorf("GitHub knows no pull request #%d in %s, or the token may not read it.", number, b.Address())
		case "FORBIDDEN", "INSUFFICIENT_SCOPES":
			return fmt.Errorf("The token may not read the review threads of %s.", b.Address())
		}
		return fmt.Errorf("GitHub refused the read of the review threads of #%d: %s.", number, strings.TrimRight(refused.Message, "."))
	}
	var status *forge.StatusError
	if errors.As(err, &status) && status.Code == http.StatusNotFound {
		return fmt.Errorf("No GitHub GraphQL API answers at %s: check the kind and the web address.", a.Base())
	}
	return notesRefusal(a, b, err)
}

// notesRefusal says what an answer GitHub gave to a read of a pull request's
// notes means: one it may not read, else what readRefusal says.
func notesRefusal(a forge.API, b forge.Binding, err error) error {
	var status *forge.StatusError
	if errors.As(err, &status) && status.Code == http.StatusForbidden {
		return fmt.Errorf("The token may not read the review threads of %s.", b.Address())
	}
	return readRefusal(a, b, err)
}

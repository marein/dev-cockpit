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

// replyMutation answers a review thread by its id. The REST reply needs the
// number of the thread's first comment, which GraphQL names only by a field
// GitHub retires or one an older Enterprise Server lacks.
const replyMutation = `mutation($thread: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $thread, body: $body}) {
    comment { id state body url createdAt author { login } }
  }
}`

// resolveMutation resolves a review thread, which only GraphQL can.
const resolveMutation = `mutation($thread: ID!) {
  resolveReviewThread(input: {threadId: $thread}) {
    thread { id isResolved }
  }
}`

// Reply posts a comment of its own in the review thread. A reply GitHub
// files under the account's pending review stays unseen until that review
// is submitted, so it is said and not answered as posted.
func (Provider) Reply(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change, t forge.Thread, body string) (forge.Note, error) {
	a := graphql(c, b.Host, token)
	var data struct {
		Reply *struct {
			Comment *struct {
				ID        string    `json:"id"`
				State     string    `json:"state"`
				Body      string    `json:"body"`
				URL       string    `json:"url"`
				CreatedAt time.Time `json:"createdAt"`
				Author    *login    `json:"author"`
			} `json:"comment"`
		} `json:"addPullRequestReviewThreadReply"`
	}
	if err := a.Query(ctx, replyMutation, map[string]any{"thread": t.ID, "body": body}, &data); err != nil {
		return forge.Note{}, threadWriteRefusal(a, b, ch.Number, err, "answer")
	}
	if data.Reply == nil || data.Reply.Comment == nil || data.Reply.Comment.ID == "" {
		return forge.Note{}, fmt.Errorf("GitHub answered the reply on pull request #%d without its comment.", ch.Number)
	}
	made := data.Reply.Comment
	if made.State == "PENDING" {
		return forge.Note{}, fmt.Errorf("GitHub put the reply on pull request #%d into the token's pending review, it shows once that review is submitted.", ch.Number)
	}
	return forge.Note{ID: made.ID, Author: made.Author.name(), Body: made.Body, Created: made.CreatedAt, URL: made.URL}, nil
}

// Resolve resolves the review thread.
func (Provider) Resolve(ctx context.Context, c *forge.Client, b forge.Binding, token string, ch forge.Change, t forge.Thread) error {
	a := graphql(c, b.Host, token)
	var data struct {
		Resolve *struct {
			Thread *struct {
				IsResolved bool `json:"isResolved"`
			} `json:"thread"`
		} `json:"resolveReviewThread"`
	}
	if err := a.Query(ctx, resolveMutation, map[string]any{"thread": t.ID}, &data); err != nil {
		return threadWriteRefusal(a, b, ch.Number, err, "resolve")
	}
	if data.Resolve == nil || data.Resolve.Thread == nil || !data.Resolve.Thread.IsResolved {
		return fmt.Errorf("GitHub did not answer the thread on pull request #%d as resolved.", ch.Number)
	}
	return nil
}

// threadWriteRefusal says what an answer of the GraphQL API to a write on a
// review thread means: a token that may not, a thread GitHub does not know,
// a write it refuses in words of its own.
func threadWriteRefusal(a forge.API, b forge.Binding, number int, err error, act string) error {
	scope := tokenScope("Pull requests")
	var refused *forge.GraphQLError
	if errors.As(err, &refused) {
		switch refused.Type {
		case "FORBIDDEN", "INSUFFICIENT_SCOPES":
			return fmt.Errorf("The token may not %s review threads in %s: %s", act, b.Address(), scope)
		case "NOT_FOUND":
			return fmt.Errorf("GitHub knows no such review thread on pull request #%d in %s, or the token may not %s it: %s", number, b.Address(), act, scope)
		}
		return fmt.Errorf("GitHub refused to %s the thread on pull request #%d: %s.", act, number, strings.TrimRight(refused.Message, "."))
	}
	var status *forge.StatusError
	switch {
	case errors.As(err, &status) && status.Code == http.StatusNotFound:
		return fmt.Errorf("No GitHub GraphQL API answers at %s: check the kind and the web address.", a.Base())
	case errors.As(err, &status) && status.Code == http.StatusForbidden:
		return fmt.Errorf("The token may not %s review threads in %s: %s", act, b.Address(), scope)
	}
	return readRefusal(a, b, err)
}

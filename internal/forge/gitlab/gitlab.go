// Package gitlab is the GitLab provider of the forge service.
package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/marein/dev-cockpit/internal/forge"
)

// Kind is GitLab as a sort of forge.
var Kind = forge.Kind{ID: "gitlab", Label: "GitLab", Icon: "ti-brand-gitlab", ChangeNoun: "merge request", ChangeMark: "!", Retry: "Retry"}

// The forge reads the API as GitLab 15 documents it: the fields of merge
// requests, pipelines, jobs and discussions it names. An older server may
// lack any of them, so it is refused at the door instead of failing later
// on a field.
const minimumMajor, minimumMinor = 15, 0

// Builtin is gitlab.com. Its SSH also answers as altssh.gitlab.com on port
// 443, for networks that close port 22.
func Builtin() forge.Host {
	return forge.BuiltIn(Kind, "https://gitlab.com", "altssh.gitlab.com")
}

// Provider is GitLab, gitlab.com and self hosted alike, over its REST API v4.
type Provider struct{}

func (Provider) Kind() forge.Kind {
	return Kind
}

func api(c *forge.Client, h forge.Host, token string) forge.API {
	return c.API(strings.TrimRight(h.WebURL, "/")+"/api/v4", h, token, nil)
}

// TokenPage is the page that creates a personal access token with the scope
// api, the one that also writes.
func (Provider) TokenPage(h forge.Host, name string) string {
	return strings.TrimRight(h.WebURL, "/") + "/-/user_settings/personal_access_tokens?name=" + url.QueryEscape(name) + "&scopes=api"
}

// Test reads the server's version first, which tells a GitLab from anything
// else and an old one from one the forge reads, then the token's user.
func (Provider) Test(ctx context.Context, c *forge.Client, h forge.Host, token string) (forge.Connection, error) {
	a := api(c, h, token)
	var version struct {
		Version string `json:"version"`
	}
	if _, err := a.Get(ctx, "/version", &version); err != nil {
		return forge.Connection{}, refusal(a, err)
	}
	major, minor, ok := release(version.Version)
	if !ok {
		return forge.Connection{}, noGitLab(a)
	}
	if major < minimumMajor || (major == minimumMajor && minor < minimumMinor) {
		return forge.Connection{}, fmt.Errorf("The server runs GitLab %d.%d, the forge needs GitLab %d.%d or newer.", major, minor, minimumMajor, minimumMinor)
	}
	var user struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if _, err := a.Get(ctx, "/user", &user); err != nil {
		return forge.Connection{}, refusal(a, err)
	}
	if user.Username == "" {
		return forge.Connection{}, noGitLab(a)
	}
	return forge.Connection{Account: user.Username, Name: user.Name, Server: "GitLab " + version.Version}, nil
}

// release reads major and minor off a version like 17.5.1-ee.
func release(version string) (int, int, bool) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	digits := strings.IndexFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' })
	if digits < 0 {
		digits = len(parts[1])
	}
	minor, err := strconv.Atoi(parts[1][:digits])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// refusal says what an answer GitLab gave means for the token.
func refusal(a forge.API, err error) error {
	var status *forge.StatusError
	switch {
	case errors.Is(err, forge.ErrNotJSON):
		return noGitLab(a)
	case !errors.As(err, &status):
		return err
	case status.Code == http.StatusUnauthorized:
		return errors.New("GitLab refused the token: it is wrong, expired or revoked.")
	case status.Code == http.StatusForbidden && insufficientScope(status.Body):
		return errors.New("The token may not read the API: give it the scope read_api, or api to also write.")
	case status.Code == http.StatusNotFound:
		return noGitLab(a)
	}
	return err
}

func insufficientScope(body []byte) bool {
	var answer struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(body, &answer) == nil && answer.Error == "insufficient_scope"
}

func noGitLab(a forge.API) error {
	return fmt.Errorf("No GitLab API answers at %s: check the kind and the web address.", a.Base())
}

// asText is a body GitLab posts as the text it is: it runs a line opening
// with a slash as a quick action instead, /merge and /title among them, and
// answers a body of only those without a note. Which of these lines it skips
// in a code, quote or HTML block follows its own reading, where one block
// hides the start of the next, so every such line goes behind a backslash:
// Markdown shows the slash, a code block the backslash too, text either way.
// GitLab deletes every carriage return before it reads, so those before the
// slash do not count.
func asText(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, "\r"), "/") {
			lines[i] = `\` + line
		}
	}
	return strings.Join(lines, "\n")
}

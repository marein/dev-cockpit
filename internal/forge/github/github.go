// Package github is the GitHub provider of the forge service.
package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/marein/dev-cockpit/internal/forge"
)

// Kind is GitHub as a sort of forge.
var Kind = forge.Kind{ID: "github", Label: "GitHub", Icon: "ti-brand-github", ChangeNoun: "pull request", ChangeMark: "#", Retry: "Re-run"}

const webURL = "https://github.com"

// Builtin is github.com. Its SSH also answers as ssh.github.com on port 443,
// for networks that close port 22.
func Builtin() forge.Host {
	return forge.BuiltIn(Kind, webURL, "ssh.github.com")
}

// Provider is GitHub, github.com and GitHub Enterprise Server alike, over
// its REST API.
type Provider struct{}

func (Provider) Kind() forge.Kind {
	return Kind
}

// api is github.com's API on its own host, an Enterprise Server's below its
// web address.
func api(c *forge.Client, h forge.Host, token string) forge.API {
	return c.API(apiBase(h), h, token, headers())
}

func apiBase(h forge.Host) string {
	if h.WebURL == webURL {
		return "https://api.github.com"
	}
	return strings.TrimRight(h.WebURL, "/") + "/api/v3"
}

// headers are what every request to GitHub's API sends.
func headers() http.Header {
	return http.Header{
		"Accept":               {"application/vnd.github+json"},
		"X-Github-Api-Version": {"2022-11-28"},
	}
}

// TokenPage is the page that creates a classic token, which every
// Enterprise Server knows, with the broadest scopes a forge feature may need
// short of any admin: repo and workflow for the repositories and their
// Actions, read:org for the teams of an organization.
func (Provider) TokenPage(h forge.Host, name string) string {
	return strings.TrimRight(h.WebURL, "/") + "/settings/tokens/new?description=" + url.QueryEscape(name) + "&scopes=repo,workflow,read:org"
}

// Test reads the token's user. An Enterprise Server names its version in a
// header of every answer.
func (Provider) Test(ctx context.Context, c *forge.Client, h forge.Host, token string) (forge.Connection, error) {
	a := api(c, h, token)
	var user struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	header, err := a.Get(ctx, "/user", &user)
	if err != nil {
		return forge.Connection{}, refusal(a, err)
	}
	if user.Login == "" {
		return forge.Connection{}, noGitHub(a)
	}
	server := "GitHub"
	if h.WebURL != webURL {
		server = strings.TrimSpace("GitHub Enterprise Server " + header.Get("X-GitHub-Enterprise-Version"))
	}
	return forge.Connection{Account: user.Login, Name: user.Name, Server: server}, nil
}

// refusal says what an answer GitHub gave means for the token.
func refusal(a forge.API, err error) error {
	var status *forge.StatusError
	switch {
	case errors.Is(err, forge.ErrNotJSON):
		return noGitHub(a)
	case !errors.As(err, &status):
		return err
	case status.Code == http.StatusUnauthorized:
		return errors.New("GitHub refused the token: it is wrong, expired or revoked.")
	case status.Code == http.StatusNotFound:
		return noGitHub(a)
	}
	return err
}

func noGitHub(a forge.API) error {
	return fmt.Errorf("No GitHub API answers at %s: check the kind and the web address.", a.Base())
}

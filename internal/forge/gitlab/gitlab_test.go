package gitlab_test

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

const token = "glpat-Xq3vR8sT2uW5yZ7aB9cD"

// The answers of a GitLab 17.5 as its API sends them, trimmed to a few of
// their fields.
const (
	versionAnswer = `{"version":"%s","revision":"8ff7d9d4d4a","kas":{"enabled":true,"externalUrl":"wss://gitlab.example.com/-/kubernetes-agent/","version":"17.5.1"},"enterprise":false}`
	userAnswer    = `{"id":1,"username":"root","name":"Administrator","state":"active","locked":false,"avatar_url":"https://www.gravatar.com/avatar/x?s=80&d=identicon","web_url":"https://gitlab.example.com/root","created_at":"2024-01-01T00:00:00.000Z","bot":false,"is_admin":true,"two_factor_enabled":false,"external":false}`
	scopeAnswer   = `{"error":"insufficient_scope","error_description":"The request requires higher privileges than provided by the access token.","scope":"api read_api read_user"}`
)

// fakeGitLab answers the API under prefix like GitLab does for the token,
// refusing every other one, in the given version.
func fakeGitLab(t *testing.T, prefix, version string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
			return
		}
		switch r.URL.Path {
		case prefix + "/api/v4/version":
			fmt.Fprintf(w, versionAnswer, version)
		case prefix + "/api/v4/user":
			fmt.Fprint(w, userAnswer)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"404 Not Found"}`)
		}
	})
}

func test(t *testing.T, h forge.Host, tok string) (forge.Connection, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := gitlab.Provider{}.Test(ctx, forge.NewClient(), h, tok)
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return conn, err
}

func host(webURL string) forge.Host {
	return forge.Host{ID: "h", Kind: gitlab.Kind, WebURL: webURL}
}

// The token page stands below the web address, a path of its own included,
// with the name and the scope api in its address, on gitlab.com and a self
// managed server alike.
func TestTheTokenPageAsksForTheScopeAPI(t *testing.T) {
	for _, c := range []struct {
		host forge.Host
		want string
	}{
		{gitlab.Builtin(), "https://gitlab.com/-/user_settings/personal_access_tokens?name=dev+cockpit&scopes=api"},
		{host("https://example.com/gitlab/"), "https://example.com/gitlab/-/user_settings/personal_access_tokens?name=dev+cockpit&scopes=api"},
	} {
		if got := (gitlab.Provider{}).TokenPage(c.host, "dev cockpit"); got != c.want {
			t.Fatalf("%s reads %s", c.host.WebURL, got)
		}
	}
}

func TestATokenShowsItsUserAndTheVersion(t *testing.T) {
	srv := httptest.NewServer(fakeGitLab(t, "/gitlab", "17.5.1-ee"))
	defer srv.Close()
	conn, err := test(t, host(srv.URL+"/gitlab"), token)
	if err != nil || conn != (forge.Connection{Account: "root", Name: "Administrator", Server: "GitLab 17.5.1-ee"}) {
		t.Fatalf("got %+v, %v", conn, err)
	}
}

func TestTheMinimumVersionHolds(t *testing.T) {
	for version, says := range map[string]string{
		"14.10.5":    "The server runs GitLab 14.10, the forge needs GitLab 15.0 or newer.",
		"9.5.0":      "The server runs GitLab 9.5, the forge needs GitLab 15.0 or newer.",
		"15.0.0":     "",
		"16.11.10":   "",
		"18.5.0-pre": "",
	} {
		srv := httptest.NewServer(fakeGitLab(t, "", version))
		_, err := test(t, host(srv.URL), token)
		srv.Close()
		if (says == "" && err != nil) || (says != "" && (err == nil || err.Error() != says)) {
			t.Errorf("GitLab %s: %v, want %q", version, err, says)
		}
	}
}

func TestFailuresAreSaidPlainly(t *testing.T) {
	gitlabAt := httptest.NewServer(fakeGitLab(t, "", "17.5.1"))
	defer gitlabAt.Close()
	githubAt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}`)
	}))
	defer githubAt.Close()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!DOCTYPE html><title>Welcome</title>")
	}))
	defer page.Close()
	scope := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", `Bearer realm="Protected by OAuth 2", error="insufficient_scope"`)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, scopeAnswer)
	}))
	defer scope.Close()
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://gitlab.example.com"+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer moved.Close()
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer stuck.Close()
	tls := httptest.NewTLSServer(fakeGitLab(t, "", "17.5.1"))
	defer tls.Close()
	cases := []struct {
		name string
		host forge.Host
		tok  string
		says string
	}{
		{"bad token", host(gitlabAt.URL), "glpat-wrong-wrong-wrong", "GitLab refused the token: it is wrong, expired or revoked."},
		{"missing scope", host(scope.URL), token, "The token may not read the API: give it the scope read_api, or api to also write."},
		{"wrong kind, a GitHub", host(githubAt.URL), token, "No GitLab API answers at " + githubAt.URL + "/api/v4: check the kind and the web address."},
		{"wrong kind, a web page", host(page.URL), token, "No GitLab API answers at " + page.URL + "/api/v4: check the kind and the web address."},
		{"wrong path", host(gitlabAt.URL + "/gitlab"), token, "No GitLab API answers at " + gitlabAt.URL + "/gitlab/api/v4: check the kind and the web address."},
		{"redirect", host(moved.URL), token, "The server redirects to https://gitlab.example.com/api/v4/version, which is not followed: check the web address."},
		{"timeout", host(stuck.URL), token, "The server did not answer in time."},
		{"TLS", host(tls.URL), token, "The server's certificate is signed by an unknown authority, an internal CA needs its CA bundle."},
	}
	for _, c := range cases {
		if _, err := test(t, c.host, c.tok); err == nil || err.Error() != c.says {
			t.Errorf("%s: %v, want %q", c.name, err, c.says)
		}
	}
	trusted := host(tls.URL)
	trusted.CABundle = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tls.Certificate().Raw}))
	if conn, err := test(t, trusted, token); err != nil || conn.Account != "root" {
		t.Fatalf("with the CA bundle: %+v, %v", conn, err)
	}
}

// A server that answers the version but no user is no GitLab either.
func TestAVersionWithoutAUserIsNoGitLab(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/version") {
			fmt.Fprintf(w, versionAnswer, "17.5.1")
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	if _, err := test(t, host(srv.URL), token); err == nil || !strings.HasPrefix(err.Error(), "No GitLab API answers at") {
		t.Fatalf("got %v", err)
	}
}

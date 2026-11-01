package github

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
)

const token = "github_pat_11AAAAAAA0abcdefghijklmnopqrstuvwxyz"

// The answer of GET /user as GitHub sends it, trimmed to a few of its fields.
const userAnswer = `{"login":"octocat","id":1,"node_id":"MDQ6VXNlcjE=","avatar_url":"https://github.example.com/images/error/octocat_happy.gif","gravatar_id":"","url":"https://github.example.com/api/v3/users/octocat","html_url":"https://github.example.com/octocat","type":"User","site_admin":false,"name":"monalisa octocat","company":"GitHub","public_repos":2,"created_at":"2008-01-14T04:33:35Z"}`

// fakeEnterprise answers the API below /api/v3 like a GitHub Enterprise
// Server does for the token, refusing every other one.
func fakeEnterprise(version string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if version != "" {
			w.Header().Set("X-GitHub-Enterprise-Version", version)
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Bad credentials","documentation_url":"https://docs.github.com/enterprise-server@3.14/rest","status":"401"}`)
			return
		}
		if r.URL.Path != "/api/v3/user" || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}`)
			return
		}
		fmt.Fprint(w, userAnswer)
	})
}

func test(t *testing.T, h forge.Host, tok string) (forge.Connection, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := Provider{}.Test(ctx, forge.NewClient(), h, tok)
	if err != nil && strings.Contains(err.Error(), tok) {
		t.Fatalf("the error names the token: %v", err)
	}
	return conn, err
}

func host(webURL string) forge.Host {
	return forge.Host{ID: "h", Kind: Kind, WebURL: webURL}
}

func TestGitHubComHasItsAPIOnAHostOfItsOwn(t *testing.T) {
	if got := api(forge.NewClient(), Builtin(), token).Base(); got != "https://api.github.com" {
		t.Fatalf("github.com reads %s", got)
	}
	if got := api(forge.NewClient(), host("https://github.example.com"), token).Base(); got != "https://github.example.com/api/v3" {
		t.Fatalf("an Enterprise Server reads %s", got)
	}
}

// The token page is the classic one, on github.com and an Enterprise Server
// alike, with the description and the scopes in its address.
func TestTheTokenPageIsTheClassicOne(t *testing.T) {
	for webURL, want := range map[string]string{
		"https://github.com":         "https://github.com/settings/tokens/new?description=dev+cockpit&scopes=repo,workflow,read:org",
		"https://github.example.com": "https://github.example.com/settings/tokens/new?description=dev+cockpit&scopes=repo,workflow,read:org",
	} {
		if got := (Provider{}).TokenPage(host(webURL), "dev cockpit"); got != want {
			t.Fatalf("%s reads %s", webURL, got)
		}
	}
}

func TestAnEnterpriseServerShowsTheUserAndItsVersion(t *testing.T) {
	srv := httptest.NewServer(fakeEnterprise("3.14.2"))
	defer srv.Close()
	conn, err := test(t, host(srv.URL), token)
	if err != nil || conn != (forge.Connection{Account: "octocat", Name: "monalisa octocat", Server: "GitHub Enterprise Server 3.14.2"}) {
		t.Fatalf("got %+v, %v", conn, err)
	}
	quiet := httptest.NewServer(fakeEnterprise(""))
	defer quiet.Close()
	if conn, err := test(t, host(quiet.URL), token); err != nil || conn.Server != "GitHub Enterprise Server" {
		t.Fatalf("without a version: %+v, %v", conn, err)
	}
}

func TestFailuresAreSaidPlainly(t *testing.T) {
	ghe := httptest.NewServer(fakeEnterprise("3.14.2"))
	defer ghe.Close()
	gitlabAt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "<!DOCTYPE html><title>Not Found</title>")
	}))
	defer gitlabAt.Close()
	gitlabAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":1,"username":"root","name":"Administrator"}`)
	}))
	defer gitlabAPI.Close()
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(20*time.Minute).Unix()))
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded for user ID 1.","documentation_url":"https://docs.github.com/rest/overview/rate-limits-for-the-rest-api","status":"403"}`)
	}))
	defer limited.Close()
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login?return_to=%2Fapi%2Fv3%2Fuser", http.StatusFound)
	}))
	defer moved.Close()
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer stuck.Close()
	tls := httptest.NewTLSServer(fakeEnterprise("3.14.2"))
	defer tls.Close()
	cases := []struct {
		name string
		host forge.Host
		tok  string
		says string
	}{
		{"bad token", host(ghe.URL), "ghp_wrongwrongwrongwrongwrong", "GitHub refused the token: it is wrong, expired or revoked."},
		{"wrong kind, a GitLab", host(gitlabAt.URL), token, "No GitHub API answers at " + gitlabAt.URL + "/api/v3: check the kind and the web address."},
		{"wrong kind, another API", host(gitlabAPI.URL), token, "No GitHub API answers at " + gitlabAPI.URL + "/api/v3: check the kind and the web address."},
		{"rate limit", host(limited.URL), token, "The server limits the requests, try again in 20 minutes."},
		{"redirect", host(moved.URL), token, "The server redirects to " + moved.URL + "/login, which is not followed: check the web address."},
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
	if conn, err := test(t, trusted, token); err != nil || conn.Account != "octocat" {
		t.Fatalf("with the CA bundle: %+v, %v", conn, err)
	}
}

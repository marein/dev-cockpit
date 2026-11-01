package forge_test

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

const token = "glpat-Xq3vR8sT2uW5yZ7aB9cD"

func certificate(t *testing.T) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
}

// A token lives in a file of its own, 0600 in a directory only the owner
// enters, never beside the hosts, and comes back as its last characters
// alone.
func TestATokenIsStoredApartAndNeverShownBack(t *testing.T) {
	s, hostsPath := service(t)
	dir := filepath.Dir(hostsPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	added := addedWithToken(t, s, forge.Input{Kind: "gitlab", WebURL: "https://git.example.com"}, "  "+token+"\n")
	tokensPath := filepath.Join(dir, "tokens.json")
	for path, mode := range map[string]os.FileMode{dir: 0o700, tokensPath: 0o600, hostsPath: 0o600} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: %v, %v, want %o", path, info, err, mode)
		}
	}
	if data, _ := os.ReadFile(hostsPath); strings.Contains(string(data), token) {
		t.Fatalf("the hosts file holds the token: %s", data)
	}
	if data, _ := os.ReadFile(tokensPath); !strings.Contains(string(data), `"`+added.ID+`": "`+token+`"`) {
		t.Fatalf("the tokens file: %s", data)
	}
	if err := s.ChangeToken("github.com", "ghp_short", false); err != nil {
		t.Fatal(err)
	}
	hints, err := s.TokenHints()
	if err != nil || len(hints) != 2 || hints[added.ID] != "B9cD" || hints["github.com"] != "" {
		t.Fatalf("hints %v, %v", hints, err)
	}
	if _, ok := hints["gitlab.com"]; ok {
		t.Fatal("gitlab.com shows a token it has not")
	}
	if _, err := s.Change(added.ID, forge.Input{Kind: "gitlab", WebURL: "https://git2.example.com"}); err != nil {
		t.Fatal(err)
	}
	if hints, _ := s.TokenHints(); hints[added.ID] != "B9cD" {
		t.Fatalf("a change of the host dropped the token: %v", hints)
	}
	if err := s.ChangeToken(added.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if hints, _ := s.TokenHints(); len(hints) != 1 {
		t.Fatalf("the removed token stays: %v", hints)
	}
	if err := s.ChangeToken(added.ID, token, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(added.ID); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(tokensPath); strings.Contains(string(data), token) {
		t.Fatalf("the deleted host's token stays: %s", data)
	}
}

func TestTheServerRefusesWhatNoTokenCanBe(t *testing.T) {
	s, _ := service(t)
	cases := []struct {
		token  string
		remove bool
		says   string
	}{
		{"glpat abc", false, "A token is one line of letters, digits and signs, without spaces."},
		{"glpat\nabc", false, "A token is one line of letters, digits and signs, without spaces."},
		{"glpät", false, "A token is one line of letters, digits and signs, without spaces."},
		{strings.Repeat("a", forge.MaxTokenLength+1), false, "A token has at most 1024 characters."},
		{token, true, "Type a new token or remove the one there is, not both."},
	}
	for _, c := range cases {
		if err := s.ChangeToken("gitlab.com", c.token, c.remove); err == nil || err.Error() != c.says {
			t.Errorf("ChangeToken(%q, %v) = %v, want %q", c.token, c.remove, err, c.says)
		}
	}
	if hints, _ := s.TokenHints(); len(hints) != 0 {
		t.Fatalf("a refused token was stored: %v", hints)
	}
	if err := s.ChangeToken("nope", token, false); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("a token for no host: %v", err)
	}
	if err := s.ChangeToken("gitlab.com", strings.Repeat("a", forge.MaxTokenLength), false); err != nil {
		t.Fatalf("the longest token: %v", err)
	}
}

func TestACABundleHoldsCertificatesForAnHTTPSServer(t *testing.T) {
	s, _ := service(t)
	cert := certificate(t)
	key := "-----BEGIN PRIVATE KEY-----\nMIIBVQ==\n-----END PRIVATE KEY-----\n"
	broken := "-----BEGIN CERTIFICATE-----\nMIIBVQ==\n-----END CERTIFICATE-----\n"
	cases := []struct {
		url, bundle, says string
	}{
		{"http://git.example.com", cert, "A CA bundle only serves a web address that starts with https://."},
		{"https://git.example.com", "just text", "A CA bundle is one or more PEM certificates, each from -----BEGIN CERTIFICATE----- to -----END CERTIFICATE-----."},
		{"https://git.example.com", cert + key, "A CA bundle holds certificates only, not a PRIVATE KEY."},
		{"https://git.example.com", cert + broken, "Certificate 2 of the CA bundle does not parse."},
		{"https://git.example.com", strings.Repeat(cert, 33), "A CA bundle holds at most 32 certificates."},
		{"https://git.example.com", cert + strings.Repeat("#", forge.MaxCABundleBytes), "A CA bundle has at most 64 KiB."},
	}
	for _, c := range cases {
		if _, err := s.Add(forge.Input{Kind: "gitlab", WebURL: c.url, CABundle: c.bundle}); err == nil || err.Error() != c.says {
			t.Errorf("%s with %.40q: %v, want %q", c.url, c.bundle, err, c.says)
		}
	}
	added, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com", CABundle: "# the internal CA\n" + cert + "\n\n"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Host(added.ID); got.CABundle != "# the internal CA\n"+strings.TrimSpace(cert) {
		t.Fatalf("stored %q", got.CABundle)
	}
	if _, err := s.Change(added.ID, forge.Input{Kind: "gitlab", WebURL: "http://git.example.com", CABundle: cert}); err == nil {
		t.Fatal("a change to http kept the CA bundle")
	}
}

type stubProvider struct {
	gitlab.Provider
	got  forge.Host
	tok  string
	conn forge.Connection
}

func (p *stubProvider) Test(_ context.Context, _ *forge.Client, h forge.Host, token string) (forge.Connection, error) {
	p.got, p.tok = h, token
	return p.conn, nil
}

// A test reaches the provider with the host and its token, and what the
// server said comes back cut to what a notice carries.
func TestATestAsksTheHostsProviderWithItsToken(t *testing.T) {
	stub := &stubProvider{conn: forge.Connection{Account: "root", Name: strings.Repeat("n", 500), Server: "GitLab 17.5.1"}}
	s := forge.New(t.TempDir(), []forge.Provider{github.Provider{}, stub}, github.Builtin(), gitlab.Builtin())
	if _, err := s.Test(context.Background(), "gitlab.com"); err == nil || err.Error() != "The host has no token to test." {
		t.Fatalf("a host without a token: %v", err)
	}
	if _, err := s.Test(context.Background(), "nope"); !errors.Is(err, forge.ErrNotFound) {
		t.Fatalf("no host: %v", err)
	}
	if err := s.ChangeToken("gitlab.com", token, false); err != nil {
		t.Fatal(err)
	}
	conn, err := s.Test(context.Background(), "gitlab.com")
	if err != nil || conn.Account != "root" || conn.Server != "GitLab 17.5.1" || len([]rune(conn.Name)) != 64 || !strings.HasSuffix(conn.Name, "…") {
		t.Fatalf("got %+v, %v", conn, err)
	}
	if stub.got.WebURL != "https://gitlab.com" || stub.tok != token {
		t.Fatalf("the provider got %+v with %q", stub.got, stub.tok)
	}
}

// The token page is the provider's, named after the cockpit, and none where
// the provider knows no page or no provider the kind.
func TestTheTokenPageIsTheProvidersNamedAfterTheCockpit(t *testing.T) {
	s := forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}, plainProvider{}}, github.Builtin(), gitlab.Builtin())
	if got := s.TokenPage(gitlab.Builtin()); got != "https://gitlab.com/-/user_settings/personal_access_tokens?name=dev-cockpit&scopes=api" {
		t.Fatalf("gitlab.com reads %s", got)
	}
	if got := s.TokenPage(github.Builtin()); got != "https://github.com/settings/tokens/new?description=dev-cockpit&scopes=repo,workflow,read:org" {
		t.Fatalf("github.com reads %s", got)
	}
	for _, kind := range []forge.Kind{plainProvider{}.Kind(), {ID: "gone"}} {
		if got := s.TokenPage(forge.Host{ID: "h", Kind: kind, WebURL: "https://git.example.com"}); got != "" {
			t.Fatalf("%s reads %s", kind.ID, got)
		}
	}
}

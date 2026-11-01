package forge_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

func service(t *testing.T) (*forge.Service, string) {
	t.Helper()
	dir := t.TempDir()
	return forge.New(dir, []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin()), filepath.Join(dir, "forge", "hosts.json")
}

// addedWithToken adds the host in and gives it token, the way the settings
// add one and then ask for its token.
func addedWithToken(t *testing.T, s *forge.Service, in forge.Input, token string) forge.Host {
	t.Helper()
	h, err := s.Add(in)
	if err == nil {
		err = s.ChangeToken(h.ID, token, false)
	}
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestAddedHostsResolveAfterTheBuiltInOnes(t *testing.T) {
	s, path := service(t)
	added, err := s.Add(forge.Input{Kind: "gitlab", WebURL: " https://git.example.com/ ", Aliases: []string{"gl", "", "ssh.git.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID == "" || added.Kind != gitlab.Kind || added.WebURL != "https://git.example.com" || strings.Join(added.Aliases, " ") != "gl ssh.git.example.com" {
		t.Fatalf("added %+v", added)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("hosts file %v, %v, want 0600", info, err)
	}
	hosts, err := s.Hosts()
	if err != nil || len(hosts) != 3 || !hosts[0].Builtin() || !hosts[1].Builtin() || hosts[2].ID != added.ID {
		t.Fatalf("hosts %+v, %v, want github.com, gitlab.com and the added one", hosts, err)
	}
	if b, ok := hosts.Resolve("gl:team/app.git"); !ok || b.Host.ID != added.ID || b.WebURL() != "https://git.example.com/team/app" {
		t.Fatalf("the alias bound %+v, %v", b, ok)
	}
	if got, err := s.Host(added.ID); err != nil || got.WebURL != added.WebURL {
		t.Fatalf("Host(%s) = %+v, %v", added.ID, got, err)
	}
}

func TestChangeAndDeleteAnAddedHost(t *testing.T) {
	s, _ := service(t)
	added, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com", Aliases: []string{"gl"}})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := s.Change(added.ID, forge.Input{Kind: "github", WebURL: "https://github.example.com", Aliases: []string{"gl", "ghe"}})
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID != added.ID || changed.Kind != github.Kind || changed.WebURL != "https://github.example.com" {
		t.Fatalf("changed %+v", changed)
	}
	hosts, _ := s.Hosts()
	if b, ok := hosts.Resolve("git@git.example.com:team/app.git"); ok {
		t.Fatalf("the old web address still binds %+v", b)
	}
	if b, ok := hosts.Resolve("ghe:team/app.git"); !ok || b.Host.Kind != github.Kind {
		t.Fatalf("the new alias bound %+v, %v", b, ok)
	}
	if err := s.Delete(added.ID); err != nil {
		t.Fatal(err)
	}
	if hosts, _ := s.Hosts(); len(hosts) != 2 {
		t.Fatalf("after the delete %+v", hosts)
	}
	for _, err := range []error{
		s.Delete(added.ID),
		func() error {
			_, err := s.Change(added.ID, forge.Input{Kind: "gitlab", WebURL: "https://x.example.com"})
			return err
		}(),
		func() error { _, err := s.Host(added.ID); return err }(),
		func() error { _, err := s.Host(""); return err }(),
	} {
		if !errors.Is(err, forge.ErrNotFound) {
			t.Errorf("a gone host answered %v", err)
		}
	}
}

func TestAHostKeepsItsOwnNamesOnChange(t *testing.T) {
	s, _ := service(t)
	added, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com", Aliases: []string{"gl"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Change(added.ID, forge.Input{Kind: "gitlab", WebURL: "https://git.example.com/gitlab", Aliases: []string{"gl"}}); err != nil {
		t.Fatalf("a host refused its own names: %v", err)
	}
}

func TestTheServerRefusesWhatNoHostCanBe(t *testing.T) {
	s, path := service(t)
	if _, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com", Aliases: []string{"gl"}}); err != nil {
		t.Fatal(err)
	}
	long := "a" + strings.Repeat("b", 300)
	tooMany := strings.Split(strings.Repeat("x,", forge.MaxAliases+1), ",")[:forge.MaxAliases+1]
	for i := range tooMany {
		tooMany[i] += strings.Repeat("y", i)
	}
	cases := []struct {
		kind, url string
		aliases   []string
		says      string
	}{
		{"gitea", "https://gitea.example.com", nil, "Pick the kind of forge."},
		{"", "https://x.example.com", nil, "Pick the kind of forge."},
		{"gitlab", "  ", nil, "A host needs its web address."},
		{"gitlab", "https://" + strings.Repeat("a", forge.MaxWebURLLength), nil, "A web address has at most 512 characters."},
		{"gitlab", "https://git example.com", nil, "A web address is one line without spaces."},
		{"gitlab", "https://x.example.com/\nfoo", nil, "A web address is one line without spaces."},
		{"gitlab", "git.example.com", nil, "A web address starts with https:// or http:// and names a server, like https://git.example.com."},
		{"gitlab", "ssh://git.example.com", nil, "A web address starts with https:// or http:// and names a server, like https://git.example.com."},
		{"gitlab", "javascript:alert(1)", nil, "A web address starts with https:// or http:// and names a server, like https://git.example.com."},
		{"gitlab", "https:///gitlab", nil, "A web address starts with https:// or http:// and names a server, like https://git.example.com."},
		{"gitlab", "https://x.example.com:port", nil, "A web address starts with https:// or http:// and names a server, like https://git.example.com."},
		{"gitlab", "https://user:pw@x.example.com", nil, "A web address carries no user name or password."},
		{"gitlab", "https://x.example.com/?a=1", nil, "A web address carries no query and no fragment."},
		{"gitlab", "https://x.example.com/#top", nil, "A web address carries no query and no fragment."},
		{"gitlab", "https://x.example.com/#", nil, "A web address carries no query and no fragment."},
		{"gitlab", "https://x.example.com", tooMany, "A host has at most 10 alias hosts."},
		{"gitlab", "https://x.example.com", []string{"a:b"}, `"a:b" is no host name.`},
		{"gitlab", "https://x.example.com", []string{"git@x"}, `"git@x" is no host name.`},
		{"gitlab", "https://x.example.com", []string{"x/y"}, `"x/y" is no host name.`},
		{"gitlab", "https://x.example.com", []string{"a\x07b"}, `"a\ab" is no host name.`},
		{"gitlab", "https://x.example.com", []string{long}, `"` + long[:63] + `…" is no host name.`},
		{"gitlab", "https://x.example.com", []string{"x.example.com"}, "x.example.com is named twice."},
		{"gitlab", "https://x.example.com", []string{"y", "Y"}, "Y is named twice."},
		{"gitlab", "https://github.com", nil, "github.com already belongs to https://github.com."},
		{"github", "https://x.example.com", []string{"ssh.github.com"}, "ssh.github.com already belongs to https://github.com."},
		{"gitlab", "https://GIT.example.com:8443/other", nil, "GIT.example.com already belongs to https://git.example.com."},
		{"gitlab", "https://x.example.com", []string{"GL"}, "GL already belongs to https://git.example.com."},
	}
	for _, c := range cases {
		if _, err := s.Add(forge.Input{Kind: c.kind, WebURL: c.url, Aliases: c.aliases}); err == nil || err.Error() != c.says {
			t.Errorf("Add(%q, %q, %q) = %v, want %q", c.kind, c.url, c.aliases, err, c.says)
		}
	}
	if hosts, _ := s.Hosts(); len(hosts) != 3 {
		t.Fatalf("a refusal changed the hosts: %+v", hosts)
	}
	for _, ok := range []struct {
		url     string
		aliases []string
	}{
		{"http://192.168.1.20:8080", []string{"10.0.0.2", "::1", "gitlab-work", "my_server.lan"}},
		{"https://[fd00::1]/gitlab", nil},
	} {
		if _, err := s.Add(forge.Input{Kind: "gitlab", WebURL: ok.url, Aliases: ok.aliases}); err != nil {
			t.Errorf("Add(%q, %q) refused: %v", ok.url, ok.aliases, err)
		}
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "x.example.com") {
		t.Fatalf("a refused host was stored: %s", data)
	}
}

func TestAtMostMaxHostsAreAdded(t *testing.T) {
	s, _ := service(t)
	for i := range forge.MaxHosts {
		if _, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://git" + strings.Repeat("x", i) + ".example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://one-more.example.com"}); err == nil || err.Error() != "At most 50 hosts can be added." {
		t.Fatalf("host %d: %v", forge.MaxHosts+1, err)
	}
}

// A file that is there but cannot be read keeps the built in hosts and
// refuses every write, so nothing it holds is written over.
func TestAnUnreadableFileRefusesEveryWrite(t *testing.T) {
	s, path := service(t)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	hosts, err := s.Hosts()
	if err == nil || len(hosts) != 2 {
		t.Fatalf("hosts %+v, %v, want the built in ones and the failure", hosts, err)
	}
	if _, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com"}); err == nil || err.Error() != "The hosts could not be read, nothing was changed." {
		t.Fatalf("Add over an unreadable file: %v", err)
	}
	if err := s.Delete("x"); err == nil || err.Error() != "The hosts could not be read, nothing was changed." {
		t.Fatalf("Delete over an unreadable file: %v", err)
	}
}

// A host of a kind no provider declares stays in the file, which another
// build may read, but binds nothing here.
func TestAHostOfAnUnknownKindStaysStored(t *testing.T) {
	s, path := service(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hosts":[{"id":"old","kind":"gitea","webURL":"https://gitea.example.com"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if hosts, _ := s.Hosts(); len(hosts) != 2 {
		t.Fatalf("the unknown kind shows: %+v", hosts)
	}
	if _, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com"}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "gitea.example.com") {
		t.Fatalf("a write dropped the unknown host: %s", data)
	}
}

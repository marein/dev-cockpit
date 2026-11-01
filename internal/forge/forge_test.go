package forge_test

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

func builtins() forge.Hosts {
	return forge.Hosts{github.Builtin(), gitlab.Builtin()}
}

func TestResolveBindsEveryOriginForm(t *testing.T) {
	cases := []struct {
		origin, kind, path, web string
	}{
		{"git@github.com:marein/dev-cockpit.git", "github", "marein/dev-cockpit", "https://github.com/marein/dev-cockpit"},
		{"git@github.com:marein/dev-cockpit", "github", "marein/dev-cockpit", "https://github.com/marein/dev-cockpit"},
		{"https://github.com/marein/dev-cockpit.git", "github", "marein/dev-cockpit", "https://github.com/marein/dev-cockpit"},
		{"https://marein@github.com/marein/dev-cockpit", "github", "marein/dev-cockpit", "https://github.com/marein/dev-cockpit"},
		{"ssh://git@github.com/marein/dev-cockpit.git", "github", "marein/dev-cockpit", "https://github.com/marein/dev-cockpit"},
		{"ssh://git@ssh.github.com:443/marein/dev-cockpit.git", "github", "marein/dev-cockpit", "https://github.com/marein/dev-cockpit"},
		{"git@GitHub.com:Marein/Dev-Cockpit.git", "github", "Marein/Dev-Cockpit", "https://github.com/Marein/Dev-Cockpit"},
		{"git@gitlab.com:group/repo.git", "gitlab", "group/repo", "https://gitlab.com/group/repo"},
		{"git@gitlab.com:group/sub/repo.git", "gitlab", "group/sub/repo", "https://gitlab.com/group/sub/repo"},
		{"git@gitlab.com:group/a/b/c/d/repo", "gitlab", "group/a/b/c/d/repo", "https://gitlab.com/group/a/b/c/d/repo"},
		{"ssh://git@gitlab.com:2222/group/repo.git", "gitlab", "group/repo", "https://gitlab.com/group/repo"},
		{"ssh://git@altssh.gitlab.com:443/group/sub/repo.git", "gitlab", "group/sub/repo", "https://gitlab.com/group/sub/repo"},
		{"https://user@gitlab.com:8443/group/sub/repo.git", "gitlab", "group/sub/repo", "https://gitlab.com/group/sub/repo"},
		{"https://gitlab.com/group/sub/repo", "gitlab", "group/sub/repo", "https://gitlab.com/group/sub/repo"},
		{"https://gitlab.com/group/sub/repo.git/", "gitlab", "group/sub/repo", "https://gitlab.com/group/sub/repo"},
	}
	s := builtins()
	for _, c := range cases {
		b, ok := s.Resolve(c.origin)
		if !ok {
			t.Errorf("Resolve(%q) bound nothing", c.origin)
			continue
		}
		if b.Host.Kind.ID != c.kind || b.Path != c.path || b.WebURL() != c.web {
			t.Errorf("Resolve(%q) = %s %q %q, want %s %q %q", c.origin, b.Host.Kind.ID, b.Path, b.WebURL(), c.kind, c.path, c.web)
		}
	}
}

func TestResolveBindsNothingElsewhere(t *testing.T) {
	s := builtins()
	for _, origin := range []string{
		"",
		"git@gitlab.example.com:group/repo.git",
		"https://github.example.com/marein/x.git",
		"git@bitbucket.org:team/repo.git",
		"https://notgithub.com/marein/x",
		"https://github.com.example.com/marein/x",
		"git@github.com:repo.git",
		"https://github.com/",
		"/srv/git/repo.git",
		"file:///srv/git/github.com/marein/x.git",
		"../github.com/marein/x",
	} {
		if b, ok := s.Resolve(origin); ok {
			t.Errorf("Resolve(%q) bound %s %q, want nothing", origin, b.Host.Kind.ID, b.Path)
		}
	}
}

func TestResolveAnswersTheHostsItKnows(t *testing.T) {
	self := forge.Host{ID: "self", Kind: gitlab.Kind, WebURL: "https://git.example.com", Aliases: []string{"gl"}}
	hosts := forge.Hosts{github.Builtin(), self}
	for _, origin := range []string{"git@git.example.com:team/app.git", "gl:team/app.git", "ssh://git@GL:2222/team/app"} {
		b, ok := hosts.Resolve(origin)
		if !ok || b.Host.WebURL != self.WebURL || b.Path != "team/app" || b.WebURL() != "https://git.example.com/team/app" {
			t.Errorf("Resolve(%q) = %+v, %v, want team/app on %s", origin, b, ok, self.WebURL)
		}
	}
	if _, ok := (forge.Hosts{}).Resolve("git@github.com:marein/x.git"); ok {
		t.Error("no hosts bound an origin")
	}
}

// A server under a path of its own puts that path in front of every http
// remote and in front of the repository's page, never into the repository
// path, and SSH never carries it.
func TestResolveKeepsTheWebPathOutOfTheRepository(t *testing.T) {
	self := forge.Host{ID: "self", Kind: gitlab.Kind, WebURL: "https://git.example.com/gitlab/"}
	hosts := forge.Hosts{self}
	for _, origin := range []string{
		"https://git.example.com/gitlab/team/app.git",
		"https://user@git.example.com:8443/gitlab/team/app",
		"git@git.example.com:team/app.git",
		"ssh://git@git.example.com:2222/team/app.git",
	} {
		b, ok := hosts.Resolve(origin)
		if !ok || b.Path != "team/app" || b.WebURL() != "https://git.example.com/gitlab/team/app" {
			t.Errorf("Resolve(%q) = %q %q, %v, want team/app at https://git.example.com/gitlab/team/app", origin, b.Path, b.WebURL(), ok)
		}
	}
	if b, ok := hosts.Resolve("https://git.example.com/gitlab/app.git"); ok {
		t.Errorf("a repository without namespace under the web path bound %q", b.Path)
	}
}

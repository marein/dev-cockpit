package forge_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

func bind(t *testing.T, s *forge.Service, project, origin string) (forge.Binding, bool) {
	t.Helper()
	b, err := s.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	return b.Bind(project, origin)
}

// An override binds where its origin cannot tell, wins over an origin that
// can, and Off binds nothing at all. Back to the origin takes it away.
func TestAnOverrideWinsOverTheOrigin(t *testing.T) {
	s, hosts := service(t)
	file := filepath.Join(filepath.Dir(hosts), "bindings.json")
	if b, ok := bind(t, s, "app", "git@github.com:marein/app.git"); !ok || b.Host.ID != "github.com" || b.Path != "marein/app" {
		t.Fatalf("the origin bound %+v, %v", b, ok)
	}
	if err := s.SetOverride("app", forge.Override{How: forge.ByHand, Host: "gitlab.com", Path: " /group/sub/app.git/ "}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("bindings file %v, %v, want 0600", info, err)
	}
	for _, origin := range []string{"git@github.com:marein/app.git", "/srv/git/app.git", ""} {
		b, ok := bind(t, s, "app", origin)
		if !ok || b.Host.Kind != gitlab.Kind || b.Path != "group/sub/app" || b.WebURL() != "https://gitlab.com/group/sub/app" || b.Address() != "gitlab.com/group/sub/app" {
			t.Fatalf("by hand with origin %q bound %+v, %v", origin, b, ok)
		}
	}
	if b, ok := bind(t, s, "other", "git@github.com:marein/other.git"); !ok || b.Host.Kind != github.Kind {
		t.Fatalf("another project lost its origin binding: %+v, %v", b, ok)
	}
	if err := s.SetOverride("app", forge.Override{How: forge.Off, Host: "gitlab.com", Path: "x/y"}); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Bindings()
	if o := all.Override("app"); o != (forge.Override{How: forge.Off}) {
		t.Fatalf("off reads %+v", o)
	}
	if b, ok := all.Bind("app", "git@github.com:marein/app.git"); ok {
		t.Fatalf("off bound %+v", b)
	}
	if err := s.SetOverride("app", forge.Override{How: forge.ByOrigin}); err != nil {
		t.Fatal(err)
	}
	if b, ok := bind(t, s, "app", "git@github.com:marein/app.git"); !ok || b.Host.Kind != github.Kind {
		t.Fatalf("back to the origin bound %+v, %v", b, ok)
	}
	if raw, _ := os.ReadFile(file); strings.Contains(string(raw), "app") {
		t.Fatalf("the file still holds the project: %s", raw)
	}
}

// The server reads every override before it stores one: how, a host there
// is and a path of a repository. A refusal stores nothing.
func TestAnOverrideTheServerRefusesIsNotStored(t *testing.T) {
	s, _ := service(t)
	for _, c := range []struct {
		o    forge.Override
		want string
	}{
		{forge.Override{How: "mirror"}, "Pick how the project binds."},
		{forge.Override{}, "Pick how the project binds."},
		{forge.Override{How: forge.ByHand, Path: "group/app"}, "Pick the host the repository lives on."},
		{forge.Override{How: forge.ByHand, Host: "nowhere", Path: "group/app"}, "That host no longer exists."},
		{forge.Override{How: forge.ByHand, Host: "github.com", Path: " / "}, "A binding by hand needs the repository path, like group/app."},
		{forge.Override{How: forge.ByHand, Host: "github.com", Path: "app"}, `"app" is no repository path, like group/app.`},
		{forge.Override{How: forge.ByHand, Host: "github.com", Path: "group//app"}, `"group//app" is no repository path, like group/app.`},
		{forge.Override{How: forge.ByHand, Host: "github.com", Path: "group/../app"}, `"group/../app" is no repository path, like group/app.`},
		{forge.Override{How: forge.ByHand, Host: "github.com", Path: "group/my app"}, `"group/my app" is no repository path, like group/sub/app.`},
		{forge.Override{How: forge.ByHand, Host: "github.com", Path: "group/app?x=1"}, `"group/app?x=1" is no repository path, like group/sub/app.`},
		{forge.Override{How: forge.ByHand, Host: "github.com", Path: "https://github.com/marein/app"}, "Type the repository path alone, like group/app."},
		{forge.Override{How: forge.ByHand, Host: "github.com", Path: "g/" + strings.Repeat("ä", forge.MaxPathLength)}, "A repository path has at most 512 characters."},
	} {
		if err := s.SetOverride("app", c.o); err == nil || err.Error() != c.want {
			t.Errorf("SetOverride(%+v) = %v, want %q", c.o, err, c.want)
		}
	}
	if b, ok := bind(t, s, "app", ""); ok {
		t.Fatalf("a refusal stored %+v", b)
	}
	long := "group/" + strings.Repeat("a", 80) + "/app"
	err := s.SetOverride("app", forge.Override{How: forge.ByHand, Host: "github.com", Path: long + " x"})
	if err == nil || len([]rune(err.Error())) > 160 {
		t.Fatalf("a long refusal quotes the path uncut: %v", err)
	}
}

// An override on a host that is gone, or in a shape no save writes, leaves
// the project to its origin.
func TestAnOverrideOnAHostThatIsGoneLeavesTheOrigin(t *testing.T) {
	s, hosts := service(t)
	added, err := s.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride("app", forge.Override{How: forge.ByHand, Host: added.ID, Path: "team/app"}); err != nil {
		t.Fatal(err)
	}
	if b, ok := bind(t, s, "app", "git@github.com:marein/app.git"); !ok || b.Host.ID != added.ID || b.WebURL() != "https://git.example.com/team/app" {
		t.Fatalf("by hand on the added host bound %+v, %v", b, ok)
	}
	if err := s.Delete(added.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Bindings()
	if o := all.Override("app"); o.How != forge.ByOrigin {
		t.Fatalf("an override on a deleted host reads %+v", o)
	}
	if b, ok := all.Bind("app", "git@github.com:marein/app.git"); !ok || b.Host.Kind != github.Kind {
		t.Fatalf("an override on a deleted host bound %+v, %v", b, ok)
	}
	file := filepath.Join(filepath.Dir(hosts), "bindings.json")
	if err := os.WriteFile(file, []byte(`{"projects":{"app":{"how":"mirror","host":"github.com","path":"a/b"},"lab":{"how":"hand","host":"github.com","path":"nopath"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	all, _ = s.Bindings()
	for _, name := range []string{"app", "lab"} {
		if o := all.Override(name); o.How != forge.ByOrigin {
			t.Fatalf("a hand written override of %s reads %+v", name, o)
		}
	}
}

// A project that is gone takes its override with it, the others stay. A file
// the service cannot read stays as it is.
func TestForgetTakesAProjectsOverrideAway(t *testing.T) {
	s, hosts := service(t)
	for _, name := range []string{"app", "lab"} {
		if err := s.SetOverride(name, forge.Override{How: forge.Off}); err != nil {
			t.Fatal(err)
		}
	}
	s.Forget("app")
	s.Forget("gone")
	all, _ := s.Bindings()
	if all.Override("app").How != forge.ByOrigin || all.Override("lab").How != forge.Off {
		t.Fatalf("after forget: app %+v, lab %+v", all.Override("app"), all.Override("lab"))
	}
	file := filepath.Join(filepath.Dir(hosts), "bindings.json")
	if err := os.Rename(file, file+".away"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bindings(); err == nil || err.Error() != "The forge bindings could not be read." {
		t.Fatalf("an unreadable file read %v", err)
	}
	if err := s.SetOverride("lab", forge.Override{How: forge.ByOrigin}); err == nil || err.Error() != "The forge bindings could not be read, nothing was changed." {
		t.Fatalf("a save over a file that could not be read: %v", err)
	}
	s.Forget("lab")
	if err := os.Remove(file); err != nil {
		t.Fatalf("the unreadable file was written over: %v", err)
	}
	if err := os.Rename(file+".away", file); err != nil {
		t.Fatal(err)
	}
	all, _ = s.Bindings()
	if all.Override("lab").How != forge.Off {
		t.Fatal("an unreadable file lost its override")
	}
}

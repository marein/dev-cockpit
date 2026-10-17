package share

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s := NewStore(t.TempDir())
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, &now
}

func projects(names ...string) []Resource {
	out := make([]Resource, 0, len(names))
	for _, name := range names {
		out = append(out, Resource{Kind: KindProject, Target: name})
	}
	return out
}

func mustCreate(t *testing.T, s *Store, name string, lifetime time.Duration, targets ...string) (Share, string) {
	t.Helper()
	sh, token, err := s.Create(name, projects(targets...), lifetime)
	if err != nil {
		t.Fatal(err)
	}
	return sh, token
}

func TestCreateStoresTheHashAlone(t *testing.T) {
	s, _ := newTestStore(t)
	sh, token := mustCreate(t, s, "Anna", time.Hour, "demo")
	if len(sh.TokenHash) != 64 {
		t.Fatalf("hash %q", sh.TokenHash)
	}
	data, err := os.ReadFile(s.file(sh.TokenHash))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) || !strings.Contains(string(data), sh.TokenHash) {
		t.Fatalf("the file holds the token or lacks the hash: %s", data)
	}
	for path, want := range map[string]os.FileMode{s.file(sh.TokenHash): 0o600, s.dir: 0o700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: mode = %v, %v", path, info.Mode().Perm(), err)
		}
	}
}

// Every resource stands on disk with a permissions list of its own, empty for
// read only, so granting more later changes a value and adds no field.
func TestEachResourceStoresItsOwnPermissions(t *testing.T) {
	s, _ := newTestStore(t)
	sh, _ := mustCreate(t, s, "Anna", time.Hour, "shop", "blog")
	data, err := os.ReadFile(s.file(sh.TokenHash))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Name      string                       `json:"name"`
		Resources []map[string]json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Name != "Anna" || len(raw.Resources) != 2 {
		t.Fatalf("stored %s", data)
	}
	for _, r := range raw.Resources {
		if string(r["kind"]) != `"project"` || string(r["permissions"]) != "[]" {
			t.Fatalf("a resource reads %s", data)
		}
	}
}

func TestLookupOpensTheShareItsTokenMade(t *testing.T) {
	s, _ := newTestStore(t)
	first, firstToken := mustCreate(t, s, "Anna", time.Hour, "one")
	second, secondToken := mustCreate(t, s, "Anna", time.Hour, "two", "three")
	for token, want := range map[string]Share{firstToken: first, secondToken: second} {
		got, err := s.Lookup(token)
		if err != nil || got.ID != want.ID || !slices.Equal(got.Targets(KindProject), want.Targets(KindProject)) {
			t.Fatalf("Lookup = %+v, %v; want %+v", got, err, want)
		}
	}
}

func TestLookupAnswersTheSameForEveryTokenThatOpensNothing(t *testing.T) {
	s, now := newTestStore(t)
	revoked, revokedToken := mustCreate(t, s, "Anna", time.Hour, "a")
	_, expiredToken := mustCreate(t, s, "Anna", time.Minute, "b")
	_, deletedToken := mustCreate(t, s, "Anna", time.Hour, "gone")
	if err := s.Revoke(revoked.ID); err != nil {
		t.Fatal(err)
	}
	s.RemoveTarget(KindProject, "gone")
	*now = now.Add(2 * time.Minute)
	unknown, _, _ := newToken()
	for name, token := range map[string]string{
		"unknown":         unknown,
		"malformed":       "not a token",
		"short":           "abc",
		"empty":           "",
		"revoked":         revokedToken,
		"expired":         expiredToken,
		"deleted project": deletedToken,
	} {
		if _, err := s.Lookup(token); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestCreateRefusesWhatCannotBeShared(t *testing.T) {
	s, _ := newTestStore(t)
	cases := map[string]func() error{
		"nothing":    func() error { _, _, err := s.Create("Anna", nil, time.Hour); return err },
		"no project": func() error { _, _, err := s.Create("Anna", projects(" "), time.Hour); return err },
		"long project": func() error {
			_, _, err := s.Create("Anna", projects(strings.Repeat("x", 256)), time.Hour)
			return err
		},
		"twice": func() error { _, _, err := s.Create("Anna", projects("x", "x"), time.Hour); return err },
		"unknown kind": func() error {
			_, _, err := s.Create("Anna", []Resource{{Kind: "terminal", Target: "x"}}, time.Hour)
			return err
		},
		"an assistant granting more than reading": func() error {
			_, _, err := s.Create("Anna", []Resource{{Kind: KindAssistant, Target: "a1b2c3d4", Permissions: []Permission{Files}}}, time.Hour)
			return err
		},
		"unknown permission": func() error {
			_, _, err := s.Create("Anna", []Resource{{Kind: KindProject, Target: "x", Permissions: []Permission{Files, "delete-terminals"}}}, time.Hour)
			return err
		},
		"long name": func() error {
			_, _, err := s.Create(strings.Repeat("ä", MaxNameLength+1), projects("x"), time.Hour)
			return err
		},
		"no name":      func() error { _, _, err := s.Create("", projects("x"), time.Hour); return err },
		"blank name":   func() error { _, _, err := s.Create(" \t ", projects("x"), time.Hour); return err },
		"two lines":    func() error { _, _, err := s.Create("a\nb", projects("x"), time.Hour); return err },
		"broken utf-8": func() error { _, _, err := s.Create("a\xffb", projects("x"), time.Hour); return err },
		"too short":    func() error { _, _, err := s.Create("Anna", projects("x"), time.Second); return err },
		"too long":     func() error { _, _, err := s.Create("Anna", projects("x"), MaxLifetime+time.Second); return err },
	}
	for name, create := range cases {
		if err := create(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if len(s.List()) != 0 {
		t.Fatal("a refused share was stored")
	}
	if sh, _ := mustCreate(t, s, "  "+strings.Repeat("ä", MaxNameLength)+" ", time.Hour, "x"); sh.Name != strings.Repeat("ä", MaxNameLength) {
		t.Fatalf("name = %q", sh.Name)
	}
}

func TestCreateKeepsEachPermissionOnceInItsOrder(t *testing.T) {
	s, _ := newTestStore(t)
	sh, token, err := s.Create("Anna", []Resource{{Kind: KindProject, Target: "x", Permissions: []Permission{Compose, Files, Compose}}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(token)
	if err != nil {
		t.Fatal(err)
	}
	want := []Permission{Files, Compose}
	stored, _ := got.Resource(KindProject, "x")
	made, _ := sh.Resource(KindProject, "x")
	if !slices.Equal(stored.Permissions, want) || !slices.Equal(made.Permissions, want) {
		t.Fatalf("permissions = %v, want %v", stored.Permissions, want)
	}
}

// How many links stand is the user's business: nothing refuses one more.
func TestCreateHasNoCap(t *testing.T) {
	s, _ := newTestStore(t)
	const many = 300
	tokens := make([]string, 0, many)
	for i := 0; i < many; i++ {
		_, token, err := s.Create("Anna", projects("x"), time.Hour)
		if err != nil {
			t.Fatalf("link %d was refused: %v", i, err)
		}
		tokens = append(tokens, token)
	}
	if got := len(s.List()); got != many {
		t.Fatalf("%d links listed, want %d", got, many)
	}
	for _, token := range []string{tokens[0], tokens[many/2], tokens[many-1]} {
		if _, err := s.Lookup(token); err != nil {
			t.Fatalf("a lookup among %d links failed: %v", many, err)
		}
	}
}

// A lookup reads the one file its token's hash names and nothing else, so its
// cost does not grow with the links. The proof is a neighbour that statefile
// would quarantine if anybody read it: after the lookups it is untouched.
func TestALookupReadsOnlyTheFileItsTokenNames(t *testing.T) {
	s, _ := newTestStore(t)
	_, token := mustCreate(t, s, "Anna", time.Hour, "x")
	neighbour := s.file(strings.Repeat("a", 64))
	if err := os.WriteFile(neighbour, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	unknown, _, _ := newToken()
	for _, tok := range []string{token, unknown, "malformed"} {
		_, _ = s.Lookup(tok)
	}
	if _, err := s.Lookup(token); err != nil {
		t.Fatalf("the lookup failed: %v", err)
	}
	if _, err := os.Stat(neighbour); err != nil {
		t.Fatalf("a lookup read a file it does not name: %v", err)
	}
}

func TestAWritePrunesTheExpiredAndLeavesWhatItCannotRead(t *testing.T) {
	s, now := newTestStore(t)
	expired, _ := mustCreate(t, s, "Anna", time.Minute, "x")
	unreadable := s.file(strings.Repeat("b", 64))
	if err := os.MkdirAll(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	live, liveToken := mustCreate(t, s, "Anna", time.Hour, "x")
	if _, err := os.Stat(s.file(expired.TokenHash)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the expired link was kept: %v", err)
	}
	if info, err := os.Stat(unreadable); err != nil || !info.IsDir() {
		t.Fatal("a file the store could not read was removed")
	}
	if got, err := s.Lookup(liveToken); err != nil || got.ID != live.ID {
		t.Fatalf("Lookup = %+v, %v", got, err)
	}
}

func TestRevokeWakesTheWaiters(t *testing.T) {
	s, _ := newTestStore(t)
	sh, _ := mustCreate(t, s, "Anna", time.Hour, "x")
	if _, ok := s.Current(sh); !ok {
		t.Fatal("a fresh share does not stand")
	}
	changed := s.Changed()
	if err := s.Revoke(sh.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("a revoke woke nobody")
	}
	if _, ok := s.Current(sh); ok {
		t.Fatal("the revoked share still stands")
	}
	if err := s.Revoke(sh.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a second revoke = %v", err)
	}
}

// A standing link grows and shrinks under the same token in one save, every
// save wakes the waiters, what left or joined the link meanwhile is passed
// over, and a save that would leave it opening nothing changes nothing.
func TestApplyChangesALinkUnderItsToken(t *testing.T) {
	s, now := newTestStore(t)
	sh, token := mustCreate(t, s, "Anna", time.Hour, "shop")
	changed := s.Changed()
	told := 0
	s.OnChange(func() { told++ })
	if err := s.Apply(sh.ID, Change{Add: projects("docs", "blog")}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("a save woke nobody")
	}
	if told != 1 {
		t.Fatalf("a save told the callback %d times", told)
	}
	targets := func() []string {
		t.Helper()
		got, err := s.Lookup(token)
		if err != nil {
			t.Fatal(err)
		}
		return got.Targets(KindProject)
	}
	if got := targets(); !slices.Equal(got, []string{"shop", "docs", "blog"}) {
		t.Fatalf("the link opens %v", got)
	}
	if err := s.Apply(sh.ID, Change{Add: projects("blog"), Remove: projects("nothing")}); err != nil {
		t.Fatal(err)
	}
	if got := targets(); !slices.Equal(got, []string{"shop", "docs", "blog"}) {
		t.Fatalf("a project on the link already or off it moved it to %v", got)
	}
	for _, refused := range []Change{
		{Add: projects(" ")},
		{Add: projects("x", "x")},
		{Add: projects("x"), Lifetime: time.Second},
		{Remove: projects("shop", "docs", "blog")},
	} {
		if err := s.Apply(sh.ID, refused); err == nil {
			t.Fatalf("%+v went through", refused)
		}
	}
	if got := targets(); !slices.Equal(got, []string{"shop", "docs", "blog"}) {
		t.Fatalf("a refused save moved the link to %v", got)
	}
	if err := s.Apply(sh.ID, Change{Remove: projects("shop", "blog"), Add: projects("api")}); err != nil {
		t.Fatal(err)
	}
	if got := targets(); !slices.Equal(got, []string{"docs", "api"}) {
		t.Fatalf("the link opens %v", got)
	}
	*now = now.Add(2 * time.Hour)
	if err := s.Apply(sh.ID, Change{Add: projects("shop")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an ended link took a project: %v", err)
	}
}

// A save keeps the link's expiry unless it carries a lifetime, which counts
// from the save, and never touches its name.
func TestApplyRenewsAndKeepsTheName(t *testing.T) {
	s, now := newTestStore(t)
	sh, token := mustCreate(t, s, "Anna", time.Hour, "shop")
	if err := s.Apply(sh.ID, Change{Add: projects("blog")}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(token)
	if err != nil || got.Name != "Anna" || !got.ExpiresAt.Equal(sh.ExpiresAt) {
		t.Fatalf("after a save %+v, %v", got, err)
	}
	*now = now.Add(30 * time.Minute)
	if err := s.Apply(sh.ID, Change{Lifetime: 2 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Lookup(token)
	if err != nil || got.Name != "Anna" || !got.ExpiresAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("after a renewal %+v, %v", got, err)
	}
	for name, lifetime := range map[string]time.Duration{"short": time.Second, "long": MaxLifetime + time.Minute} {
		if err := s.Apply(sh.ID, Change{Lifetime: lifetime}); err == nil {
			t.Fatalf("a %s lifetime was taken", name)
		}
	}
	*now = now.Add(3 * time.Hour)
	if err := s.Apply(sh.ID, Change{Lifetime: time.Hour}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an expired link was renewed: %v", err)
	}
}

func TestRemoveTargetTakesOnlyThatProjectOffEveryLink(t *testing.T) {
	s, _ := newTestStore(t)
	alone, _ := mustCreate(t, s, "Anna", time.Hour, "gone")
	shared, _ := mustCreate(t, s, "Anna", time.Hour, "gone", "kept")
	other, _ := mustCreate(t, s, "Anna", time.Hour, "kept")
	changed := s.Changed()
	s.RemoveTarget(KindProject, "gone")
	select {
	case <-changed:
	default:
		t.Fatal("deleting a project's links woke nobody")
	}
	if _, ok := s.Current(alone); ok {
		t.Fatal("the link of the gone project alone stands")
	}
	if current, ok := s.Current(shared); !ok || !slices.Equal(current.Targets(KindProject), []string{"kept"}) {
		t.Fatalf("the shared link = %+v, %v", current, ok)
	}
	if current, ok := s.Current(other); !ok || len(current.Resources) != 1 {
		t.Fatalf("the other link = %+v, %v", current, ok)
	}
	if got := len(s.List()); got != 2 {
		t.Fatalf("%d links listed, want 2", got)
	}
}

// An assistant is a resource beside the projects of a link, named by its id,
// read only, and the link ends when the last of either kind goes.
func TestAnAssistantStandsOnALinkBesideItsProjects(t *testing.T) {
	s, _ := newTestStore(t)
	sh, token, err := s.Create("Anna", []Resource{{Kind: KindAssistant, Target: "a1b2c3d4"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(sh.ID, Change{Add: []Resource{{Kind: KindProject, Target: "shop"}, {Kind: KindAssistant, Target: "e5f6a7b8"}, {Kind: KindAssistant, Target: "a1b2c3d4"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(token)
	if err != nil || !slices.Equal(got.Targets(KindAssistant), []string{"a1b2c3d4", "e5f6a7b8"}) || !slices.Equal(got.Targets(KindProject), []string{"shop"}) {
		t.Fatalf("Lookup = %+v, %v", got, err)
	}
	if r, ok := got.Resource(KindAssistant, "a1b2c3d4"); !ok || r.Permissions == nil || len(r.Permissions) != 0 {
		t.Fatalf("the assistant is stored as %+v, %v", r, ok)
	}
	if _, ok := got.Resource(KindProject, "a1b2c3d4"); ok {
		t.Fatal("an assistant reads as a project of the same name")
	}
	if err := s.Apply(sh.ID, Change{Remove: projects("shop")}); err != nil {
		t.Fatal(err)
	}
	s.RemoveTarget(KindAssistant, "a1b2c3d4")
	if current, ok := s.Current(sh); !ok || !slices.Equal(current.Targets(KindAssistant), []string{"e5f6a7b8"}) {
		t.Fatalf("after the first assistant went: %+v, %v", current, ok)
	}
	s.RemoveTarget(KindAssistant, "e5f6a7b8")
	if _, ok := s.Current(sh); ok {
		t.Fatal("a link without anything left stands")
	}
	if _, err := os.Stat(s.file(sh.TokenHash)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the emptied link stays on disk: %v", err)
	}
}

// What a project of a standing link grants changes under the same token, a
// project put on grants what it was put on with, and nothing grants an
// assistant anything or a project what no permission names, which refuses the
// whole save.
func TestApplyChangesWhatAProjectGrants(t *testing.T) {
	s, now := newTestStore(t)
	sh, token := mustCreate(t, s, "Anna", time.Hour, "shop", "blog")
	if err := s.Apply(sh.ID, Change{Add: []Resource{{Kind: KindAssistant, Target: "a1b2c3d4"}, {Kind: KindProject, Target: "docs", Permissions: []Permission{Comments}}}}); err != nil {
		t.Fatal(err)
	}
	grants := func(target string) []Permission {
		t.Helper()
		got, err := s.Lookup(token)
		if err != nil {
			t.Fatal(err)
		}
		r, _ := got.Resource(KindProject, target)
		return r.Permissions
	}
	if got := grants("docs"); !slices.Equal(got, []Permission{Comments}) {
		t.Fatalf("a project put on grants %#v", got)
	}
	if err := s.Apply(sh.ID, Change{Add: []Resource{{Kind: KindProject, Target: "api"}}}); err != nil {
		t.Fatal(err)
	}
	if got := grants("api"); got == nil || len(got) != 0 {
		t.Fatalf("a project put on read only grants %#v", got)
	}
	for name, refused := range map[string]Change{
		"an assistant put on granting":                       {Add: []Resource{{Kind: KindAssistant, Target: "e5f6a7b8", Permissions: []Permission{Comments}}}},
		"a project put on granting what no permission names": {Add: []Resource{{Kind: KindProject, Target: "web", Permissions: []Permission{"root"}}}},
	} {
		if err := s.Apply(sh.ID, refused); err == nil {
			t.Fatalf("%s went through", name)
		}
	}
	comment := []Resource{{Kind: KindProject, Target: "shop", Permissions: []Permission{Comments, Comments}}, {Kind: KindProject, Target: "gone", Permissions: []Permission{Comments}}}
	if err := s.Apply(sh.ID, Change{Grants: comment}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(grants("shop"), []Permission{Comments}) || len(grants("blog")) != 0 {
		t.Fatalf("shop grants %v, blog %v", grants("shop"), grants("blog"))
	}
	for name, refused := range map[string]Change{
		"an assistant":     {Grants: []Resource{{Kind: KindAssistant, Target: "a1b2c3d4", Permissions: []Permission{Comments}}}},
		"an unknown grant": {Grants: []Resource{{Kind: KindProject, Target: "shop", Permissions: []Permission{"root"}}}},
		"a whole save":     {Grants: []Resource{{Kind: KindProject, Target: "shop"}, {Kind: KindProject, Target: "blog", Permissions: []Permission{"root"}}}},
	} {
		if err := s.Apply(sh.ID, refused); err == nil {
			t.Fatalf("granting %s went through", name)
		}
	}
	if !slices.Equal(grants("shop"), []Permission{Comments}) {
		t.Fatalf("a refused save left shop granting %v", grants("shop"))
	}
	if err := s.Apply(sh.ID, Change{Grants: []Resource{{Kind: KindProject, Target: "shop"}}}); err != nil {
		t.Fatal(err)
	}
	if got := grants("shop"); got == nil || len(got) != 0 {
		t.Fatalf("shop grants %#v after the take away", got)
	}
	*now = now.Add(2 * time.Hour)
	if err := s.Apply(sh.ID, Change{Grants: comment}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an expired link took a grant: %v", err)
	}
}

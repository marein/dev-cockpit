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
	sh, token := mustCreate(t, s, "", time.Hour, "demo")
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
	first, firstToken := mustCreate(t, s, "", time.Hour, "one")
	second, secondToken := mustCreate(t, s, "", time.Hour, "two", "three")
	for token, want := range map[string]Share{firstToken: first, secondToken: second} {
		got, err := s.Lookup(token)
		if err != nil || got.ID != want.ID || !slices.Equal(got.Targets(KindProject), want.Targets(KindProject)) {
			t.Fatalf("Lookup = %+v, %v; want %+v", got, err, want)
		}
	}
}

func TestLookupAnswersTheSameForEveryTokenThatOpensNothing(t *testing.T) {
	s, now := newTestStore(t)
	revoked, revokedToken := mustCreate(t, s, "", time.Hour, "a")
	_, expiredToken := mustCreate(t, s, "", time.Minute, "b")
	_, deletedToken := mustCreate(t, s, "", time.Hour, "gone")
	emptied, emptiedToken := mustCreate(t, s, "", time.Hour, "c")
	if err := s.Revoke(revoked.ID); err != nil {
		t.Fatal(err)
	}
	s.RemoveTarget(KindProject, "gone")
	if _, err := s.Remove(emptied.ID, KindProject, "c"); err != nil {
		t.Fatal(err)
	}
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
		"last removed":    emptiedToken,
	} {
		if _, err := s.Lookup(token); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestCreateRefusesWhatCannotBeShared(t *testing.T) {
	s, _ := newTestStore(t)
	cases := map[string]func() error{
		"nothing":    func() error { _, _, err := s.Create("", nil, time.Hour); return err },
		"no project": func() error { _, _, err := s.Create("", projects(" "), time.Hour); return err },
		"long project": func() error {
			_, _, err := s.Create("", projects(strings.Repeat("x", 256)), time.Hour)
			return err
		},
		"twice": func() error { _, _, err := s.Create("", projects("x", "x"), time.Hour); return err },
		"unknown kind": func() error {
			_, _, err := s.Create("", []Resource{{Kind: "assistant", Target: "x"}}, time.Hour)
			return err
		},
		"unknown permission": func() error {
			_, _, err := s.Create("", []Resource{{Kind: KindProject, Target: "x", Permissions: []Permission{Files, "delete-terminals"}}}, time.Hour)
			return err
		},
		"long name": func() error {
			_, _, err := s.Create(strings.Repeat("ä", MaxNameLength+1), projects("x"), time.Hour)
			return err
		},
		"two lines":    func() error { _, _, err := s.Create("a\nb", projects("x"), time.Hour); return err },
		"broken utf-8": func() error { _, _, err := s.Create("a\xffb", projects("x"), time.Hour); return err },
		"too short":    func() error { _, _, err := s.Create("", projects("x"), time.Second); return err },
		"too long":     func() error { _, _, err := s.Create("", projects("x"), MaxLifetime+time.Second); return err },
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
	sh, token, err := s.Create("", []Resource{{Kind: KindProject, Target: "x", Permissions: []Permission{Compose, Files, Compose}}}, time.Hour)
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
		_, token, err := s.Create("", projects("x"), time.Hour)
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
	_, token := mustCreate(t, s, "", time.Hour, "x")
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
	expired, _ := mustCreate(t, s, "", time.Minute, "x")
	unreadable := s.file(strings.Repeat("b", 64))
	if err := os.MkdirAll(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	live, liveToken := mustCreate(t, s, "", time.Hour, "x")
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
	sh, _ := mustCreate(t, s, "", time.Hour, "x")
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

// A standing link grows and shrinks under the same token, every change wakes
// the waiters, and the last resource taken off ends it on disk.
func TestALinkGainsAndLosesProjectsUnderItsToken(t *testing.T) {
	s, _ := newTestStore(t)
	sh, token := mustCreate(t, s, "", time.Hour, "shop")
	changed := s.Changed()
	if err := s.Add(sh.ID, Resource{Kind: KindProject, Target: "blog"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("an add woke nobody")
	}
	if err := s.Add(sh.ID, Resource{Kind: KindProject, Target: "blog"}); err == nil {
		t.Fatal("a project went on the link twice")
	}
	if err := s.Add(sh.ID, Resource{Kind: KindProject, Target: " "}); err == nil {
		t.Fatal("an empty target went on the link")
	}
	got, err := s.Lookup(token)
	if err != nil || !slices.Equal(got.Targets(KindProject), []string{"shop", "blog"}) {
		t.Fatalf("Lookup = %+v, %v", got, err)
	}
	if current, ok := s.Current(sh); !ok || len(current.Resources) != 2 {
		t.Fatalf("Current = %+v, %v", current, ok)
	}
	if _, err := s.Remove(sh.ID, KindProject, "nothing"); err == nil {
		t.Fatal("removing a project the link lacks went through")
	}
	if ended, err := s.Remove(sh.ID, KindProject, "shop"); err != nil || ended {
		t.Fatalf("Remove = %v, %v", ended, err)
	}
	if got, err := s.Lookup(token); err != nil || !slices.Equal(got.Targets(KindProject), []string{"blog"}) {
		t.Fatalf("Lookup = %+v, %v", got, err)
	}
	if ended, err := s.Remove(sh.ID, KindProject, "blog"); err != nil || !ended {
		t.Fatalf("Remove of the last = %v, %v", ended, err)
	}
	if _, err := os.Stat(s.file(sh.TokenHash)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the emptied link stays on disk: %v", err)
	}
	if err := s.Add(sh.ID, Resource{Kind: KindProject, Target: "shop"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an ended link took a project: %v", err)
	}
}

func TestUpdateRenamesAndRenewsWithoutANewToken(t *testing.T) {
	s, now := newTestStore(t)
	sh, token := mustCreate(t, s, "Anna", time.Hour, "shop")
	if err := s.Update(sh.ID, "Bert", 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(token)
	if err != nil || got.Name != "Bert" || !got.ExpiresAt.Equal(sh.ExpiresAt) {
		t.Fatalf("after a rename %+v, %v", got, err)
	}
	*now = now.Add(30 * time.Minute)
	if err := s.Update(sh.ID, "", 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err = s.Lookup(token)
	if err != nil || got.Name != "" || !got.ExpiresAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("after a renewal %+v, %v", got, err)
	}
	for name, lifetime := range map[string]time.Duration{"short": time.Second, "long": MaxLifetime + time.Minute} {
		if err := s.Update(sh.ID, "x", lifetime); err == nil {
			t.Fatalf("a %s lifetime was taken", name)
		}
	}
	if err := s.Update(sh.ID, strings.Repeat("x", MaxNameLength+1), 0); err == nil {
		t.Fatal("a long name was taken")
	}
	*now = now.Add(3 * time.Hour)
	if err := s.Update(sh.ID, "late", time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an expired link was renewed: %v", err)
	}
}

func TestRemoveTargetTakesOnlyThatProjectOffEveryLink(t *testing.T) {
	s, _ := newTestStore(t)
	alone, _ := mustCreate(t, s, "", time.Hour, "gone")
	shared, _ := mustCreate(t, s, "", time.Hour, "gone", "kept")
	other, _ := mustCreate(t, s, "", time.Hour, "kept")
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

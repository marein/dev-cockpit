package browserextension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateStoresOnlyTheHash(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	created, token, err := s.Create("  Laptop  ")
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Laptop" || created.ID == "" || !created.UsedAt.IsZero() {
		t.Fatalf("created %+v", created)
	}
	data, err := os.ReadFile(filepath.Join(dir, "browser-extension", "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("the token stands in the file")
	}
	info, err := os.Stat(filepath.Join(dir, "browser-extension", "tokens.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the file is %v, %v", info.Mode().Perm(), err)
	}
	got, err := s.Lookup(token)
	if err != nil || got.ID != created.ID || got.Name != "Laptop" {
		t.Fatalf("lookup %+v %v", got, err)
	}
}

func TestLookupRefusesWhatOpensNothing(t *testing.T) {
	s := NewStore(t.TempDir())
	_, token, err := s.Create("Laptop")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "x", token + "A", strings.ToUpper(token), strings.Repeat("A", 43)} {
		if _, err := s.Lookup(bad); err != ErrNotFound {
			t.Fatalf("lookup %q: %v", bad, err)
		}
	}
}

func TestRevokeEndsTheTokenAtOnce(t *testing.T) {
	s := NewStore(t.TempDir())
	created, token, err := s.Create("Laptop")
	if err != nil {
		t.Fatal(err)
	}
	other, otherToken, err := s.Create("Desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(token); err != ErrNotFound {
		t.Fatalf("a revoked token still opens: %v", err)
	}
	if got, err := s.Lookup(otherToken); err != nil || got.ID != other.ID {
		t.Fatalf("the other token: %+v %v", got, err)
	}
	if err := s.Revoke(created.ID); err != ErrNotFound {
		t.Fatalf("revoking twice: %v", err)
	}
}

func TestLastUseIsWrittenAtMostOnceAMinute(t *testing.T) {
	s := NewStore(t.TempDir())
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	_, token, err := s.Create("Laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !s.List()[0].UsedAt.IsZero() {
		t.Fatal("a new token reads as used")
	}
	if _, err := s.Lookup(token); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0].UsedAt; !got.Equal(now) {
		t.Fatalf("used at %v", got)
	}
	first := now
	now = now.Add(30 * time.Second)
	if _, err := s.Lookup(token); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0].UsedAt; !got.Equal(first) {
		t.Fatalf("written again within the minute: %v", got)
	}
	now = now.Add(31 * time.Second)
	if _, err := s.Lookup(token); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0].UsedAt; !got.Equal(now) {
		t.Fatalf("not written after the minute: %v", got)
	}
}

func TestCreateBoundsNameAndCount(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, bad := range []string{"", "   ", "two\nlines", "tab\there", strings.Repeat("x", MaxNameLength+1)} {
		if _, _, err := s.Create(bad); err == nil {
			t.Fatalf("the name %q was taken", bad)
		}
	}
	if _, _, err := s.Create(strings.Repeat("ü", MaxNameLength)); err != nil {
		t.Fatalf("a name of %d runes: %v", MaxNameLength, err)
	}
	if _, _, err := s.Create(strings.Repeat("Ü", MaxNameLength)); err == nil {
		t.Fatal("a name taken in another case was made again")
	}
	for i := len(s.List()); i < MaxTokens; i++ {
		if _, _, err := s.Create("Browser " + string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Create("One more"); err == nil || !strings.Contains(err.Error(), "Revoke one") {
		t.Fatalf("a token past the bound: %v", err)
	}
}

func TestAnUnreadableFileOpensNothing(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	_, token, err := s.Create("Laptop")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "browser-extension", "tokens.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(token); err != ErrNotFound {
		t.Fatalf("an unreadable file opened the token: %v", err)
	}
	if _, _, err := s.Create("Desktop"); err == nil {
		t.Fatal("a create over an unreadable file went through")
	}
}

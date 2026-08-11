package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHostRPID(t *testing.T) {
	cases := map[string]string{
		"cockpit.example.org":       "cockpit.example.org",
		"cockpit.example.org:3000":  "cockpit.example.org",
		"COCKPIT.Example.org:3000":  "cockpit.example.org",
		"cockpit.example.org.:3000": "cockpit.example.org",
		"localhost:3000":            "localhost",
		"192.168.1.10:3000":         "",
		"127.0.0.1":                 "",
		"[::1]:3000":                "",
		"":                          "",
	}
	for host, want := range cases {
		if got := HostRPID(host); got != want {
			t.Errorf("HostRPID(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestMatchesHost(t *testing.T) {
	cases := []struct {
		rpID, host string
		want       bool
	}{
		{"cockpit.example.org", "cockpit.example.org", true},
		{"example.org", "cockpit.example.org", true},
		{"cockpit.example.org", "example.org", false},
		{"cockpit.example.org", "other.example.org", false},
		{"cockpit.example.org", "evilcockpit.example.org", false},
		{"", "cockpit.example.org", false},
		{"cockpit.example.org", "", false},
	}
	for _, tc := range cases {
		if got := matchesHost(tc.rpID, tc.host); got != tc.want {
			t.Errorf("matchesHost(%q, %q) = %v, want %v", tc.rpID, tc.host, got, tc.want)
		}
	}
}

// A file dropped into the state dir by hand takes effect on the next request,
// there is no restart and no unlocking step in between. And a passkey belongs
// to the host it was registered for: under another name the login page stays
// on username and password.
func TestRegisteredFollowsTheFileAndTheHost(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)

	if store.Registered("cockpit.example.org") {
		t.Fatal("an empty state dir offers a passkey")
	}

	writeFile(t, filepath.Join(dir, passkeyFileName), `{"credentials":[{"id":"abc","rp_id":"cockpit.example.org"}]}`)
	if !store.Registered("cockpit.example.org") {
		t.Fatal("a passkey placed by hand is not picked up")
	}
	if store.Registered("other.example.org") {
		t.Fatal("a passkey is offered on a host it was not registered for")
	}

	if err := os.Remove(filepath.Join(dir, passkeyFileName)); err != nil {
		t.Fatal(err)
	}
	if store.Registered("cockpit.example.org") {
		t.Fatal("the passkey survived its file")
	}
}

func TestCredentialLifecycle(t *testing.T) {
	store := New(t.TempDir())
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	store.AddCredential(Credential{ID: "one", RPID: "cockpit.example.org", Label: "iPhone", CreatedAt: now})
	store.AddCredential(Credential{ID: "two", RPID: "other.example.org", Label: "Laptop", CreatedAt: now})

	if got := len(store.Credentials()); got != 2 {
		t.Fatalf("stored %d credentials, want 2", got)
	}
	if got := len(store.CredentialsFor("cockpit.example.org")); got != 1 {
		t.Fatalf("%d credentials for the host, want 1", got)
	}

	store.RecordUse("one", 7, now.Add(time.Hour))
	credential := store.CredentialsFor("cockpit.example.org")[0]
	if credential.SignCount != 7 {
		t.Fatalf("sign count %d, want 7", credential.SignCount)
	}
	if credential.LastUsedAt.IsZero() {
		t.Fatal("last used stayed empty")
	}

	if !store.DeleteCredential("one") {
		t.Fatal("delete reported nothing removed")
	}
	if store.DeleteCredential("one") {
		t.Fatal("delete removed the same credential twice")
	}
	if got := len(store.Credentials()); got != 1 {
		t.Fatalf("%d credentials left, want 1", got)
	}
}

func TestSignCountOK(t *testing.T) {
	cases := []struct {
		stored, presented uint32
		want              bool
	}{
		{0, 0, true},   // a platform passkey never counts
		{0, 1, true},   // a counting authenticator starts
		{7, 8, true},   // the normal step
		{7, 7, false},  // replay of a counter that stood still
		{7, 6, false},  // a clone signing behind the original
		{7, 0, false},  // a clone that does not count at all
		{1, 99, true},  // a gap is fine, only shrinking is not
		{99, 1, false}, // the clone is far behind
	}
	for _, tc := range cases {
		if got := SignCountOK(tc.stored, tc.presented); got != tc.want {
			t.Errorf("SignCountOK(%d, %d) = %v, want %v", tc.stored, tc.presented, got, tc.want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Package auth holds the passkeys this cockpit accepts: one JSON file in the
// state directory, plus the WebAuthn ceremonies that register a passkey and
// sign in with it.
//
// Nothing here grants access. A passkey only proves the identity; the session
// line the cockpit checks is written by the web handlers, and it is the same
// line the password login writes.
//
// The file's presence is the whole switch: register a passkey and the login
// page offers it, remove the last one and it is gone. The file goes through
// internal/statefile, which reads through on every access, so a file placed by
// hand takes effect without a restart.
package auth

import (
	"net"
	"path/filepath"
	"strings"
	"sync"

	"github.com/local/dev-cockpit/internal/statefile"
)

const (
	passkeyFileName = "passkey.json"
	// fileMode keeps the file owner only: it is what decides who may sign in.
	fileMode = 0o600
)

// Store reads and writes the passkey file under dir. Safe for concurrent use.
type Store struct {
	dir string
	mu  sync.Mutex
}

// New returns a store backed by dir. Nothing is read or created now, the file
// is read on demand.
func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

// HostRPID reduces a request host to the WebAuthn relying party id: the
// hostname, lowercased, without the port. WebAuthn wants a domain name, so an
// IP address returns the empty string and no passkey can be registered or used
// under it.
func HostRPID(host string) string {
	name := strings.TrimSpace(host)
	if hostname, _, err := net.SplitHostPort(name); err == nil {
		name = hostname
	}
	name = strings.Trim(name, "[]")
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if name == "" || net.ParseIP(name) != nil {
		return ""
	}
	return name
}

// matchesHost reports whether a credential registered under rpID can be used
// on host. A relying party id may be the host itself or a parent domain of it,
// which is exactly the set the browser will offer.
func matchesHost(rpID, host string) bool {
	if rpID == "" || host == "" {
		return false
	}
	return rpID == host || strings.HasSuffix(host, "."+rpID)
}

func (s *Store) load(name string, v any) {
	statefile.Load(s.path(name), v)
}

func (s *Store) save(name string, v any) {
	statefile.Save(s.path(name), fileMode, v)
}

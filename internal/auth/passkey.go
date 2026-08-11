package auth

import (
	"time"
)

// Credential is one registered passkey, as it sits in auth/passkey.json.
//
// RPID is the host the passkey was registered under, taken from the request at
// registration time. A passkey only works under that name, so a cockpit reached
// under two names needs one passkey per name, and the settings page shows the
// relying party id of every entry for exactly that reason.
type Credential struct {
	ID         string    `json:"id"`         // base64url credential id
	PublicKey  string    `json:"public_key"` // base64 COSE key
	SignCount  uint32    `json:"sign_count"`
	Transports []string  `json:"transports,omitempty"`
	RPID       string    `json:"rp_id"`
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// Passkeys is the auth/passkey.json file. Several passkeys side by side are the
// normal case, a phone, a laptop, a hardware key, so this is a list and every
// entry carries its own label.
type Passkeys struct {
	Credentials []Credential `json:"credentials"`
}

// Credentials returns every registered passkey, whatever host it belongs to.
// The settings page lists them all so a lost device can be deleted from
// wherever you are signed in.
func (s *Store) Credentials() []Credential {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadPasskeys().Credentials
}

// Registered reports whether host has a passkey, which is what makes the
// passkey the way the login page opens. Under a name nothing was registered
// for, the browser would answer the prompt with "no matching key", so there
// the login page stays on username and password.
func (s *Store) Registered(host string) bool {
	return len(s.CredentialsFor(host)) > 0
}

// CredentialsFor returns the passkeys usable on host.
func (s *Store) CredentialsFor(host string) []Credential {
	matching := make([]Credential, 0, 2)
	for _, credential := range s.Credentials() {
		if matchesHost(credential.RPID, host) {
			matching = append(matching, credential)
		}
	}
	return matching
}

// AddCredential appends a freshly registered passkey.
func (s *Store) AddCredential(credential Credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file := s.loadPasskeys()
	file.Credentials = append(file.Credentials, credential)
	s.save(passkeyFileName, file)
}

// DeleteCredential removes the passkey with that id and reports whether one
// was there. The file stays behind once empty, an empty list simply offers no
// passkey method.
func (s *Store) DeleteCredential(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	file := s.loadPasskeys()
	kept := make([]Credential, 0, len(file.Credentials))
	for _, credential := range file.Credentials {
		if credential.ID != id {
			kept = append(kept, credential)
		}
	}
	if len(kept) == len(file.Credentials) {
		return false
	}
	file.Credentials = kept
	s.save(passkeyFileName, file)
	return true
}

// RecordUse writes back the counter a successful assertion presented and stamps
// the passkey as used. The counter is what a later sign in is compared against.
func (s *Store) RecordUse(id string, signCount uint32, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file := s.loadPasskeys()
	for i := range file.Credentials {
		if file.Credentials[i].ID != id {
			continue
		}
		file.Credentials[i].SignCount = signCount
		file.Credentials[i].LastUsedAt = now.UTC()
		s.save(passkeyFileName, file)
		return
	}
}

func (s *Store) loadPasskeys() Passkeys {
	var file Passkeys
	s.load(passkeyFileName, &file)
	return file
}

// SignCountOK compares the counter an authenticator presented with the stored
// one. A counter that does not grow means the credential exists twice, so the
// sign in is refused. The exception is the authenticator that does not count at
// all: a platform passkey reports zero forever, and zero against zero is not a
// clone.
func SignCountOK(stored, presented uint32) bool {
	if stored == 0 && presented == 0 {
		return true
	}
	return presented > stored
}

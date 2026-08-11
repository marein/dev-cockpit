package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// AppName is the relying party name an authenticator shows while it asks.
const AppName = "Dev Cockpit"

// Ceremony is the state a passkey ceremony keeps between its two requests, the
// challenge above all. It rides in the session, so the web layer stores and
// reads it without knowing the library.
type Ceremony = webauthn.SessionData

// userHandle is the WebAuthn user handle of this cockpit. A cockpit has
// exactly one account, so the handle is a constant: it must stay the same for
// the life of a passkey, and deriving it from the configured user name would
// break every registered key the moment somebody changes --auth-user. It is
// opaque and carries no personal data, which is what the specification asks
// for.
var userHandle = []byte("dev-cockpit")

// ErrUnknownCredential is returned when an assertion names a passkey this
// cockpit does not know.
var ErrUnknownCredential = errors.New("this passkey is not registered here")

// ErrSignCount is returned when the counter of an assertion did not grow. That
// means the credential exists a second time somewhere.
var ErrSignCount = errors.New("the passkey counter went backwards, it may be a copy")

// account adapts the single cockpit account to the library's user interface.
type account struct {
	name        string
	credentials []webauthn.Credential
}

func (a account) WebAuthnID() []byte                         { return userHandle }
func (a account) WebAuthnName() string                       { return a.name }
func (a account) WebAuthnDisplayName() string                { return a.name }
func (a account) WebAuthnCredentials() []webauthn.Credential { return a.credentials }

// relyingParty builds the party for one request. The relying party id is the
// host and the origin is the address the browser used, so a cockpit reached
// under two names is two relying parties, each with its own passkeys.
func relyingParty(rpID, origin string) (*webauthn.WebAuthn, error) {
	if rpID == "" {
		return nil, errors.New("a passkey needs a domain name, this cockpit is reached at an IP address")
	}
	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: AppName,
		RPOrigins:     []string{origin},
	})
}

// BeginRegistration starts the ceremony that adds a passkey. The passkeys
// already registered for this host are excluded, so an authenticator that
// holds one offers to replace it instead of silently making a second.
func (s *Store) BeginRegistration(rpID, origin, username string) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	party, err := relyingParty(rpID, origin)
	if err != nil {
		return nil, nil, err
	}
	existing := s.CredentialsFor(rpID)
	exclude := make([]protocol.CredentialDescriptor, 0, len(existing))
	for _, credential := range existing {
		converted, err := credential.toLibrary()
		if err != nil {
			continue
		}
		exclude = append(exclude, converted.Descriptor())
	}
	return party.BeginRegistration(account{name: username}, webauthn.WithExclusions(exclude))
}

// FinishRegistration verifies the attestation and returns the credential to
// store. Label is what the user typed, it is what tells three entries apart a
// year later.
func (s *Store) FinishRegistration(rpID, origin, username, label string, session webauthn.SessionData, body io.Reader) (Credential, error) {
	party, err := relyingParty(rpID, origin)
	if err != nil {
		return Credential{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(body)
	if err != nil {
		return Credential{}, fmt.Errorf("the browser sent an answer this cockpit could not read: %w", err)
	}
	created, err := party.CreateCredential(account{name: username}, session, parsed)
	if err != nil {
		return Credential{}, err
	}
	return fromLibrary(created, rpID, label, time.Now()), nil
}

// BeginLogin starts the ceremony that signs in. Only the passkeys registered
// for this host are offered: any other one would make the browser answer with
// "no matching key".
func (s *Store) BeginLogin(rpID, origin, username string) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	party, err := relyingParty(rpID, origin)
	if err != nil {
		return nil, nil, err
	}
	user, err := s.accountFor(rpID, username)
	if err != nil {
		return nil, nil, err
	}
	return party.BeginLogin(user)
}

// FinishLogin verifies an assertion against the stored public key and returns
// the passkey that answered, with the counter the authenticator presented. The
// caller writes that counter back, it is what the next sign in is compared
// against.
//
// Origin, relying party id and challenge are checked by the library. The
// counter is checked here first: a counter that does not grow means the
// credential exists twice, and that answer must not reach the verification at
// all.
func (s *Store) FinishLogin(rpID, origin, username string, session webauthn.SessionData, body io.Reader) (Credential, uint32, error) {
	party, err := relyingParty(rpID, origin)
	if err != nil {
		return Credential{}, 0, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(body)
	if err != nil {
		return Credential{}, 0, fmt.Errorf("the browser sent an answer this cockpit could not read: %w", err)
	}
	presented := parsed.Response.AuthenticatorData.Counter
	id := base64.RawURLEncoding.EncodeToString(parsed.RawID)
	stored, ok := findCredential(s.CredentialsFor(rpID), id)
	if !ok {
		return Credential{}, 0, ErrUnknownCredential
	}
	if !SignCountOK(stored.SignCount, presented) {
		return Credential{}, 0, ErrSignCount
	}
	user, err := s.accountFor(rpID, username)
	if err != nil {
		return Credential{}, 0, err
	}
	verified, err := party.ValidateLogin(user, session, parsed)
	if err != nil {
		return Credential{}, 0, err
	}
	if verified.Authenticator.CloneWarning {
		return Credential{}, 0, ErrSignCount
	}
	return stored, presented, nil
}

// accountFor collects the passkeys of one host into the library's user.
func (s *Store) accountFor(rpID, username string) (account, error) {
	stored := s.CredentialsFor(rpID)
	if len(stored) == 0 {
		return account{}, errors.New("no passkey is registered for this address")
	}
	credentials := make([]webauthn.Credential, 0, len(stored))
	for _, credential := range stored {
		converted, err := credential.toLibrary()
		if err != nil {
			// A single unreadable entry must not take the working ones down
			// with it, the settings page is where it gets deleted.
			continue
		}
		credentials = append(credentials, converted)
	}
	if len(credentials) == 0 {
		return account{}, errors.New("the registered passkeys of this address could not be read")
	}
	return account{name: username, credentials: credentials}, nil
}

func findCredential(list []Credential, id string) (Credential, bool) {
	for _, credential := range list {
		if credential.ID == id {
			return credential, true
		}
	}
	return Credential{}, false
}

func (c Credential) toLibrary() (webauthn.Credential, error) {
	id, err := decodeBase64(c.ID)
	if err != nil {
		return webauthn.Credential{}, fmt.Errorf("credential id: %w", err)
	}
	key, err := decodeBase64(c.PublicKey)
	if err != nil {
		return webauthn.Credential{}, fmt.Errorf("public key: %w", err)
	}
	transports := make([]protocol.AuthenticatorTransport, 0, len(c.Transports))
	for _, transport := range c.Transports {
		transports = append(transports, protocol.AuthenticatorTransport(transport))
	}
	return webauthn.Credential{
		ID:            id,
		PublicKey:     key,
		Transport:     transports,
		Authenticator: webauthn.Authenticator{SignCount: c.SignCount},
	}, nil
}

// fromLibrary turns a freshly created credential into the stored entry. The
// transports travel with it because they are what makes the platform offer the
// cross device flow, the QR code path onto another machine.
func fromLibrary(credential *webauthn.Credential, rpID, label string, now time.Time) Credential {
	transports := make([]string, 0, len(credential.Transport))
	for _, transport := range credential.Transport {
		if value := strings.TrimSpace(string(transport)); value != "" {
			transports = append(transports, value)
		}
	}
	return Credential{
		ID:         base64.RawURLEncoding.EncodeToString(credential.ID),
		PublicKey:  base64.StdEncoding.EncodeToString(credential.PublicKey),
		SignCount:  credential.Authenticator.SignCount,
		Transports: transports,
		RPID:       rpID,
		Label:      label,
		CreatedAt:  now.UTC(),
	}
}

// decodeBase64 reads either alphabet, with or without padding. The file may be
// written by hand, so it must not insist on one spelling.
func decodeBase64(value string) ([]byte, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "=")
	if trimmed == "" {
		return nil, errors.New("empty value")
	}
	if raw, err := base64.RawURLEncoding.DecodeString(trimmed); err == nil {
		return raw, nil
	}
	return base64.RawStdEncoding.DecodeString(trimmed)
}

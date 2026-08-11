package auth

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// assertionBody builds the shape a browser posts back, with a chosen counter.
// The signature is nonsense on purpose: the counter is checked before anything
// is verified, so a shrinking counter must never reach the verification.
func assertionBody(t *testing.T, credentialID string, counter uint32, origin, challenge string) string {
	t.Helper()
	authData := make([]byte, 37)
	authData[32] = 0x01 // user present
	binary.BigEndian.PutUint32(authData[33:], counter)
	clientData, err := json.Marshal(map[string]string{
		"type":      "webauthn.get",
		"challenge": challenge,
		"origin":    origin,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"id":    credentialID,
		"rawId": credentialID,
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authData),
			"signature":         base64.RawURLEncoding.EncodeToString([]byte("not a signature")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestFinishLoginRefusesAShrinkingSignCount(t *testing.T) {
	const (
		rpID      = "cockpit.example.org"
		origin    = "https://cockpit.example.org"
		challenge = "Y2hhbGxlbmdl"
		id        = "AQIDBA"
	)
	store := New(t.TempDir())
	store.AddCredential(Credential{
		ID:        id,
		PublicKey: base64.StdEncoding.EncodeToString([]byte("cose")),
		SignCount: 7,
		RPID:      rpID,
	})
	ceremony := Ceremony{Challenge: challenge, RelyingPartyID: rpID, UserID: userHandle}

	for _, counter := range []uint32{6, 7, 0} {
		body := assertionBody(t, id, counter, origin, challenge)
		_, _, err := store.FinishLogin(rpID, origin, "admin", ceremony, strings.NewReader(body))
		if !errors.Is(err, ErrSignCount) {
			t.Fatalf("counter %d against the stored 7 gave %v, want ErrSignCount", counter, err)
		}
	}

	// A counter that grows passes the gate and fails later, on the signature.
	// That is the proof the gate is not what refuses every assertion.
	body := assertionBody(t, id, 8, origin, challenge)
	if _, _, err := store.FinishLogin(rpID, origin, "admin", ceremony, strings.NewReader(body)); errors.Is(err, ErrSignCount) {
		t.Fatal("a growing counter was refused as a clone")
	} else if err == nil {
		t.Fatal("a forged signature was accepted")
	}
}

func TestFinishLoginRefusesAnUnknownCredential(t *testing.T) {
	const (
		rpID   = "cockpit.example.org"
		origin = "https://cockpit.example.org"
	)
	store := New(t.TempDir())
	store.AddCredential(Credential{ID: "AQIDBA", PublicKey: "Y29zZQ", RPID: rpID})
	body := assertionBody(t, "BQYHCA", 1, origin, "Y2hhbGxlbmdl")
	_, _, err := store.FinishLogin(rpID, origin, "admin", Ceremony{Challenge: "Y2hhbGxlbmdl"}, strings.NewReader(body))
	if !errors.Is(err, ErrUnknownCredential) {
		t.Fatalf("got %v, want ErrUnknownCredential", err)
	}
}

// A passkey of another host is never offered, so its host cannot start a
// ceremony at all.
func TestBeginLoginNeedsACredentialOfThisHost(t *testing.T) {
	store := New(t.TempDir())
	store.AddCredential(Credential{ID: "AQIDBA", PublicKey: "Y29zZQ", RPID: "cockpit.example.org"})
	if _, _, err := store.BeginLogin("other.example.org", "https://other.example.org", "admin"); err == nil {
		t.Fatal("a host without a passkey started a ceremony")
	}
	if _, _, err := store.BeginLogin("cockpit.example.org", "https://cockpit.example.org", "admin"); err != nil {
		t.Fatalf("the registered host cannot start a ceremony: %v", err)
	}
}

// An IP address is not a relying party id, so no ceremony can run under one.
func TestCeremoniesNeedADomainName(t *testing.T) {
	store := New(t.TempDir())
	if _, _, err := store.BeginRegistration("", "https://192.168.1.10:3000", "admin"); err == nil {
		t.Fatal("a registration started without a relying party id")
	}
}

func TestCredentialRoundTripsThroughTheFile(t *testing.T) {
	store := New(t.TempDir())
	stored := Credential{
		ID:         "AQIDBA",
		PublicKey:  base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}),
		Transports: []string{"internal", "hybrid"},
		RPID:       "cockpit.example.org",
		Label:      "iPhone",
	}
	store.AddCredential(stored)
	converted, err := store.Credentials()[0].toLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if string(converted.ID) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("credential id decoded to %v", converted.ID)
	}
	if len(converted.Transport) != 2 || string(converted.Transport[1]) != "hybrid" {
		t.Fatalf("transports decoded to %v, hybrid is what makes the cross device flow appear", converted.Transport)
	}
}

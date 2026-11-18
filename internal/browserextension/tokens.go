// Package browserextension holds the browser extension's tokens.
package browserextension

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/statefile"
)

const MaxTokens = 20

const MaxNameLength = 64

// As much entropy as a shared link carries.
const tokenBytes = 32

// A busy extension writes the file once a minute, not on every request.
const usedEvery = time.Minute

// The one answer for a token that opens nothing, so a caller cannot tell an
// unknown token from a revoked one.
var ErrNotFound = errors.New("No such token.")

var errNotSaved = errors.New("The tokens could not be saved.")

type Token struct {
	ID        string    `json:"id"`
	Hash      string    `json:"hash"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UsedAt    time.Time `json:"usedAt"`
}

type Store struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

func NewStore(stateDir string) *Store {
	return &Store{path: filepath.Join(stateDir, "browser-extension", "tokens.json"), now: time.Now}
}

type file struct {
	Tokens []Token `json:"tokens"`
}

func (s *Store) Create(name string) (Token, string, error) {
	name, err := cleanName(name)
	if err != nil {
		return Token{}, "", err
	}
	var raw [tokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Token{}, "", errors.New("The token could not be made.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var f file
	if err := statefile.Read(s.path, &f); err != nil {
		return Token{}, "", errNotSaved
	}
	if len(f.Tokens) >= MaxTokens {
		return Token{}, "", fmt.Errorf("There are %d tokens already. Revoke one you no longer use first.", MaxTokens)
	}
	if slices.ContainsFunc(f.Tokens, func(t Token) bool { return strings.EqualFold(t.Name, name) }) {
		return Token{}, "", errors.New("A token with that name exists. Name each after its browser.")
	}
	created := Token{ID: statefile.NewID(), Hash: hashOf(raw[:]), Name: name, CreatedAt: s.now().UTC()}
	f.Tokens = append(f.Tokens, created)
	if err := s.save(f); err != nil {
		return Token{}, "", err
	}
	return created, base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (s *Store) List() []Token {
	var f file
	statefile.Load(s.path, &f)
	return f.Tokens
}

func (s *Store) Lookup(token string) (Token, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != tokenBytes {
		return Token{}, ErrNotFound
	}
	hash := hashOf(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	var f file
	if err := statefile.Read(s.path, &f); err != nil {
		return Token{}, ErrNotFound
	}
	at := -1
	for i, t := range f.Tokens {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(t.Hash)) == 1 {
			at = i
		}
	}
	if at < 0 {
		return Token{}, ErrNotFound
	}
	now := s.now().UTC()
	if now.Sub(f.Tokens[at].UsedAt) >= usedEvery {
		f.Tokens[at].UsedAt = now
		_ = s.save(f)
	}
	return f.Tokens[at], nil
}

func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var f file
	if err := statefile.Read(s.path, &f); err != nil {
		return errNotSaved
	}
	at := slices.IndexFunc(f.Tokens, func(t Token) bool { return t.ID == id })
	if at < 0 {
		return ErrNotFound
	}
	f.Tokens = slices.Delete(f.Tokens, at, at+1)
	return s.save(f)
}

// Callers hold s.mu.
func (s *Store) save(f file) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return errNotSaved
	}
	if err := statefile.Write(s.path, 0o600, f); err != nil {
		return errNotSaved
	}
	return nil
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("A token needs a name, say the browser that holds it.")
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxNameLength || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("A name is one line of at most %d characters.", MaxNameLength)
	}
	return name, nil
}

func hashOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

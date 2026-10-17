// Package share holds the links a person without a login opens part of the
// cockpit through. A link carries a secret token, only its hash is stored, an
// optional name, one lifetime and the resources it opens, each with what the
// link grants there. It ends at its expiry, when it is revoked and when its
// last resource is gone. Whether a resource still lives is the caller's
// question, this package only knows what was shared, how far and until when.
package share

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
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// Kind is what sort of thing a resource of a link is. The values are stored
// in the link's file.
type Kind string

// KindProject is a project, named by its directory name.
const KindProject Kind = "project"

// Permission is one thing a resource lets its guest do beyond reading.
// Reading is what every resource grants, it is no permission of its own. The
// values are stored in the link's file.
type Permission string

// The permissions a resource can grant. Terminals starts, stops, resumes and
// renames the project's coders and shells, and nothing deletes one: deleting
// a terminal is never a guest's, so no permission names it.
const (
	Files         Permission = "files"
	Comments      Permission = "comments"
	LocalGit      Permission = "git-local"
	RemoteGit     Permission = "git-remote"
	TerminalInput Permission = "terminal-input"
	Terminals     Permission = "terminals"
	Compose       Permission = "compose"
)

// All is every permission in the order a form and a list show them.
var All = []Permission{Files, Comments, LocalGit, RemoteGit, TerminalInput, Terminals, Compose}

// MinLifetime and MaxLifetime bound how long a link lives. A minute is the
// smallest span the form can ask for, a month bounds a link nobody remembered
// to revoke.
const (
	MinLifetime = time.Minute
	MaxLifetime = 30 * 24 * time.Hour
)

// MaxNameLength bounds a link's name in runes, it says for whom the link is.
const MaxNameLength = 64

// maxTargetLength bounds a resource's target, a directory name is far shorter.
const maxTargetLength = 255

// tokenBytes is the entropy of a token, as much as the session secrets carry.
const tokenBytes = 32

// ErrNotFound is the one answer for a token that does not open a share, so a
// caller cannot tell an unknown token from an expired or revoked one.
var ErrNotFound = errors.New("No such share.")

// Resource is one thing a link opens, with what the link grants there. The
// permissions belong to the resource, so they can change on a standing link.
type Resource struct {
	Kind        Kind         `json:"kind"`
	Target      string       `json:"target"`
	Permissions []Permission `json:"permissions"`
}

// Share is one link as it stands on disk.
type Share struct {
	ID        string     `json:"id"`
	TokenHash string     `json:"tokenHash"`
	Name      string     `json:"name,omitempty"`
	Resources []Resource `json:"resources"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
}

// Resource answers the link's resource of that kind and target.
func (sh Share) Resource(kind Kind, target string) (Resource, bool) {
	for _, r := range sh.Resources {
		if r.Kind == kind && r.Target == target {
			return r, true
		}
	}
	return Resource{}, false
}

// Targets lists the targets of the link's resources of one kind, in the order
// they were added.
func (sh Share) Targets(kind Kind) []string {
	var out []string
	for _, r := range sh.Resources {
		if r.Kind == kind {
			out = append(out, r.Target)
		}
	}
	return out
}

// opens reports whether the share opens anything at the time now.
func (sh Share) opens(now time.Time) bool {
	return len(sh.Resources) > 0 && sh.ExpiresAt.After(now)
}

// Store keeps every share in a file of its own under the state directory,
// named by its token's hash. A lookup reads the one file that hash names, so it
// costs the same with one link or with thousands, and a name aimed at by
// guessing reveals nothing: nobody can pick a token for a hash. Every call
// reads through, writes are atomic.
type Store struct {
	dir string
	now func() time.Time

	mu      sync.Mutex
	changed chan struct{}
}

// NewStore returns the store of the given state directory.
func NewStore(stateDir string) *Store {
	return &Store{
		dir:     filepath.Join(stateDir, "shares"),
		now:     time.Now,
		changed: make(chan struct{}),
	}
}

// Create stores a link to the resources and returns it with its token. The
// token leaves this function once and is never stored.
func (s *Store) Create(name string, resources []Resource, lifetime time.Duration) (Share, string, error) {
	name, err := cleanName(name)
	if err != nil {
		return Share{}, "", err
	}
	if len(resources) == 0 {
		return Share{}, "", errors.New("A link opens at least one project.")
	}
	var checked []Resource
	for _, r := range resources {
		r, err := cleanResource(r)
		if err != nil {
			return Share{}, "", err
		}
		if slices.ContainsFunc(checked, func(c Resource) bool { return c.Kind == r.Kind && c.Target == r.Target }) {
			return Share{}, "", errAlreadyOn
		}
		checked = append(checked, r)
	}
	if err := checkLifetime(lifetime); err != nil {
		return Share{}, "", err
	}
	token, hash, err := newToken()
	if err != nil {
		return Share{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.prune(now)
	created := Share{
		ID:        statefile.NewID(),
		TokenHash: hash,
		Name:      name,
		Resources: checked,
		CreatedAt: now.UTC(),
		ExpiresAt: now.Add(lifetime).UTC(),
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Share{}, "", errNotSaved
	}
	if err := statefile.Write(s.file(hash), 0o600, created); err != nil {
		return Share{}, "", errNotSaved
	}
	s.wake()
	return created, token, nil
}

var (
	errNotSaved  = errors.New("The link could not be saved.")
	errAlreadyOn = errors.New("That project is on the link already.")
)

// cleanName trims a link's name and keeps it to one short line.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxNameLength || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("A name is one line of at most %d characters.", MaxNameLength)
	}
	return name, nil
}

// cleanResource checks a resource a link is made or grown with and puts its
// permissions in the order of All, each once.
func cleanResource(r Resource) (Resource, error) {
	r.Target = strings.TrimSpace(r.Target)
	if r.Kind != KindProject || r.Target == "" || len(r.Target) > maxTargetLength {
		return Resource{}, errors.New("This cannot be shared.")
	}
	granted := []Permission{}
	for _, p := range All {
		if slices.Contains(r.Permissions, p) {
			granted = append(granted, p)
		}
	}
	for _, p := range r.Permissions {
		if !slices.Contains(All, p) {
			return Resource{}, errors.New("A link cannot grant that.")
		}
	}
	r.Permissions = granted
	return r, nil
}

func checkLifetime(lifetime time.Duration) error {
	if lifetime < MinLifetime || lifetime > MaxLifetime {
		return errors.New("A link lives between a minute and 30 days.")
	}
	return nil
}

// Lookup returns the share a token opens. The stored hash is compared in
// constant time. Unknown, malformed, revoked and expired tokens all answer
// ErrNotFound, and so does a link that opens nothing anymore.
func (s *Store) Lookup(token string) (Share, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != tokenBytes {
		return Share{}, ErrNotFound
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	var sh Share
	if err := statefile.Read(s.file(hash), &sh); err != nil ||
		subtle.ConstantTimeCompare([]byte(hash), []byte(sh.TokenHash)) != 1 ||
		!sh.opens(s.now()) {
		return Share{}, ErrNotFound
	}
	return sh, nil
}

// Current answers a share a lookup returned as it stands now, and false once
// it is revoked, expired or opens nothing anymore.
func (s *Store) Current(sh Share) (Share, bool) {
	if !isHash(sh.TokenHash) {
		return Share{}, false
	}
	var stored Share
	if err := statefile.Read(s.file(sh.TokenHash), &stored); err != nil ||
		stored.ID != sh.ID || stored.TokenHash != sh.TokenHash || !stored.opens(s.now()) {
		return Share{}, false
	}
	return stored, true
}

// List returns the shares that still open something, oldest first.
func (s *Store) List() []Share {
	now := s.now()
	out := []Share{}
	for _, sh := range s.stored() {
		if sh.opens(now) {
			out = append(out, sh)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Update renames a link and, with a lifetime other than zero, gives it that
// long from now. Its token and its resources stay.
func (s *Store) Update(id, name string, lifetime time.Duration) error {
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	if lifetime != 0 {
		if err := checkLifetime(lifetime); err != nil {
			return err
		}
	}
	return s.change(id, func(sh *Share, now time.Time) error {
		sh.Name = name
		if lifetime != 0 {
			sh.ExpiresAt = now.Add(lifetime).UTC()
		}
		return nil
	})
}

// Add puts one more resource on a standing link, which its guest reaches
// under the same token.
func (s *Store) Add(id string, r Resource) error {
	r, err := cleanResource(r)
	if err != nil {
		return err
	}
	return s.change(id, func(sh *Share, _ time.Time) error {
		if _, ok := sh.Resource(r.Kind, r.Target); ok {
			return errAlreadyOn
		}
		sh.Resources = append(sh.Resources, r)
		return nil
	})
}

// Remove takes a resource off a link. A link whose last resource it was
// opens nothing anymore and is deleted, which ended reports.
func (s *Store) Remove(id string, kind Kind, target string) (ended bool, err error) {
	err = s.change(id, func(sh *Share, _ time.Time) error {
		kept := slices.DeleteFunc(slices.Clone(sh.Resources), func(r Resource) bool { return r.Kind == kind && r.Target == target })
		if len(kept) == len(sh.Resources) {
			return errNotOn
		}
		sh.Resources = kept
		ended = len(kept) == 0
		return nil
	})
	return ended, err
}

var errNotOn = errors.New("That project is not on the link.")

// Revoke ends a share at once.
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh, ok := s.find(id, s.now())
	if !ok {
		return ErrNotFound
	}
	if err := os.Remove(s.file(sh.TokenHash)); err != nil {
		return errors.New("The link could not be revoked.")
	}
	s.wake()
	return nil
}

// RemoveTarget takes a resource off every link, which is what a deleted
// project does to its links. A link left with nothing is deleted.
func (s *Store) RemoveTarget(kind Kind, target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	moved := false
	for _, sh := range s.stored() {
		if _, ok := sh.Resource(kind, target); !ok {
			continue
		}
		sh.Resources = slices.DeleteFunc(sh.Resources, func(r Resource) bool { return r.Kind == kind && r.Target == target })
		if s.save(sh) == nil {
			moved = true
		}
	}
	if moved {
		s.wake()
	}
}

// Changed returns a channel that is closed on the next write, so an open guest
// stream checks its share again the moment it changes. Subscribe before the
// check, then wait.
func (s *Store) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

// change runs one edit of a standing link and saves it. Callers hold no lock.
func (s *Store) change(id string, edit func(*Share, time.Time) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	sh, ok := s.find(id, now)
	if !ok {
		return ErrNotFound
	}
	if err := edit(&sh, now); err != nil {
		return err
	}
	if err := s.save(sh); err != nil {
		return err
	}
	s.wake()
	return nil
}

// save writes a link, or deletes it when it opens nothing anymore. Callers
// hold s.mu.
func (s *Store) save(sh Share) error {
	if len(sh.Resources) == 0 {
		if err := os.Remove(s.file(sh.TokenHash)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("The link could not be ended.")
		}
		return nil
	}
	if err := statefile.Write(s.file(sh.TokenHash), 0o600, sh); err != nil {
		return errNotSaved
	}
	return nil
}

// find answers the standing link of an id. Callers hold s.mu.
func (s *Store) find(id string, now time.Time) (Share, bool) {
	for _, sh := range s.stored() {
		if sh.ID == id && sh.opens(now) {
			return sh, true
		}
	}
	return Share{}, false
}

// stored reads every share file that reads whole and lies under its own
// hash. A file that does not is left out here and left alone on disk.
func (s *Store) stored() []Share {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var out []Share
	for _, entry := range entries {
		hash, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !isHash(hash) || !entry.Type().IsRegular() {
			continue
		}
		var sh Share
		if err := statefile.Read(s.file(hash), &sh); err != nil || sh.TokenHash != hash {
			continue
		}
		out = append(out, sh)
	}
	return out
}

// prune removes the shares that open nothing anymore, expired or emptied,
// the one deletion that needs no read of anything but the share itself.
// Callers hold s.mu.
func (s *Store) prune(now time.Time) {
	for _, sh := range s.stored() {
		if !sh.opens(now) {
			_ = os.Remove(s.file(sh.TokenHash))
		}
	}
}

// wake closes the channel Changed handed out. Callers hold s.mu.
func (s *Store) wake() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *Store) file(hash string) string {
	return filepath.Join(s.dir, hash+".json")
}

func isHash(name string) bool {
	if len(name) != 2*sha256.Size {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil && strings.ToLower(name) == name
}

func newToken() (string, string, error) {
	var raw [tokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", errors.New("No token could be made.")
	}
	sum := sha256.Sum256(raw[:])
	return base64.RawURLEncoding.EncodeToString(raw[:]), hex.EncodeToString(sum[:]), nil
}

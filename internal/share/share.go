// Package share holds the links a person without a login opens part of the
// cockpit through. A link carries a secret token, only its hash is stored, a
// name for whom it is, one lifetime and the resources it opens, each with what
// the link grants there. It ends at its expiry, when it is revoked and when its
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

// KindProject is a project, named by its directory name, KindAssistant an
// assistant, named by its id.
const (
	KindProject   Kind = "project"
	KindAssistant Kind = "assistant"
)

// kinds is every kind a link can carry.
var kinds = []Kind{KindProject, KindAssistant}

// word is what a refusal calls a resource of the kind.
func (k Kind) word() string {
	if k == KindAssistant {
		return "assistant"
	}
	return "project"
}

// Permission is one thing a project of a link lets its guest do beyond
// reading. Reading is what every resource grants, it is no permission of its
// own, and an assistant grants nothing beyond it. The values are stored in the
// link's file.
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
var ErrNotFound = errors.New("No such link.")

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
	Name      string     `json:"name"`
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

	mu       sync.Mutex
	changed  chan struct{}
	onChange func()
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
		return Share{}, "", errors.New("A link needs at least one project or assistant.")
	}
	checked, err := cleanResources(resources)
	if err != nil {
		return Share{}, "", err
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

var errNotSaved = errors.New("The link could not be saved.")

func errAlreadyOn(k Kind) error {
	return fmt.Errorf("That %s is already on the link.", k.word())
}

// cleanName trims a link's name, which every link carries, and keeps it to
// one short line.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("A link needs a name.")
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxNameLength || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("A name is one line of at most %d characters.", MaxNameLength)
	}
	return name, nil
}

// cleanResource checks a resource a link is made or grown with and puts its
// permissions in the order of All, each once. An assistant grants none.
func cleanResource(r Resource) (Resource, error) {
	r.Target = strings.TrimSpace(r.Target)
	if !slices.Contains(kinds, r.Kind) || r.Target == "" || len(r.Target) > maxTargetLength {
		return Resource{}, errors.New("This cannot be shared.")
	}
	if r.Kind != KindProject && len(r.Permissions) > 0 {
		return Resource{}, errors.New("A link cannot allow that.")
	}
	granted := []Permission{}
	for _, p := range All {
		if slices.Contains(r.Permissions, p) {
			granted = append(granted, p)
		}
	}
	for _, p := range r.Permissions {
		if !slices.Contains(All, p) {
			return Resource{}, errors.New("A link cannot allow that.")
		}
	}
	r.Permissions = granted
	return r, nil
}

// cleanResources checks the resources a link is made or grown with, each of
// which may stand among them once.
func cleanResources(rs []Resource) ([]Resource, error) {
	checked := make([]Resource, 0, len(rs))
	for _, r := range rs {
		r, err := cleanResource(r)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(checked, func(c Resource) bool { return c.Kind == r.Kind && c.Target == r.Target }) {
			return nil, errAlreadyOn(r.Kind)
		}
		checked = append(checked, r)
	}
	return checked, nil
}

func checkLifetime(lifetime time.Duration) error {
	if lifetime < MinLifetime || lifetime > MaxLifetime {
		return errors.New("The expiry is between 1 minute and 30 days.")
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

// Change is one save of a standing link, applied whole or not at all. Remove
// names the resources it takes off and Grants what a resource that stays
// grants from now on, both by kind and target, so one that left the link
// meanwhile is passed over. Add puts resources on with what each grants, and
// passes over one that is on already. A lifetime other than zero gives the
// link that long from now. A link keeps its name.
type Change struct {
	Remove   []Resource
	Grants   []Resource
	Add      []Resource
	Lifetime time.Duration
}

// errEmptied refuses a save that would leave a link opening nothing, which is
// what revoking it is for.
var errEmptied = errors.New("A link needs at least one project or assistant. Revoke it to end it.")

// Apply saves one change of a standing link. The guest finds it from its next
// request on, under the same token.
func (s *Store) Apply(id string, c Change) error {
	if c.Lifetime != 0 {
		if err := checkLifetime(c.Lifetime); err != nil {
			return err
		}
	}
	added, err := cleanResources(c.Add)
	if err != nil {
		return err
	}
	grants := make([]Resource, 0, len(c.Grants))
	for _, r := range c.Grants {
		checked, err := cleanResource(r)
		if err != nil {
			return err
		}
		grants = append(grants, checked)
	}
	return s.change(id, func(sh *Share, now time.Time) error {
		sh.Resources = slices.DeleteFunc(sh.Resources, func(r Resource) bool {
			return slices.ContainsFunc(c.Remove, func(off Resource) bool { return off.Kind == r.Kind && off.Target == r.Target })
		})
		for i, r := range sh.Resources {
			if at := slices.IndexFunc(grants, func(g Resource) bool { return g.Kind == r.Kind && g.Target == r.Target }); at >= 0 {
				sh.Resources[i].Permissions = grants[at].Permissions
			}
		}
		for _, r := range added {
			if _, ok := sh.Resource(r.Kind, r.Target); !ok {
				sh.Resources = append(sh.Resources, r)
			}
		}
		if len(sh.Resources) == 0 {
			return errEmptied
		}
		if c.Lifetime != 0 {
			sh.ExpiresAt = now.Add(c.Lifetime).UTC()
		}
		return nil
	})
}

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
// project or assistant does to its links. A link left with nothing is deleted.
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

// OnChange sets what runs after every write of the links, set once before
// the store is used. It runs under the store's lock, so it must not block.
func (s *Store) OnChange(fn func()) {
	s.onChange = fn
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
			return errors.New("The link could not be deleted.")
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
	if s.onChange != nil {
		s.onChange()
	}
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
		return "", "", errors.New("The link could not be created.")
	}
	sum := sha256.Sum256(raw[:])
	return base64.RawURLEncoding.EncodeToString(raw[:]), hex.EncodeToString(sum[:]), nil
}

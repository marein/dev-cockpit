package forge

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// MaxPathLength bounds a repository path set by hand.
const MaxPathLength = 512

var (
	errBindingsUnread   = errors.New("The forge bindings could not be read, nothing was changed.")
	errBindingsNotSaved = errors.New("The forge binding could not be saved.")
)

// How is how a project binds to a forge.
type How string

const (
	// ByOrigin reads the binding off the project's origin.
	ByOrigin How = "origin"
	// ByHand binds the project to a host and a repository path the user
	// named, whatever its origin says.
	ByHand How = "hand"
	// Off binds the project to no forge.
	Off How = "off"
)

// Override is how the user binds a project. Only ByHand names a host, by its
// id, and a repository path on it.
type Override struct {
	How  How    `json:"how"`
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
}

// bindingFile holds the overrides by project name. A project that binds by
// its origin has none.
type bindingFile struct {
	Projects map[string]Override `json:"projects"`
}

func (s *Service) bindingsPath() string {
	return filepath.Join(s.dir, "bindings.json")
}

// Bindings is what binds the projects: every host and every override.
type Bindings struct {
	Hosts     Hosts
	overrides map[string]Override
}

// Bindings reads the hosts and the overrides once for every project a page
// shows. A file it cannot read leaves out what it holds and says so.
func (s *Service) Bindings() (Bindings, error) {
	var errs []error
	hosts, err := s.Hosts()
	if err != nil {
		errs = append(errs, errors.New("The added hosts could not be read."))
	}
	var f bindingFile
	if err := statefile.Read(s.bindingsPath(), &f); err != nil {
		errs = append(errs, errors.New("The forge bindings could not be read."))
	}
	return Bindings{Hosts: hosts, overrides: f.Projects}, errors.Join(errs...)
}

// Override answers how the project binds. An override the file holds in a
// shape no save writes, or by hand on a host that is gone, leaves it to the
// origin.
func (b Bindings) Override(project string) Override {
	o := b.overrides[project]
	switch o.How {
	case Off:
		return Override{How: Off}
	case ByHand:
		if _, ok := b.Hosts.host(o.Host); ok && repositoryPath(o.Path) {
			return o
		}
	}
	return Override{How: ByOrigin}
}

// Bind answers where the project with that origin is bound: where its
// override says, else where its origin names. A worktree binds by its main
// project's name.
func (b Bindings) Bind(project, origin string) (Binding, bool) {
	switch o := b.Override(project); o.How {
	case Off:
		return Binding{}, false
	case ByHand:
		h, _ := b.Hosts.host(o.Host)
		return Binding{Host: h, Path: o.Path}, true
	}
	return b.Hosts.Resolve(origin)
}

// SetOverride stores how the project binds. ByOrigin takes its override
// away, ByHand needs a host there is and a repository path.
func (s *Service) SetOverride(project string, o Override) error {
	o, err := s.checkOverride(o)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var f bindingFile
	if err := statefile.Read(s.bindingsPath(), &f); err != nil {
		return errBindingsUnread
	}
	if o.How == ByOrigin {
		if _, ok := f.Projects[project]; !ok {
			return nil
		}
		delete(f.Projects, project)
	} else {
		if f.Projects == nil {
			f.Projects = map[string]Override{}
		}
		f.Projects[project] = o
	}
	if err := s.write(s.bindingsPath(), f); err != nil {
		return errBindingsNotSaved
	}
	s.poll.nudge()
	return nil
}

func (s *Service) checkOverride(o Override) (Override, error) {
	switch o.How {
	case ByOrigin, Off:
		return Override{How: o.How}, nil
	case ByHand:
	default:
		return Override{}, errors.New("Pick how the project binds.")
	}
	if o.Host == "" {
		return Override{}, errors.New("Pick the host the repository lives on.")
	}
	if _, err := s.Host(o.Host); err != nil {
		return Override{}, err
	}
	path, err := checkRepositoryPath(o.Path)
	if err != nil {
		return Override{}, err
	}
	return Override{How: ByHand, Host: o.Host, Path: path}, nil
}

// checkRepositoryPath reads a repository path as typed, without the slashes
// around it and the .git an origin may end in.
func checkRepositoryPath(raw string) (string, error) {
	path := strings.Trim(strings.TrimSuffix(strings.Trim(strings.TrimSpace(raw), "/"), ".git"), "/")
	switch {
	case path == "":
		return "", errors.New("A binding by hand needs the repository path, like group/app.")
	case utf8.RuneCountInString(path) > MaxPathLength:
		return "", fmt.Errorf("A repository path has at most %d characters.", MaxPathLength)
	case strings.Contains(path, "://"):
		return "", errors.New("Type the repository path alone, like group/app.")
	case strings.IndexFunc(path, strayInPath) >= 0:
		return "", fmt.Errorf("%q is no repository path, like group/sub/app.", quoted(path))
	case !repositoryPath(path) || slices.ContainsFunc(strings.Split(path, "/"), func(s string) bool { return s == "." || s == ".." }):
		return "", fmt.Errorf("%q is no repository path, like group/app.", quoted(path))
	}
	return path, nil
}

// strayInPath reports a rune no repository path holds: a space, a control
// character, or one that ends the path of an address.
func strayInPath(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`?#%\`, r)
}

// Forget takes away the override, the record of the pipeline ends and the
// one of the review notes of a project that is gone.
func (s *Service) Forget(project string) {
	s.forgetOverride(project)
	s.forgetEnds(project)
	s.forgetNotes(project)
}

// forgetOverride takes away the override of a project that is gone.
func (s *Service) forgetOverride(project string) {
	s.DropOverrides(func(name string) bool { return name == project })
}

// DropOverrides takes away the override of every project gone reports. A
// file it cannot read cannot tell which there are, so it stays as it is.
func (s *Service) DropOverrides(gone func(project string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var f bindingFile
	if err := statefile.Read(s.bindingsPath(), &f); err != nil {
		log.Printf("forge bindings: %v, the bindings stay", err)
		return
	}
	n := len(f.Projects)
	maps.DeleteFunc(f.Projects, func(project string, _ Override) bool { return gone(project) })
	if len(f.Projects) == n {
		return
	}
	if err := s.write(s.bindingsPath(), f); err != nil {
		log.Printf("forge bindings: the bindings of projects that are gone could not be taken away: %v", err)
	}
}

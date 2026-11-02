package forge

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/gitfacts"
)

// The bounds of a repository search. A public search waits no longer than a
// forge takes for a common word, gitlab.com needed up to four seconds, and
// starts at a term long enough to narrow millions of repositories.
const (
	MaxRepositorySearch = 100
	MinPublicSearch     = 3
	RepositoryPage      = 8
	ownSearchTimeout    = 10 * time.Second
	publicSearchTimeout = 5 * time.Second
	maxCloneURL         = 500
)

// Repository is a repository a forge answered a search with: its path,
// "owner/name" or "group/sub/name", the addresses it clones from over SSH and
// over HTTPS, private or internal where the forge says so, and whether it is
// a fork or archived.
type Repository struct {
	Path       string
	SSHURL     string
	HTTPURL    string
	Visibility string
	Fork       bool
	Archived   bool
}

// The visibilities a search tells apart, a public repository has none.
const (
	VisibilityPrivate  = "private"
	VisibilityInternal = "internal"
)

// RepositorySearcher is a provider that searches the repositories of its
// forge. A term is SearchRefusal's to pass, and a word of it may hold a
// slash, the owner before it.
type RepositorySearcher interface {
	// OwnRepositories answers at most limit repositories the token's account
	// reaches on h, its own, those of the groups or organizations it is a
	// member of and those it collaborates on, the most recently active
	// first, all of them for an empty term and else those whose path carries
	// every word of term. A failure is the sentence the user reads.
	OwnRepositories(ctx context.Context, c *Client, h Host, token, term string, limit int) ([]Repository, error)
	// PublicRepositories answers at most limit public repositories on h whose
	// name carries term, the forge's best first. A failure is the sentence
	// the user reads.
	PublicRepositories(ctx context.Context, c *Client, h Host, token, term string, limit int) ([]Repository, error)
}

// Found is a repository a search offers: the host it lives on, its path,
// the address it clones from, the forge's SSH one where it has a usable one,
// and what the forge says of it.
type Found struct {
	Host       Host
	Path       string
	CloneURL   string
	Visibility string
	Fork       bool
	Archived   bool
}

// ErrNoSearch answers a host no search asks: one that is gone, lost its token
// or is of a forge that searches nothing.
var ErrNoSearch = errors.New("That host has no token any more or is gone.")

// SearchRefused answers a term no search takes.
type SearchRefused struct {
	Reason string
}

func (e *SearchRefused) Error() string {
	return e.Reason
}

// SearchRefusal says why term is no repository search, "" when it is one.
// A name on a forge holds no colon and no quote, so a term holds none
// either: both would reach a forge's query as its syntax, and a term that
// holds them is an address, which the form takes in a field of its own.
func SearchRefusal(term string) string {
	switch {
	case !utf8.ValidString(term):
		return "A search term is valid UTF-8."
	case utf8.RuneCountInString(term) > MaxRepositorySearch:
		return fmt.Sprintf("A search term has at most %d characters.", MaxRepositorySearch)
	case strings.ContainsAny(term, `:"`):
		return "A search term holds no colon and no quote, an address goes under An existing repository."
	}
	at := 0
	for _, word := range strings.Split(term, " ") {
		if i, r, ok := hiddenRune(word); ok {
			return fmt.Sprintf("A search term holds no control characters and no invisible characters but the space, its character %d is U+%04X.", at+i, r)
		}
		at += utf8.RuneCountInString(word) + 1
	}
	return ""
}

// SearchWords are the words of term, lowercased, in the order typed.
func SearchWords(term string) []string {
	return strings.Fields(strings.ToLower(term))
}

// Carries reports whether path carries every one of words, in any order and
// anywhere in it, ignoring case: the one rule every search of the app
// matches typed words by.
func Carries(path string, words []string) bool {
	lower := strings.ToLower(path)
	for _, word := range words {
		if !strings.Contains(lower, word) {
			return false
		}
	}
	return true
}

// NameWords are the words of term as a forge's search of names takes them:
// of a word with a slash what follows its last one, and the owners named
// before them, "marein/dev" asks for "dev" of "marein". A word that names
// an owner alone asks for nothing of the name.
func NameWords(term string) (names, owners []string) {
	for _, word := range SearchWords(term) {
		at := strings.LastIndex(word, "/")
		if at < 0 {
			names = append(names, word)
			continue
		}
		if owner := strings.Trim(word[:at], "/"); owner != "" {
			owners = append(owners, owner)
		}
		if name := word[at+1:]; name != "" {
			names = append(names, name)
		}
	}
	return names, owners
}

// OwnRepositories searches the repositories the token of the host with that
// id reaches whose path carries every word of term, all of them for an empty
// term, the most recently active first, at most RepositoryPage.
func (s *Service) OwnRepositories(ctx context.Context, hostID, term string) ([]Found, error) {
	return s.searchRepositories(ctx, hostID, term, ownSearchTimeout, RepositorySearcher.OwnRepositories)
}

// PublicRepositories searches the public repositories of the host with that
// id whose name carries term, the forge's best first, at most
// RepositoryPage. A term shorter than MinPublicSearch asks no host. The
// search stands in publicSearchTimeout, a host that answers later fails.
func (s *Service) PublicRepositories(ctx context.Context, hostID, term string) ([]Found, error) {
	if reason := SearchRefusal(term); reason != "" {
		return nil, &SearchRefused{Reason: reason}
	}
	if utf8.RuneCountInString(strings.TrimSpace(term)) < MinPublicSearch {
		return nil, nil
	}
	return s.searchRepositories(ctx, hostID, term, publicSearchTimeout, RepositorySearcher.PublicRepositories)
}

// SearchHosts answers the hosts a search asks, in the order of Hosts: those
// with a token whose forge searches.
func (s *Service) SearchHosts() ([]Host, error) {
	asked, err := s.searchHosts()
	if err != nil {
		return nil, err
	}
	out := make([]Host, 0, len(asked))
	for _, a := range asked {
		out = append(out, a.host)
	}
	return out, nil
}

// searchedHost is a host a search asks, through its provider with its token.
type searchedHost struct {
	host     Host
	searcher RepositorySearcher
	token    string
}

func (s *Service) searchHosts() ([]searchedHost, error) {
	hosts, err := s.Hosts()
	if err != nil {
		return nil, errors.New("The forge hosts could not be read.")
	}
	tokens, err := s.tokens()
	if err != nil {
		return nil, err
	}
	var asked []searchedHost
	for _, h := range hosts {
		p, _ := s.provider(h.Kind.ID)
		if searcher, ok := p.(RepositorySearcher); ok && tokens[h.ID] != "" {
			asked = append(asked, searchedHost{host: h, searcher: searcher, token: tokens[h.ID]})
		}
	}
	return asked, nil
}

type searchCall func(r RepositorySearcher, ctx context.Context, c *Client, h Host, token, term string, limit int) ([]Repository, error)

// searchRepositories asks the one host with that id through call. Only that
// host is asked, so a host that hangs holds no other's search.
func (s *Service) searchRepositories(ctx context.Context, hostID, term string, timeout time.Duration, call searchCall) ([]Found, error) {
	if reason := SearchRefusal(term); reason != "" {
		return nil, &SearchRefused{Reason: reason}
	}
	asked, err := s.searchHosts()
	if err != nil {
		return nil, err
	}
	at := slices.IndexFunc(asked, func(a searchedHost) bool { return a.host.ID == hostID })
	if at < 0 {
		return nil, ErrNoSearch
	}
	a := asked[at]
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	repos, err := call(a.searcher, ctx, s.client, a.host, a.token, strings.TrimSpace(term), RepositoryPage)
	if err != nil {
		return nil, err
	}
	return offered(a.host, repos), nil
}

// offered is what a search can offer of what a forge answered: each
// repository at most once and the first RepositoryPage, whose address
// cloneURL takes.
func offered(h Host, repos []Repository) []Found {
	var out []Found
	seen := map[string]bool{}
	for _, r := range repos {
		if len(out) == RepositoryPage {
			break
		}
		clone, ok := cloneURL(h, r)
		key := strings.ToLower(r.Path)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		visibility := r.Visibility
		if visibility != VisibilityPrivate && visibility != VisibilityInternal {
			visibility = ""
		}
		out = append(out, Found{Host: h, Path: r.Path, CloneURL: clone, Visibility: visibility, Fork: r.Fork, Archived: r.Archived})
	}
	return out
}

// cloneURL is the address a repository the forge answered clones from: its
// SSH address, else its https one, http for a host served over http. The
// forge wrote both, so one is taken only where it is a line of visible
// ASCII within what the form's field holds, no option, and binds to the
// host itself at the repository's own path, which keeps a hostile answer
// from filling in another server or another repository than the row names.
func cloneURL(h Host, r Repository) (string, bool) {
	for _, candidate := range []string{r.SSHURL, r.HTTPURL} {
		if candidate == "" || len(candidate) > maxCloneURL || strings.HasPrefix(candidate, "-") {
			continue
		}
		if strings.IndexFunc(candidate, func(c rune) bool { return c < '!' || c > '~' }) >= 0 {
			continue
		}
		remote, ok := gitfacts.ParseRemote(candidate)
		if !ok || !clonesOver(h, remote.Scheme, candidate) {
			continue
		}
		if b, ok := (Hosts{h}).Resolve(candidate); ok && strings.EqualFold(b.Path, r.Path) {
			return candidate, true
		}
	}
	return "", false
}

// clonesOver reports whether an address of scheme is one a search fills in
// for h: SSH, or the web's own scheme without a user in it.
func clonesOver(h Host, scheme, address string) bool {
	switch scheme {
	case "ssh":
		return true
	case "https", "http":
		u, err := url.Parse(address)
		return err == nil && u.User == nil && strings.HasPrefix(h.WebURL, scheme+"://")
	}
	return false
}

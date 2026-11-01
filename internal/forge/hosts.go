package forge

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// The bounds of what a host the user adds may carry.
const (
	MaxHosts          = 50
	MaxWebURLLength   = 512
	MaxAliases        = 10
	MaxCABundleBytes  = 64 << 10
	maxCertificates   = 32
	maxHostNameLength = 253
	maxQuotedInError  = 64
)

// ErrNotFound answers an id no host carries. Change and Delete answer it for
// a built in host too, which is no one's to change.
var ErrNotFound = errors.New("That host no longer exists.")

var (
	errUnread   = errors.New("The hosts could not be read, nothing was changed.")
	errNotSaved = errors.New("The hosts could not be saved.")
)

// Service knows the forge hosts: the built in ones of the providers and those
// the user added, which it keeps in a file of its own under the state
// directory, and the token of each, in a file of their own. Every call reads
// through, writes are atomic. Every request to a forge goes through its door,
// and what its poller read of the branches it keeps and of their review
// threads stands in memory, the pipeline ends and the review notes it
// published in files of their own.
type Service struct {
	dir       string
	providers []Provider
	builtins  Hosts
	client    *Client
	poll      *poller

	mu sync.Mutex

	endsMu sync.Mutex
	onEnd  func(PipelineEnd)

	reviewMu    sync.Mutex
	onReview    func(ReviewRead)
	onKept      func(projects map[string]bool)
	onNote      func(ReviewNote)
	reviewCalls sync.Mutex

	notesMu sync.Mutex
}

// New is the service of the given state directory, offering the kinds of the
// providers and the hosts they build in.
func New(stateDir string, providers []Provider, builtins ...Host) *Service {
	return &Service{dir: filepath.Join(stateDir, "forge"), providers: providers, builtins: builtins, client: NewClient(), poll: newPoller()}
}

// Input is what a host is added or changed with.
type Input struct {
	Kind     string
	WebURL   string
	Aliases  []string
	CABundle string
}

// file is the hosts file as it stands on disk. A kind is stored by its id.
type file struct {
	Hosts []storedHost `json:"hosts"`
}

type storedHost struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	WebURL   string   `json:"webURL"`
	Aliases  []string `json:"aliases,omitempty"`
	CABundle string   `json:"caBundle,omitempty"`
}

func (s *Service) hostsPath() string {
	return filepath.Join(s.dir, "hosts.json")
}

// Kinds are the sorts of forge a host can be.
func (s *Service) Kinds() []Kind {
	kinds := make([]Kind, 0, len(s.providers))
	for _, p := range s.providers {
		kinds = append(kinds, p.Kind())
	}
	return kinds
}

// Kind is the sort of forge with that id, false when no provider offers it.
func (s *Service) Kind(id string) (Kind, bool) {
	p, ok := s.provider(id)
	if !ok {
		return Kind{}, false
	}
	return p.Kind(), true
}

// Hosts answers every host, the built in ones first and then the added ones
// in the order they were added. A file it cannot read leaves the built in
// ones alone and says so.
func (s *Service) Hosts() (Hosts, error) {
	var f file
	err := statefile.Read(s.hostsPath(), &f)
	return s.known(f), err
}

// known resolves the stored hosts' kinds, after the built in ones. A stored
// host of a kind no provider declares any more is no host.
func (s *Service) known(f file) Hosts {
	hosts := slices.Clone(s.builtins)
	for _, h := range f.Hosts {
		if p, ok := s.provider(h.Kind); ok {
			hosts = append(hosts, Host{ID: h.ID, Kind: p.Kind(), WebURL: h.WebURL, Aliases: h.Aliases, CABundle: h.CABundle})
		}
	}
	return hosts
}

func (s *Service) provider(kind string) (Provider, bool) {
	at := slices.IndexFunc(s.providers, func(p Provider) bool { return p.Kind().ID == kind })
	if at < 0 {
		return nil, false
	}
	return s.providers[at], true
}

// Host answers the host with that id, a built in one included.
func (s *Service) Host(id string) (Host, error) {
	hosts, err := s.Hosts()
	if err != nil {
		return Host{}, errUnread
	}
	if h, ok := hosts.host(id); ok {
		return h, nil
	}
	return Host{}, ErrNotFound
}

// Add stores a host without a token, ChangeToken gives it one.
func (s *Service) Add(in Input) (Host, error) {
	var added Host
	err := s.change(func(f *file) error {
		if len(f.Hosts) >= MaxHosts {
			return fmt.Errorf("At most %d hosts can be added.", MaxHosts)
		}
		h, err := s.check("", in, s.known(*f))
		if err != nil {
			return err
		}
		h.ID = statefile.NewID()
		f.Hosts = append(f.Hosts, stored(h))
		added = h
		return nil
	})
	if err != nil {
		return Host{}, err
	}
	s.poll.hostChanged(added.ID)
	return added, nil
}

// Change gives the added host with that id a new kind, web address, alias
// hosts and CA bundle, its token stays.
func (s *Service) Change(id string, in Input) (Host, error) {
	var changed Host
	err := s.change(func(f *file) error {
		at := slices.IndexFunc(f.Hosts, func(h storedHost) bool { return h.ID == id })
		if at < 0 {
			return ErrNotFound
		}
		h, err := s.check(id, in, s.known(*f))
		if err != nil {
			return err
		}
		h.ID = id
		f.Hosts[at] = stored(h)
		changed = h
		return nil
	})
	if err != nil {
		return Host{}, err
	}
	s.poll.hostChanged(id)
	return changed, nil
}

func stored(h Host) storedHost {
	return storedHost{ID: h.ID, Kind: h.Kind.ID, WebURL: h.WebURL, Aliases: h.Aliases, CABundle: h.CABundle}
}

// Delete takes the added host with that id away, its token with it.
func (s *Service) Delete(id string) error {
	err := s.change(func(f *file) error {
		at := slices.IndexFunc(f.Hosts, func(h storedHost) bool { return h.ID == id })
		if at < 0 {
			return ErrNotFound
		}
		f.Hosts = slices.Delete(f.Hosts, at, at+1)
		return nil
	})
	if err != nil {
		return err
	}
	err = s.storeToken(id, "", true)
	s.poll.hostChanged(id)
	if err != nil {
		return errors.New("The host was deleted, its token could not be removed.")
	}
	return nil
}

// change applies one write to the hosts file as it stands, whole or not at
// all. The poller hears of it once the host's token is written too.
func (s *Service) change(apply func(*file) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var f file
	if err := statefile.Read(s.hostsPath(), &f); err != nil {
		return errUnread
	}
	if err := apply(&f); err != nil {
		return err
	}
	if err := s.write(s.hostsPath(), f); err != nil {
		return errNotSaved
	}
	return nil
}

// write stores one of the service's files, in a directory only the owner
// enters, since the tokens stand there.
func (s *Service) write(path string, v any) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return err
	}
	return statefile.Write(path, 0o600, v)
}

// check reads what a host is added or changed with against every host there
// is but itself: the kind one a provider declares, the web address an http
// or https address of a server, every alias the name of a host, no name
// twice, neither within the host nor taken by another one, and the CA bundle
// certificates for an https server.
func (s *Service) check(id string, in Input, hosts Hosts) (Host, error) {
	p, ok := s.provider(in.Kind)
	if !ok {
		return Host{}, errors.New("Pick the kind of forge.")
	}
	webURL, hostname, err := checkWebURL(in.WebURL)
	if err != nil {
		return Host{}, err
	}
	if len(in.Aliases) > MaxAliases {
		return Host{}, fmt.Errorf("A host has at most %d alias hosts.", MaxAliases)
	}
	names := []string{hostname}
	kept := []string{}
	for _, alias := range in.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		if !hostName(alias) {
			return Host{}, fmt.Errorf("%q is no host name.", quoted(alias))
		}
		names = append(names, alias)
		kept = append(kept, alias)
	}
	for i, name := range names {
		if slices.ContainsFunc(names[:i], func(seen string) bool { return strings.EqualFold(seen, name) }) {
			return Host{}, fmt.Errorf("%s is named twice.", quoted(name))
		}
		for _, other := range hosts {
			if other.ID == id {
				continue
			}
			if other.names(name) {
				return Host{}, fmt.Errorf("%s already belongs to %s.", quoted(name), quoted(other.WebURL))
			}
		}
	}
	bundle, err := checkCABundle(in.CABundle, webURL)
	if err != nil {
		return Host{}, err
	}
	return Host{Kind: p.Kind(), WebURL: webURL, Aliases: kept, CABundle: bundle}, nil
}

// checkCABundle reads a CA bundle: PEM certificates, which pem skips any
// comment line around, a certificate alone and no key ever.
func checkCABundle(raw, webURL string) (string, error) {
	bundle := strings.TrimSpace(raw)
	if bundle == "" {
		return "", nil
	}
	if len(bundle) > MaxCABundleBytes {
		return "", fmt.Errorf("A CA bundle has at most %d KiB.", MaxCABundleBytes>>10)
	}
	if !strings.HasPrefix(webURL, "https://") {
		return "", errors.New("A CA bundle only serves a web address that starts with https://.")
	}
	count := 0
	for rest := []byte(bundle); ; {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return "", fmt.Errorf("A CA bundle holds certificates only, not a %s.", quoted(block.Type))
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return "", fmt.Errorf("Certificate %d of the CA bundle does not parse.", count+1)
		}
		if count++; count > maxCertificates {
			return "", fmt.Errorf("A CA bundle holds at most %d certificates.", maxCertificates)
		}
		rest = next
	}
	if count == 0 {
		return "", errors.New("A CA bundle is one or more PEM certificates, each from -----BEGIN CERTIFICATE----- to -----END CERTIFICATE-----.")
	}
	return bundle, nil
}

// checkWebURL reads a host's web address and answers it without trailing
// slashes, with the name of its server.
func checkWebURL(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return "", "", errors.New("A host needs its web address.")
	case utf8.RuneCountInString(raw) > MaxWebURLLength:
		return "", "", fmt.Errorf("A web address has at most %d characters.", MaxWebURLLength)
	case strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return "", "", errors.New("A web address is one line without spaces.")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !hostName(u.Hostname()) {
		return "", "", errors.New("A web address starts with https:// or http:// and names a server, like https://git.example.com.")
	}
	if u.User != nil {
		return "", "", errors.New("A web address carries no user name or password.")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", "", errors.New("A web address carries no query and no fragment.")
	}
	return strings.TrimRight(raw, "/"), u.Hostname(), nil
}

// hostName reports whether a name can stand as the host of an origin: an
// address, or a name of a server or of an ~/.ssh/config entry, which takes
// anything but what separates the host from the rest of an origin.
func hostName(name string) bool {
	if net.ParseIP(name) != nil {
		return true
	}
	if name == "" || len(name) > maxHostNameLength || !utf8.ValidString(name) {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(":/@,[]%", r)
	}) < 0
}

// quoted cuts what a refusal repeats of a request to a bounded length.
func quoted(s string) string {
	return quotedCut(s, maxQuotedInError)
}

func quotedCut(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}

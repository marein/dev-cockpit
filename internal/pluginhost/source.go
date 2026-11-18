package pluginhost

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/netguard"
	"github.com/marein/dev-cockpit/internal/update"
)

// APIURLEnv sends every release request of a dev build to a stub, keeping
// the requested host in the Host header. Links below the stub are fetched
// there over plain http. The cli honors it in dev builds only.
const APIURLEnv = "DEV_COCKPIT_PLUGIN_API_URL"

// The forges a repository is read from. The person picks one, nothing is
// guessed from the host.
const (
	ForgeGitHub = "github"
	ForgeGitLab = "gitlab"
)

// Repository is a public repository on a GitHub or GitLab host.
type Repository struct {
	// URL is https://<host>/<path>.
	URL   string
	Forge string
	Host  string
	Path  string
}

// Bounds of a repository address.
const (
	maxRepositoryURL  = 512
	maxGitLabSegments = 20
	maxHostName       = 253
)

var (
	repositorySegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	hostLabels        = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
)

// ParseRepository reads a repository address as a browser shows it on the
// picked forge. The scheme may be left out, a trailing .git, slash or
// subpage is cut. Every refusal is one sentence.
func ParseRepository(raw, forge string) (Repository, error) {
	if forge != ForgeGitHub && forge != ForgeGitLab {
		return Repository{}, refuse("Pick GitHub or GitLab.")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Repository{}, refuse("Enter the repository address.")
	}
	if len(raw) > maxRepositoryURL || hasControl(raw) {
		return Repository{}, refuse("The address is plain text of at most %d characters.", maxRepositoryURL)
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Repository{}, refuse("That is no web address.")
	}
	if u.Scheme != "https" {
		return Repository{}, refuse("The address must use https.")
	}
	if u.User != nil {
		return Repository{}, refuse("The address carries no user name or password.")
	}
	host, err := repositoryHost(u)
	if err != nil {
		return Repository{}, err
	}
	var segments []string
	for _, s := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	switch forge {
	case ForgeGitHub:
		if len(segments) > 2 {
			segments = segments[:2]
		}
	case ForgeGitLab:
		for i, s := range segments {
			if s == "-" {
				segments = segments[:i]
				break
			}
		}
	}
	if n := len(segments); n > 0 {
		segments[n-1] = strings.TrimSuffix(segments[n-1], ".git")
	}
	if len(segments) < 2 || len(segments) > maxGitLabSegments {
		return Repository{}, refuse("Include the owner, like https://%s/owner/name.", host)
	}
	for _, s := range segments {
		if !repositorySegment.MatchString(s) || strings.Trim(s, ".") == "" {
			return Repository{}, refuse("The path has a character not allowed in a repository name.")
		}
	}
	path := strings.Join(segments, "/")
	return Repository{URL: "https://" + host + "/" + path, Forge: forge, Host: host, Path: path}, nil
}

// repositoryHost answers the lower case host of u, with its port unless 443.
// Private addresses are fine, link local ones are refused here and at every
// dial.
func repositoryHost(u *url.URL) (string, error) {
	name := strings.ToLower(u.Hostname())
	if addr, err := netip.ParseAddr(name); err == nil {
		if netguard.LinkLocal(addr) || addr.Zone() != "" {
			return "", refuse("Link local addresses are refused.")
		}
		if addr.Is6() {
			name = "[" + name + "]"
		}
	} else if len(name) > maxHostName || !hostLabels.MatchString(name) {
		return "", refuse("The address names no valid host.")
	}
	if name == "www.github.com" || name == "www.gitlab.com" {
		name = strings.TrimPrefix(name, "www.")
	}
	switch port := u.Port(); {
	case port == "" || port == "443":
		return name, nil
	case validPort(port):
		return name + ":" + port, nil
	default:
		return "", refuse("The address names no valid port.")
	}
}

func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

// Short is host and path.
func (r Repository) Short() string { return strings.TrimPrefix(r.URL, "https://") }

// ForgeName is the forge as the page names it.
func (r Repository) ForgeName() string { return forgeName(r.Forge) }

// Same answers whether two repositories are one. Both forges match paths
// case insensitively.
func (r Repository) Same(other Repository) bool {
	return r.Forge == other.Forge && strings.EqualFold(r.URL, other.URL)
}

// feed is the repository's release feed: api.github.com for github.com,
// /api/v3 on a GitHub Enterprise host, /api/v4 on any GitLab host.
func (r Repository) feed(client *http.Client) *update.Feed {
	var feedURL string
	switch {
	case r.Forge == ForgeGitLab:
		feedURL = "https://" + r.Host + "/api/v4/projects/" + url.PathEscape(r.Path) + "/releases?per_page=100"
	case r.Host == "github.com":
		feedURL = "https://api.github.com/repos/" + r.Path + "/releases?per_page=100"
	default:
		feedURL = "https://" + r.Host + "/api/v3/repos/" + r.Path + "/releases?per_page=100"
	}
	format, _ := update.ParseFeedFormat(r.Forge)
	return update.NewFeed(feedURL, format, client)
}

// Bounds of a fetch of a plugin's releases.
const (
	fetchDial      = 5 * time.Second
	fetchHandshake = 10 * time.Second
	fetchHeader    = 20 * time.Second
	feedTimeout    = 30 * time.Second
	fetchTimeout   = 5 * time.Minute
	maxRedirects   = 5
)

var errNotHTTPS = errors.New("only https addresses are fetched")

// fetchClient carries every request for a plugin's releases, feed and
// downloads. It takes https alone, refuses link local addresses at every
// dial and bounds dial, handshake and header wait. It follows at most five
// https redirects, both forges serve release files from a storage host.
func fetchClient(api string) *http.Client {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: fetchDial, Control: netguard.RefuseLinkLocal}).DialContext,
		TLSHandshakeTimeout:   fetchHandshake,
		ResponseHeaderTimeout: fetchHeader,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
	}
	guard := guardedTransport{next: transport, allowed: func(u *url.URL) bool { return u.Scheme == "https" }}
	if stub, err := url.Parse(api); api != "" && err == nil {
		guard.next = stubTransport{next: transport, stub: stub}
		guard.allowed = func(u *url.URL) bool { return u.Scheme == "https" || strings.HasPrefix(u.String(), api+"/") }
	}
	return &http.Client{
		Transport: guard,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("more than %d redirects", maxRedirects)
			}
			return nil
		},
	}
}

// guardedTransport refuses a URL the fetch may not reach, on every redirect
// too.
type guardedTransport struct {
	next    http.RoundTripper
	allowed func(*url.URL) bool
}

func (t guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.allowed(req.URL) {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, errNotHTTPS
	}
	return t.next.RoundTrip(req)
}

// stubTransport sends a dev build's https requests to the stub, see
// APIURLEnv.
type stubTransport struct {
	next http.RoundTripper
	stub *url.URL
}

func (t stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		req = req.Clone(req.Context())
		req.Host = req.URL.Host
		req.URL.Scheme, req.URL.Host = t.stub.Scheme, t.stub.Host
	}
	return t.next.RoundTrip(req)
}

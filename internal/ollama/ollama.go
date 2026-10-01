package ollama

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marein/dev-cockpit/internal/netguard"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/statefile"
)

const (
	Executable = "ollama"
	HostKey    = "ollama-host"
	ModelsKey  = "ollama-models-added"
	Prefix     = "ollama/"
	CacheFile  = "ollama-cache.json"

	SourceServer  = "server"
	SourceAdded   = "added"
	SourceCatalog = "catalog"

	answeredSleep  = 10 * time.Minute
	failedSleep    = 30 * time.Second
	catalogAge     = 10 * time.Minute
	requestTimeout = 5 * time.Second
	maxResponse    = 4 << 20
)

var ErrMissing = errors.New("The ollama executable was not found on PATH.")
var ErrServer = errors.New("The Ollama server does not answer.")

func Available() bool {
	_, err := exec.LookPath(Executable)
	return err == nil
}

func baseURL(raw string) string {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(raw), "://")
	port := map[string]string{"http": "80", "https": "443"}[scheme]
	if !ok {
		scheme, rest = "http", strings.TrimSpace(raw)
	}
	hostport, path, _ := strings.Cut(rest, "/")
	host, p, err := net.SplitHostPort(hostport)
	if err != nil {
		host, p = cmp.Or(strings.Trim(hostport, "[]"), "127.0.0.1"), cmp.Or(port, "11434")
	}
	return strings.TrimSuffix(scheme+"://"+net.JoinHostPort(host, p)+"/"+path, "/")
}

func bareName(name string) string {
	base, tag, tagged := strings.Cut(name, ":")
	if tag == "cloud" || !tagged {
		return base
	}
	return strings.TrimSuffix(name, "-cloud")
}

func cloudName(raw string) string {
	name := strings.TrimSpace(raw)
	if !strings.Contains(name, ":") {
		return name + ":cloud"
	}
	if bareName(name) != name {
		return name
	}
	return name + "-cloud"
}

type Model struct{ Name, Source string }

type snapshot struct {
	Host      string         `json:"host"`
	Names     []string       `json:"names"`
	Windows   map[string]int `json:"windows"`
	Catalog   []string       `json:"catalog"`
	CatalogAt time.Time      `json:"catalogAt"`
	Reachable bool           `json:"reachable"`
	CheckedAt time.Time      `json:"checkedAt"`
}

type Client struct {
	store      *settings.Store
	client     *http.Client
	catalogURL string
	cachePath  string
	wake       chan struct{}
	answered   chan struct{}
	once       sync.Once
	waiters    atomic.Int32

	mu   sync.Mutex
	snap snapshot
}

func New(store *settings.Store, catalogURL, stateDir string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: requestTimeout, Control: netguard.RefuseLinkLocal}).DialContext
	c := &Client{store: store, catalogURL: catalogURL, wake: make(chan struct{}, 1), answered: make(chan struct{}), client: &http.Client{
		Transport:     transport,
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	if stateDir != "" {
		c.cachePath = filepath.Join(stateDir, CacheFile)
		statefile.Load(c.cachePath, &c.snap)
	}
	return c
}

func (c *Client) Run() {
	for {
		answered := Available() && c.pass(time.Now())
		select {
		case <-c.wake:
		case <-time.After(c.sleep(answered)):
		}
	}
}

func (c *Client) sleep(answered bool) time.Duration {
	switch {
	case answered:
		return answeredSleep
	case c.waiters.Load() > 0:
		return time.Second
	}
	return failedSleep
}

func (c *Client) Wake() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Client) WaitReachable(timeout time.Duration) bool {
	c.waiters.Add(1)
	defer c.waiters.Add(-1)
	c.Wake()
	select {
	case <-c.answered:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (c *Client) pass(now time.Time) bool {
	host := c.Host()
	c.mu.Lock()
	next, added := c.snap, c.added()
	c.mu.Unlock()
	if now.Sub(next.CatalogAt) >= catalogAge {
		if catalog, err := c.fetch(c.catalogURL, false); err == nil {
			next.Catalog, next.CatalogAt = catalog, now
		}
	}
	names, err := c.fetch(baseURL(host)+"/api/tags", true)
	switch {
	case err == nil:
		windows := map[string]int{}
		if host == next.Host {
			maps.Copy(windows, next.Windows)
		}
		for _, name := range slices.Concat(names, added, next.Catalog) {
			if windows[bareName(name)] == 0 {
				windows[bareName(name)] = c.show(host, name)
			}
		}
		next.Names, next.Windows = names, windows
	case host != next.Host:
		next.Names, next.Windows = nil, nil
	}
	next.Host, next.Reachable, next.CheckedAt = host, err == nil, now
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snap = next
	if c.cachePath != "" {
		statefile.Save(c.cachePath, 0o644, next)
	}
	if next.Reachable {
		c.once.Do(func() { close(c.answered) })
	}
	return next.Reachable
}

func (c *Client) Host() string {
	if c == nil || c.store == nil {
		return ""
	}
	return strings.TrimSpace(c.store.Get(HostKey))
}

func (c *Client) SetHost(raw string) error {
	host := strings.TrimSpace(raw)
	parsed, err := url.Parse(baseURL(host))
	if err != nil || strings.ContainsAny(host, " \t") || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("The Ollama host is an address like http://127.0.0.1:11434.")
	}
	if addr, err := netip.ParseAddr(parsed.Hostname()); err == nil && netguard.LinkLocal(addr) {
		return errors.New("The Ollama host cannot be a link local address.")
	}
	if c.store != nil && host != "" {
		c.store.Set(HostKey, host)
	} else if c.store != nil {
		c.store.Delete(HostKey)
	}
	c.Wake()
	return nil
}

func (c *Client) Check() error {
	if !Available() {
		return ErrMissing
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.snap.Reachable {
		return ErrServer
	}
	return nil
}

func (c *Client) Warning() string {
	if err := c.Check(); err == ErrServer {
		return err.Error()
	}
	return ""
}

func (c *Client) Models() []Model {
	if !Available() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return appendNew(appendNew(appendNew(nil, c.snap.Names, SourceServer), c.added(), SourceAdded), c.snap.Catalog, SourceCatalog)
}

func appendNew(models []Model, names []string, source string) []Model {
	for _, name := range names {
		if !slices.ContainsFunc(models, func(m Model) bool { return m.Name == name }) {
			models = append(models, Model{Name: name, Source: source})
		}
	}
	return models
}

func (c *Client) Window(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snap.Windows[bareName(strings.TrimSpace(name))]
}

// ServedWindow is the window Ollama serves a model with, or 0 when unknown. It
// answers for cloud models only: /api/show reports the trained maximum, and a
// local model is served with num_ctx or OLLAMA_CONTEXT_LENGTH, which no API
// tells before the model is loaded. The windows are keyed by bare name, which
// a local tag shares with its cloud twin, so the name itself has to be one of
// the cloud.
func (c *Client) ServedWindow(raw string) int {
	if c == nil {
		return 0
	}
	name := strings.TrimSpace(raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	if bareName(name) == name && !slices.Contains(c.snap.Names, name) {
		return 0
	}
	return c.snap.Windows[bareName(name)]
}

func (c *Client) Add(raw string) error {
	name := strings.TrimSpace(raw)
	if name == "" {
		return errors.New("A model name is needed.")
	}
	if strings.HasPrefix(name, Prefix) {
		return errors.New("A model is added here by its Ollama name, without the ollama/ prefix.")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if names := c.added(); !c.offered(name) && !slices.Contains(names, name) {
		c.saveAdded(append(names, name))
	}
	return nil
}

func (c *Client) Delete(raw string) error {
	name := strings.TrimSpace(raw)
	if name == "" {
		return errors.New("A model name is needed.")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	names := c.added()
	if i := slices.Index(names, name); i >= 0 {
		c.saveAdded(slices.Delete(names, i, i+1))
	} else if c.offered(name) {
		return errors.New(name + " comes from Ollama and cannot be removed.")
	}
	return nil
}

func (c *Client) offered(name string) bool {
	return slices.Contains(c.snap.Names, name) || slices.Contains(c.snap.Catalog, name)
}

func (c *Client) added() []string {
	var names []string
	if c.store != nil {
		_ = json.Unmarshal([]byte(c.store.Get(ModelsKey)), &names)
	}
	return names
}

func (c *Client) saveAdded(names []string) {
	raw, _ := json.Marshal(names)
	if c.store != nil && len(names) > 0 {
		c.store.Set(ModelsKey, string(raw))
	} else if c.store != nil {
		c.store.Delete(ModelsKey)
	}
	c.Wake()
}

func (c *Client) fetch(target string, server bool) ([]string, error) {
	var tags struct {
		Models []struct {
			Name       string `json:"name"`
			RemoteHost string `json:"remote_host"`
		} `json:"models"`
	}
	res, err := c.client.Get(target)
	if err := decode(res, err, &tags); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range tags.Models {
		name := strings.TrimSpace(m.Name)
		if !server {
			name = cloudName(name)
		}
		if strings.TrimSpace(m.Name) != "" && (!server || m.RemoteHost != "") && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, nil
}

func (c *Client) show(host, name string) int {
	body, _ := json.Marshal(map[string]string{"model": name})
	var info struct {
		ModelInfo map[string]json.RawMessage `json:"model_info"`
	}
	res, err := c.client.Post(baseURL(host)+"/api/show", "application/json", bytes.NewReader(body))
	if decode(res, err, &info) != nil {
		return 0
	}
	for key, raw := range info.ModelInfo {
		var window int
		if strings.HasSuffix(key, "context_length") && json.Unmarshal(raw, &window) == nil && window > 0 {
			return window
		}
	}
	return 0
}

func decode(res *http.Response, err error, v any) error {
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New(res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, maxResponse)).Decode(v)
}

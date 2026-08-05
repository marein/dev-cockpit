package docker

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A container is reachable in two ways and only one of them is a published
// port. The other is a reverse proxy in front of it, which publishes nothing
// and routes by host name; what that host is stands in the container's own
// labels, because that is how the proxy learned it.
//
// Which label, and how to read it, is configuration and not code. Every
// ecosystem writes that down differently, and a cockpit that grows a parser
// per convention grows forever, so this file knows exactly one thing: a label
// may carry the address a container answers on. A rule says which label, a
// regular expression says where the address sits in its value, and the
// default list below covers the common case so a normal setup configures
// nothing.
//
// What is deliberately out of reach is a convention carried in an environment
// variable, nginx-proxy's VIRTUAL_HOST being the known one: the whole docker
// integration is one container list plus an event stream, and reading a
// container's environment costs an inspect per container. Labels come with
// the list, so a rule over labels costs nothing at all.
//
// The key has three states like the compose commands next to it, and only
// Lookup tells them apart: not set means DefaultLinkRules, which is never
// written at first start so a later version may improve them, set means what
// is stored, and an empty list means somebody took every rule away and wants
// the published ports alone.
const LinkRulesSettingKey = "docker-link-rules"

// LinkRule is one convention for reading addresses out of a container's
// labels.
type LinkRule struct {
	// Label is the label to read, with * standing for any run of characters,
	// so one rule covers a whole family of them (a router per host name).
	// Label keys are matched case insensitively, the way the tools that write
	// them treat their own spelling.
	Label string `json:"label"`
	// Pattern is a regular expression over the label's value with named
	// captures: host is the address, path and port are optional. Every match
	// in the value counts, not only the first, and the host capture may name
	// several addresses separated by commas. Empty means the whole value is
	// the host.
	Pattern string `json:"pattern,omitempty"`
	// Scheme pins the link to http or https. Empty is the useful answer
	// whenever the proxy in front of the app is the proxy in front of the
	// cockpit: the link then carries no scheme and is opened under the one
	// the page itself was reached over. What terminates TLS may well sit
	// above the proxy, where no label of the routed container can see it.
	Scheme string `json:"scheme,omitempty"`
	// Unless is a key=value label that switches this rule off for a
	// container, which is how an opt-out is expressed without this code
	// knowing any proxy's name for it.
	Unless string `json:"unless,omitempty"`
}

// DefaultLinkRules is the list an install that never touched the setting has.
// It carries the one convention wide enough to be worth defaulting to, the
// docker labels of traefik: a router per name, its rule naming the hosts it
// answers for, and traefik.enable=false as the opt-out. The pattern reads
// every Host(...) of a rule and, when one directly follows, the Path or
// PathPrefix the host is pinned to, so a container routed under a prefix gets
// the address that reaches it.
//
// A second entry belongs here only if it is as safe as this one. A rule that
// guesses wrong sends somebody to an address that does not exist, which is
// worse than the cockpit offering no link at all.
func DefaultLinkRules() []LinkRule {
	return []LinkRule{{
		Label:   "traefik.http.routers.*.rule",
		Pattern: `\bHost\(\s*(?P<host>[^)]+?)\s*\)(?:\s*&&\s*Path(?:Prefix)?\(\s*(?P<path>[^)]+?)\s*\))?`,
		Unless:  "traefik.enable=false",
	}}
}

// IsDefaultLinkRules reports whether a list is exactly the default one.
// Saving that says nothing the absent key does not already say, so the caller
// stores nothing and leaves the key absent.
func IsDefaultLinkRules(list []LinkRule) bool {
	return reflect.DeepEqual(list, DefaultLinkRules())
}

// LinkRules turns what the settings store answered into the rules in force.
// Pass both return values of Lookup. Something stored that cannot be read at
// all is treated like never set, so a damaged value leaves a working cockpit.
func LinkRules(raw string, set bool) []LinkRule {
	if !set {
		return DefaultLinkRules()
	}
	list, err := DecodeLinkRules(raw)
	if err != nil {
		return DefaultLinkRules()
	}
	return list
}

// DecodeLinkRules reads the stored JSON. An empty value is an empty list, not
// an error: that is what removing every rule leaves behind.
func DecodeLinkRules(raw string) ([]LinkRule, error) {
	if strings.TrimSpace(raw) == "" {
		return []LinkRule{}, nil
	}
	var list []LinkRule
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []LinkRule{}
	}
	return list, nil
}

// EncodeLinkRules writes the list back the way the store keeps it.
func EncodeLinkRules(list []LinkRule) string {
	if list == nil {
		list = []LinkRule{}
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// LinkSchemes is what a rule may pin its links to. The empty one comes first
// because it is the usual answer.
var LinkSchemes = []string{"", "http", "https"}

// Validate answers what makes a rule unusable, which is what the row it is
// edited in says under the field. A rule that does not validate is also the
// rule the matcher skips, so a page never breaks on one.
func (r LinkRule) Validate() error {
	if strings.TrimSpace(r.Label) == "" {
		return fmt.Errorf("the rule needs a label to read")
	}
	if _, err := r.compile(); err != nil {
		return err
	}
	switch r.Scheme {
	case "", "http", "https":
	default:
		return fmt.Errorf("the scheme must be empty, http or https")
	}
	if r.Unless != "" && !strings.Contains(r.Unless, "=") {
		return fmt.Errorf("the opt-out must read label=value")
	}
	return nil
}

// compile prepares the pattern. No pattern is the whole value as one host.
func (r LinkRule) compile() (*regexp.Regexp, error) {
	if strings.TrimSpace(r.Pattern) == "" {
		return nil, nil
	}
	re, err := regexp.Compile(r.Pattern)
	if err != nil {
		// The compiler's own message names the position and the reason, but
		// says "error parsing regexp" first, which reads like a crash report.
		return nil, fmt.Errorf("the pattern does not read as a regular expression: %s", trimRegexpError(err))
	}
	return re, nil
}

func trimRegexpError(err error) string {
	return strings.TrimPrefix(err.Error(), "error parsing regexp: ")
}

// LinkMatcher is the configured rules ready to be applied, compiled once and
// then asked per container: a page has many containers and a rule is a
// regular expression. Rules that do not validate are left out here, so
// nothing downstream has to think about them.
type LinkMatcher struct {
	rules []compiledRule
}

type compiledRule struct {
	rule LinkRule
	re   *regexp.Regexp
	host int
	path int
	port int
}

// NewLinkMatcher compiles what is configured, in the order it is configured.
func NewLinkMatcher(rules []LinkRule) LinkMatcher {
	out := LinkMatcher{}
	for _, rule := range rules {
		if rule.Validate() != nil {
			continue
		}
		re, _ := rule.compile()
		entry := compiledRule{rule: rule, re: re, host: -1, path: -1, port: -1}
		if re != nil {
			entry.host = re.SubexpIndex("host")
			entry.path = re.SubexpIndex("path")
			entry.port = re.SubexpIndex("port")
		}
		out.rules = append(out.rules, entry)
	}
	return out
}

// Routes answers the addresses the rules read out of one container's labels,
// deduplicated and in a stable order. It is what the container chips, the
// menus and the preview on the settings page all go through, so what a rule
// promises there is what it does everywhere.
func (m LinkMatcher) Routes(labels map[string]string) []Link {
	var out []Link
	at := map[string]int{}
	for _, rule := range m.rules {
		for _, link := range rule.links(labels) {
			// The same address twice is one entry: several host names on one
			// router are several addresses, but a plain router and a TLS one for
			// the same host are the one address people mean, and the entry
			// that pins https is the stricter claim of the two.
			key := link.Host + link.Path
			if index, seen := at[key]; seen {
				if out[index].Scheme == "" && link.Scheme != "" {
					out[index].Scheme = link.Scheme
				}
				continue
			}
			at[key] = len(out)
			out = append(out, link)
		}
	}
	return out
}

// Links answers every address a browser can open for one container: the
// routes its labels declare first, because where a route exists it is the
// address a person wants, then the published tcp ports, which are docker's
// own truth and never a rule.
func (m LinkMatcher) Links(c Container) []Link {
	out := m.Routes(c.Labels)
	for _, port := range c.Ports {
		if port.Proto != "" {
			continue
		}
		scheme := "http"
		if port.Private == 443 {
			scheme = "https"
		}
		out = append(out, Link{Scheme: scheme, Port: port.Public})
	}
	return out
}

// links applies one rule to one container's labels.
func (c compiledRule) links(labels map[string]string) []Link {
	if c.disabled(labels) {
		return nil
	}
	var keys []string
	for key := range labels {
		if labelMatch(c.rule.Label, key) {
			keys = append(keys, key)
		}
	}
	// A map has no order and a menu must not reshuffle between two renders.
	sort.Strings(keys)
	var out []Link
	for _, key := range keys {
		out = append(out, c.fromValue(labels[key])...)
	}
	return out
}

// disabled reads the rule's opt-out against this container's labels.
func (c compiledRule) disabled(labels map[string]string) bool {
	key, want, ok := strings.Cut(c.rule.Unless, "=")
	if !ok {
		return false
	}
	key, want = strings.TrimSpace(key), strings.TrimSpace(want)
	for label, value := range labels {
		if labelMatch(key, label) && strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

// fromValue reads one label value: every match of the pattern yields the
// addresses its host capture names.
func (c compiledRule) fromValue(value string) []Link {
	if c.re == nil {
		return c.linksFor(value, "", "")
	}
	var out []Link
	for _, match := range c.re.FindAllStringSubmatch(value, -1) {
		// A pattern that names no host capture puts the whole match forward,
		// which is what a rule for a label that is nothing but the address
		// looks like once it has been narrowed by a pattern.
		hosts := match[0]
		if c.host > 0 && c.host < len(match) {
			hosts = match[c.host]
		}
		out = append(out, c.linksFor(hosts, group(match, c.path), group(match, c.port))...)
	}
	return out
}

func group(match []string, index int) string {
	if index > 0 && index < len(match) {
		return match[index]
	}
	return ""
}

// linksFor turns one match into addresses. The host capture may name several,
// separated by commas and written in whatever quotes the convention uses,
// because that is how a proxy writes a host list.
func (c compiledRule) linksFor(hosts, path, port string) []Link {
	path = unquote(path)
	if !isPath(path) {
		path = ""
	}
	number := 0
	if digits := unquote(port); digits != "" {
		if parsed, err := strconv.Atoi(digits); err == nil && parsed > 0 && parsed < 65536 {
			number = parsed
		}
	}
	var out []Link
	for _, host := range strings.Split(hosts, ",") {
		host = unquote(host)
		if !isHostName(host) {
			continue
		}
		out = append(out, Link{Scheme: c.rule.Scheme, Host: host, Port: number, Path: path})
	}
	return out
}

// unquote takes the spaces and the quotes off a captured value. Backticks are
// what traefik writes, single and double quotes are what a compose file
// written by hand does.
func unquote(value string) string {
	value = strings.TrimSpace(value)
	for len(value) >= 2 {
		quote := value[0]
		if (quote == '`' || quote == '\'' || quote == '"') && value[len(value)-1] == quote {
			value = strings.TrimSpace(value[1 : len(value)-1])
			continue
		}
		break
	}
	return value
}

// labelMatch reports whether a label key is what a rule asks for. A rule
// names one label or a family of them, * standing for any run of characters.
func labelMatch(pattern, key string) bool {
	pattern, key = strings.ToLower(strings.TrimSpace(pattern)), strings.ToLower(key)
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == key
	}
	if !strings.HasPrefix(key, parts[0]) {
		return false
	}
	key = key[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		found := strings.Index(key, part)
		if found < 0 {
			return false
		}
		key = key[found+len(part):]
	}
	last := parts[len(parts)-1]
	return len(key) >= len(last) && strings.HasSuffix(key, last)
}

// isHostName reports whether a value can be a host in a URL. It is also what
// keeps a pattern out of a link: a matcher that reads
// Host(`{sub:[a-z]+}.example.com`) captures something that is not an address,
// and an address is the only thing worth offering.
func isHostName(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if !isHostByte(value[i]) {
			return false
		}
	}
	return true
}

func isHostByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' ||
		b == '.' || b == '-' || b == '_'
}

// isPath reports whether a value can be pasted behind a host: an absolute
// path and nothing that would end the attribute or the URL it is written
// into.
func isPath(value string) bool {
	if !strings.HasPrefix(value, "/") {
		return false
	}
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"', '\'', '`', '<', '>', '\\':
			return false
		}
		if value[i] <= ' ' {
			return false
		}
	}
	return true
}

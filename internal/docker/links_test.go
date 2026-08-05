package docker

import "testing"

// route writes an address the way these tests read best.
func route(scheme, host, path string) Link {
	return Link{Scheme: scheme, Host: host, Path: path}
}

func same(got, want []Link) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// defaultMatcher is what an install that configured nothing reads its
// container addresses with.
func defaultMatcher() LinkMatcher { return NewLinkMatcher(DefaultLinkRules()) }

// The default rule is the traefik docker labels, so this table is also the
// documentation of what a stock setup gets.
func TestDefaultRuleReadsTheTraefikLabels(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   []Link
	}{
		{
			name: "one router, one host, no scheme of its own",
			labels: map[string]string{
				"traefik.enable":                                     "true",
				"traefik.http.routers.app.rule":                      "Host(`app.example.com`)",
				"traefik.http.routers.app.entrypoints":               "web",
				"traefik.http.services.app.loadbalancer.server.port": "8080",
			},
			want: []Link{route("", "app.example.com", "")},
		},
		{
			name: "several hosts in one matcher and several matchers",
			labels: map[string]string{
				"traefik.http.routers.a.rule": "Host(`a.example.com`, `b.example.com`) || Host(`c.example.com`)",
			},
			want: []Link{
				route("", "a.example.com", ""),
				route("", "b.example.com", ""),
				route("", "c.example.com", ""),
			},
		},
		{
			name: "single and double quotes are read like backticks",
			labels: map[string]string{
				"traefik.http.routers.a.rule": "Host('a.example.com')",
				"traefik.http.routers.b.rule": `Host("b.example.com")`,
			},
			want: []Link{
				route("", "a.example.com", ""),
				route("", "b.example.com", ""),
			},
		},
		{
			name: "patterns are not addresses",
			labels: map[string]string{
				"traefik.http.routers.a.rule": "HostRegexp(`^.+\\.example\\.com$`)",
				"traefik.http.routers.b.rule": "Host(`{sub:[a-z]+}.example.com`)",
			},
			want: nil,
		},
		{
			name: "a tcp router matches a handshake, not a URL",
			labels: map[string]string{
				"traefik.tcp.routers.a.rule": "HostSNI(`a.example.com`)",
			},
			want: nil,
		},
		{
			name: "a matcher is a name of its own, not a suffix",
			labels: map[string]string{
				"traefik.http.routers.a.rule": "MyHost(`a.example.com`) || Host(`b.example.com`)",
			},
			want: []Link{route("", "b.example.com", "")},
		},
		{
			name: "the opt-out label switches the rule off",
			labels: map[string]string{
				"traefik.enable":              "false",
				"traefik.http.routers.a.rule": "Host(`a.example.com`)",
			},
			want: nil,
		},
		{
			name: "one path next to the host travels with it",
			labels: map[string]string{
				"traefik.http.routers.a.rule": "Host(`a.example.com`) && PathPrefix(`/admin`)",
				"traefik.http.routers.b.rule": "Host(`b.example.com`) && Path(`/health`)",
			},
			want: []Link{
				route("", "a.example.com", "/admin"),
				route("", "b.example.com", "/health"),
			},
		},
		{
			name: "a set of paths is not one address, the host stands alone",
			labels: map[string]string{
				"traefik.http.routers.a.rule": "Host(`a.example.com`) && (PathPrefix(`/one`) || PathPrefix(`/two`))",
			},
			want: []Link{route("", "a.example.com", "")},
		},
		{
			name: "the same address twice is one entry",
			labels: map[string]string{
				"traefik.http.routers.plain.rule": "Host(`a.example.com`)",
				"traefik.http.routers.tls.rule":   "Host(`a.example.com`)",
			},
			want: []Link{route("", "a.example.com", "")},
		},
		{
			name: "one host under two paths stays two addresses",
			labels: map[string]string{
				"traefik.http.routers.a.rule": "Host(`a.example.com`) && PathPrefix(`/one`)",
				"traefik.http.routers.b.rule": "Host(`a.example.com`) && PathPrefix(`/two`)",
			},
			want: []Link{
				route("", "a.example.com", "/one"),
				route("", "a.example.com", "/two"),
			},
		},
		{
			name:   "a container nobody routes answers none",
			labels: map[string]string{"com.docker.compose.service": "db"},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultMatcher().Routes(tc.labels); !same(got, tc.want) {
				t.Fatalf("the default rule answered %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The proxy's inner side looks like an answer and is not: which port and
// which scheme it reaches the container on inside its own network is nothing
// a browser can use, and neither label is read by the default rule.
func TestTheProxysInnerSideNeverReachesALink(t *testing.T) {
	labels := map[string]string{
		"traefik.http.routers.a.rule":                        "Host(`a.example.com`)",
		"traefik.http.services.a.loadbalancer.server.port":   "8080",
		"traefik.http.services.a.loadbalancer.server.scheme": "h2c",
	}
	got := defaultMatcher().Routes(labels)
	if !same(got, []Link{route("", "a.example.com", "")}) {
		t.Fatalf("answered %+v, want the host alone", got)
	}
}

// A router map has no order of its own, and a menu that reshuffles between
// two renders is a menu nobody can aim at.
func TestRoutesAreStablyOrdered(t *testing.T) {
	labels := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		labels["traefik.http.routers.app-"+name+".rule"] = "Host(`app-" + name + ".example.com`)"
	}
	first := defaultMatcher().Routes(labels)
	if len(first) != 9 {
		t.Fatalf("expected nine addresses, got %+v", first)
	}
	for i := 0; i < 5; i++ {
		if got := defaultMatcher().Routes(labels); !same(got, first) {
			t.Fatalf("run %d answered %+v, want %+v", i, got, first)
		}
	}
}

// The shape a proxied stack has: one container routed under several hosts
// that also publishes a port of its own, and one that publishes nothing at
// all and is reachable all the same.
func TestDefaultRuleReadsAProxiedStack(t *testing.T) {
	web := Container{
		Ports: []Port{{Public: 8443, Private: 443}},
		Labels: map[string]string{
			"traefik.docker.network":                                    "proxy",
			"traefik.enable":                                            "true",
			"traefik.http.routers.app-one.entrypoints":                  "web",
			"traefik.http.routers.app-one.rule":                         "Host(`app-one.example.com`)",
			"traefik.http.routers.app-two.entrypoints":                  "web",
			"traefik.http.routers.app-two.rule":                         "Host(`app-two.example.com`)",
			"traefik.http.routers.internal.rule":                        "Host(`internal.example.com`)",
			"traefik.http.services.internal.loadbalancer.server.port":   "80",
			"traefik.http.services.internal.loadbalancer.server.scheme": "h2c",
			"traefik.http.services.web.loadbalancer.server.port":        "80",
		},
	}
	want := []Link{
		route("", "app-one.example.com", ""),
		route("", "app-two.example.com", ""),
		route("", "internal.example.com", ""),
		{Scheme: "https", Port: 8443},
	}
	if got := defaultMatcher().Links(web); !same(got, want) {
		t.Fatalf("the routed container answered %+v, want %+v", got, want)
	}
	// This one has no port to offer, so without its label it would offer
	// nothing, and the port its service label names is the proxy's own.
	admin := Container{Labels: map[string]string{
		"traefik.enable":                                       "true",
		"traefik.http.routers.admin.rule":                      "Host(`admin.example.com`)",
		"traefik.http.services.admin.loadbalancer.server.port": "8080",
	}}
	got := defaultMatcher().Links(admin)
	if !same(got, []Link{route("", "admin.example.com", "")}) {
		t.Fatalf("the unpublished container answered %+v, want its host and no port", got)
	}
}

// The rules first, the ports after: where a route exists it is the address a
// person wants. The ports are docker's own truth and no rule reaches them,
// not even an empty rule list.
func TestLinksMergeRoutesBeforePorts(t *testing.T) {
	container := Container{
		Ports: []Port{
			{Public: 8080, Private: 80},
			{Public: 8443, Private: 443},
			{Public: 5000, Private: 5000, Proto: "udp"},
		},
		Labels: map[string]string{"traefik.http.routers.a.rule": "Host(`a.example.com`)"},
	}
	want := []Link{
		route("", "a.example.com", ""),
		{Scheme: "http", Port: 8080},
		{Scheme: "https", Port: 8443},
	}
	if got := defaultMatcher().Links(container); !same(got, want) {
		t.Fatalf("links answered %+v, want %+v", got, want)
	}
	if got := NewLinkMatcher(nil).Links(container); !same(got, want[1:]) {
		t.Fatalf("without a single rule the links answered %+v, want the ports alone", got)
	}
}

// A rule is configuration, so the mechanism has to work for a convention this
// code has never heard of: one label carrying nothing but the address, one
// carrying a whole URL, a scheme pinned by the rule, a port captured out of
// the value.
func TestARuleReadsAnyConvention(t *testing.T) {
	labels := map[string]string{
		"com.example.vhost":      "app.example.com, www.example.com",
		"com.example.public-url": "https://app.example.com:8443/console",
		"com.example.off":        "yes",
	}
	plain := NewLinkMatcher([]LinkRule{{Label: "com.example.vhost"}})
	want := []Link{route("", "app.example.com", ""), route("", "www.example.com", "")}
	if got := plain.Routes(labels); !same(got, want) {
		t.Fatalf("a rule without a pattern answered %+v, want %+v", got, want)
	}
	full := NewLinkMatcher([]LinkRule{{
		Label:   "com.example.public-url",
		Pattern: `https://(?P<host>[^:/]+):(?P<port>\d+)(?P<path>/\S*)`,
		Scheme:  "https",
	}})
	if got := full.Routes(labels); !same(got, []Link{{Scheme: "https", Host: "app.example.com", Port: 8443, Path: "/console"}}) {
		t.Fatalf("a rule with a port and a path answered %+v", got)
	}
	off := NewLinkMatcher([]LinkRule{{Label: "com.example.vhost", Unless: "com.example.off=yes"}})
	if got := off.Routes(labels); got != nil {
		t.Fatalf("the opt-out did not switch the rule off, answered %+v", got)
	}
}

// The label may name a family, which is what makes one rule cover a router
// per host name.
func TestLabelWildcards(t *testing.T) {
	labels := map[string]string{
		"proxy.route.one.host": "one.example.com",
		"proxy.route.two.host": "two.example.com",
		"proxy.route.two.port": "8080",
		"other.host":           "nope.example.com",
	}
	got := NewLinkMatcher([]LinkRule{{Label: "proxy.route.*.host"}}).Routes(labels)
	want := []Link{route("", "one.example.com", ""), route("", "two.example.com", "")}
	if !same(got, want) {
		t.Fatalf("the wildcard answered %+v, want %+v", got, want)
	}
	if got := NewLinkMatcher([]LinkRule{{Label: "PROXY.route.one.HOST"}}).Routes(labels); len(got) != 1 {
		t.Fatalf("label matching is case sensitive, answered %+v", got)
	}
}

// A scheme a rule pins is the stricter claim: the same address read twice
// keeps it, whichever of the two came first.
func TestAPinnedSchemeSurvivesTheDeduplication(t *testing.T) {
	labels := map[string]string{
		"plain.host":  "a.example.com",
		"secure.host": "a.example.com",
	}
	rules := []LinkRule{{Label: "plain.host"}, {Label: "secure.host", Scheme: "https"}}
	if got := NewLinkMatcher(rules).Routes(labels); !same(got, []Link{route("https", "a.example.com", "")}) {
		t.Fatalf("answered %+v, want the one address as https", got)
	}
}

// A rule nobody can use must never break a page: it is skipped where the
// links are built and reported where it is edited.
func TestAnUnusableRuleIsSkippedAndReported(t *testing.T) {
	broken := LinkRule{Label: "com.example.vhost", Pattern: "(?P<host>"}
	if err := broken.Validate(); err == nil {
		t.Fatal("an unclosed group validated")
	}
	labels := map[string]string{"com.example.vhost": "a.example.com", "com.example.other": "b.example.com"}
	matcher := NewLinkMatcher([]LinkRule{broken, {Label: "com.example.other"}})
	if got := matcher.Routes(labels); !same(got, []Link{route("", "b.example.com", "")}) {
		t.Fatalf("answered %+v, want the working rule's address alone", got)
	}
	for _, rule := range []LinkRule{
		{Label: "", Pattern: ""},
		{Label: "a", Scheme: "ftp"},
		{Label: "a", Unless: "traefik.enable"},
	} {
		if err := rule.Validate(); err == nil {
			t.Fatalf("%+v validated", rule)
		}
	}
	if err := (LinkRule{Label: "a"}).Validate(); err != nil {
		t.Fatalf("a rule that only names a label answered %v", err)
	}
}

// The three states of the setting, the same ones the compose commands have:
// absent is the defaults, stored is what is stored, and an emptied list stays
// empty, which leaves the published ports alone.
func TestLinkRulesSettingHasThreeStates(t *testing.T) {
	if got := LinkRules("", false); !IsDefaultLinkRules(got) {
		t.Fatalf("an unset key answered %+v", got)
	}
	if got := LinkRules("[]", true); len(got) != 0 {
		t.Fatalf("an emptied list answered %+v", got)
	}
	own := []LinkRule{{Label: "com.example.vhost", Scheme: "https"}}
	raw := EncodeLinkRules(own)
	got := LinkRules(raw, true)
	if len(got) != 1 || got[0] != own[0] {
		t.Fatalf("the stored list reads back as %+v", got)
	}
	if IsDefaultLinkRules(got) {
		t.Fatal("an own list reads as the default one")
	}
	// Something damaged leaves a working cockpit rather than no links at all.
	if got := LinkRules("{not json", true); !IsDefaultLinkRules(got) {
		t.Fatalf("a damaged value answered %+v", got)
	}
}

// Address is what the settings preview shows, and it says a scheme only where
// the rule pins one.
func TestLinkAddress(t *testing.T) {
	for _, tc := range []struct {
		link Link
		want string
	}{
		{Link{Host: "a.example.com"}, "a.example.com"},
		{Link{Host: "a.example.com", Path: "/admin"}, "a.example.com/admin"},
		{Link{Scheme: "https", Host: "a.example.com"}, "https://a.example.com"},
		{Link{Scheme: "https", Host: "a.example.com", Port: 8443}, "https://a.example.com:8443"},
		{Link{Scheme: "http", Port: 18088}, ":18088"},
	} {
		if got := tc.link.Address(); got != tc.want {
			t.Fatalf("%+v reads as %q, want %q", tc.link, got, tc.want)
		}
	}
}

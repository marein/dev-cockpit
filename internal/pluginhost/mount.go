package pluginhost

import (
	"fmt"
	"html"
	"html/template"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// basePlaceholder in an external plugin's markup becomes its URL subtree,
// what Routes.Path gives a compiled plugin.
const basePlaceholder = "{{base}}"

// readMarkup reads a markup file beneath the plugin directory, fills in the
// base, then parses and renders it as an HTML fragment, so an unclosed or
// extra end tag cannot break the page around it. A plaintext element, which
// no end tag closes, is dropped. It runs at load, a render never reads the
// file.
func readMarkup(root *os.Root, name, base string) (string, error) {
	info, err := root.Stat(name)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("The markup %s is missing, not a file or leaves the plugin directory.", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return "", fmt.Errorf("The markup %s could not be opened.", name)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxMarkupBytes+1))
	if err != nil || len(raw) > maxMarkupBytes || !utf8.Valid(raw) {
		return "", fmt.Errorf("The markup %s is unreadable, larger than 64 KiB or not UTF-8.", name)
	}
	place := &nethtml.Node{Type: nethtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := nethtml.ParseFragment(strings.NewReader(strings.ReplaceAll(string(raw), basePlaceholder, base)), place)
	if err != nil {
		return "", fmt.Errorf("The markup %s could not be read as HTML.", name)
	}
	for _, n := range nodes {
		place.AppendChild(n)
	}
	dropPlaintext(place)
	var b strings.Builder
	for n := place.FirstChild; n != nil; n = n.NextSibling {
		if err := nethtml.Render(&b, n); err != nil {
			return "", fmt.Errorf("The markup %s could not be read as HTML.", name)
		}
	}
	return b.String(), nil
}

func dropPlaintext(n *nethtml.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == nethtml.ElementNode && c.DataAtom == atom.Plaintext {
			n.RemoveChild(c)
		} else {
			dropPlaintext(c)
		}
		c = next
	}
}

// MountView is a live plugin's markup for a live region. Key names the
// plugin load, so the region keeps a mount it already holds.
type MountView struct {
	Key  string
	HTML template.HTML
}

// mountHTML wraps a live plugin's markup in the div a live region keeps or
// swaps as one (display: contents). data-region-key names the plugin load,
// data-dc-module the starter module the region imports, since a live
// plugin's elements are not in the import map. An external plugin's markup
// was parsed and rendered again at load, a compiled plugin's goes in as
// written.
func mountHTML(s *Serve, markup, module string) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="dc-plugin-mount" data-region-key="` + html.EscapeString(s.Key()) + `"`)
	if module != "" {
		b.WriteString(` data-dc-module="` + html.EscapeString(module) + `"`)
	}
	b.WriteString(">" + markup + "</div>")
	return template.HTML(b.String())
}

// starter answers the hashed URL of a plugin's starter module, "" for a
// plugin without elements.
func starter(s *Serve, resolve func(string) string) string {
	if len(s.CustomElements()) == 0 {
		return ""
	}
	return resolve(StarterPath(s))
}

// SlotMounts answers one mount per live plugin with markup for slot, in
// plugin order. A plugin that does not answer, crashed or starting, shows
// nothing. resolve maps a raw asset path to its hashed URL.
func SlotMounts(serves []*Serve, slot string, resolve func(string) string) []MountView {
	var views []MountView
	for _, s := range serves {
		markup := s.SlotHTML(slot)
		if !s.Live() || !s.Running() || markup == "" {
			continue
		}
		views = append(views, MountView{Key: s.Key(), HTML: mountHTML(s, markup, starter(s, resolve))})
	}
	return views
}

// PageMountView answers a live plugin's page body as a mount, false while
// the plugin does not answer or for a Serve fixed at serve start, whose page
// body renders as is.
func PageMountView(s *Serve, resolve func(string) string) (MountView, bool) {
	page := s.Page()
	if page == nil || !s.Live() || !s.Running() {
		return MountView{}, false
	}
	return MountView{Key: s.Key(), HTML: mountHTML(s, page.HTML, starter(s, resolve))}, true
}

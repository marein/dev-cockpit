// Package pluginhost is the cockpit's side of the plugin package: the
// public package is contract only, and the implementations behind its
// interfaces live here, construction, sealing and collection included. The
// helpers below read the sealed Serves the plugins filled at ConfigureServe
// and hand each surface exactly the view it renders.
package pluginhost

import (
	"fmt"
	"html/template"
	"strings"
)

// ServesHTTP reports whether the plugin's subtree answers anything, which is
// what decides whether the router registers it at all.
func ServesHTTP(s *Serve) bool {
	return s.Handler() != nil || s.Assets() != nil || len(s.CustomElements()) > 0
}

// AssetPrefix is the manifest path prefix of one plugin's added assets,
// without the leading slash, the way the asset manifest names files.
func AssetPrefix(s *Serve) string {
	return "plugins/" + s.ID() + "/assets/"
}

// StarterPath is the raw URL of the cockpit's generated starter module for
// one plugin, the module the import map binds the plugin's element names to.
// The path is reserved inside the plugin's subtree.
func StarterPath(s *Serve) string {
	return "/plugins/" + s.ID() + "/elements.js"
}

// Starter generates the starter module for one plugin: it imports the class
// module of every added element through resolve, which answers the hashed
// URL of a raw asset path, and defines each element under its final name
// with a get guard. False when the plugin added no elements.
func Starter(s *Serve, resolve func(string) string) (string, bool) {
	elements := s.CustomElements()
	if len(elements) == 0 {
		return "", false
	}
	var b strings.Builder
	imported := map[string]int{}
	for _, element := range elements {
		module := "/" + AssetPrefix(s) + element.Module
		if _, ok := imported[module]; !ok {
			imported[module] = len(imported)
			fmt.Fprintf(&b, "import C%d from %q;\n", imported[module], resolve(module))
		}
	}
	for _, element := range elements {
		module := "/" + AssetPrefix(s) + element.Module
		fmt.Fprintf(&b, "if (!customElements.get(%q)) customElements.define(%q, C%d);\n", element.Name, element.Name, imported[module])
	}
	return b.String(), true
}

// Element is one final element name with the URL of the starter module that
// defines it, ready for the import map: the lazy loader imports unknown
// element names, so binding the name to the starter is what loads a plugin's
// code the first time one of its elements is on a page.
type Element struct {
	Name string
	URL  string
}

// Elements answers every added element across the Serves, in wiring order,
// each bound to its plugin's starter through resolve, which answers the
// hashed URL of a raw asset path.
func Elements(serves []*Serve, resolve func(string) string) []Element {
	var elements []Element
	for _, s := range serves {
		if len(s.CustomElements()) == 0 {
			continue
		}
		starter := resolve(StarterPath(s))
		for _, element := range s.CustomElements() {
			elements = append(elements, Element{Name: element.Name, URL: starter})
		}
	}
	return elements
}

// SlotHTML concatenates what every plugin added for the named slot. The
// markup is compiled in plugin code and deliberately not escaped, that is
// the whole point of a slot.
func SlotHTML(serves []*Serve, slot string) template.HTML {
	var b strings.Builder
	for _, s := range serves {
		b.WriteString(s.SlotHTML(slot))
	}
	return template.HTML(b.String())
}

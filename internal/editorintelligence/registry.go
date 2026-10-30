// Package editorintelligence provides code navigation for the project
// editor: go to definition and find usages answered by language servers over
// stdio JSON-RPC. The servers run server side, the browser only exchanges
// bounded document snapshots and locations.
package editorintelligence

import (
	"path"
	"sort"
	"strings"
)

// Profile is one fixed language server profile compiled into the binary. The
// command, and the container recipe of the Docker option, are never
// configurable, so no setting can become a command execution surface; a
// setting only picks which of the fixed ways runs, or none.
type Profile struct {
	// ID is the stable profile identifier, used by the settings key.
	ID string
	// Label names the language on the settings page and in the indexing
	// indicator.
	Label string
	// Command is the server's argv, run inside the profile's container
	// over the Docker option.
	Command []string
	// Server is the short name this server wears everywhere the cockpit
	// names it itself: the image, the container, the per project cache
	// directory and the stored settings value. It is deliberately its own
	// field and not the command's leading token, which is a program name
	// and may be long enough to eat the room a container name leaves for
	// the project.
	Server string
	// Marker is the file at a project root whose presence already says
	// the project holds this language, so the warm skips the tree walk.
	Marker string
	// SilentStart marks a server that announces no work when it starts
	// because it has none: it has the workspace ready by the time it
	// answers the first request, so a lookup is never held back waiting
	// for an announcement that is not coming. The servers that index the
	// workspace at the handshake announce that run seconds late, and their
	// silence until then has to be waited out, which is the zero value
	// here: a profile that says nothing keeps the careful behaviour.
	SilentStart bool
	// ListsLate marks a server that lists the workspace's files after the
	// handshake, from a timer and unannounced, and answers meanwhile out of
	// what it has listed: a usages list in that stretch holds the
	// declaration alone and reads like the whole. It analyzes an open
	// document only once the listing is complete, so its first diagnostics
	// are the sign the workspace stands, and a lookup, which opens its
	// document first, waits for them.
	ListsLate bool
	// Settings answers the server's workspace/configuration requests, one
	// value per section. A section it does not hold, and every section of a
	// profile without settings, is answered null: the server's own default.
	Settings map[string]any
	// ClassName reads the fully qualified name of the class a hover answer
	// describes, empty for any other symbol. Nil for a language without
	// such names, whose editor surface then offers none.
	ClassName func(hover string) string
	// TypeOf reads one workspace symbol of the language: the type it
	// declares, with the word the palette shows for its kind and the place
	// its name stands, ok false for any other symbol. line answers a line of
	// the symbol's file by its 1-based number, read only when asked for and
	// false when it cannot be read: it is where a server that files several
	// things under one kind is told apart, and where the name is found when
	// the server's range starts before it. Nil for a language whose types
	// the palette does not list.
	TypeOf func(sym Symbol, line func(n int) (string, bool)) (Declared, bool)
	// TypeAsks are the workspace symbol requests a type search sends for
	// each of the words symbolWords reads off its query. A server's limit
	// takes the symbols of every kind, of the project and of its libraries,
	// together, so a server whose libraries would crowd the project's own
	// types out of it, or that leaves the libraries out unless asked, gets
	// one request for each. root is the project's path. Nil asks the word
	// alone.
	TypeAsks func(root, query string) []symbolAsk
	// Crowded plans what a type search adds for the words whose first
	// request filled the server's limit, for a server that then may have
	// left the project's own types out of it, walking the project's own
	// files of the language with what files keeps of them. complete are the
	// query's other words, whose answers hold every symbol named like them.
	// Nil for a server whose own types the requests of TypeAsks keep.
	Crowded func(files *ownFiles, walk ownWalk, words, complete []string) crowdedPlan
	// TypeHierarchy marks a server whose type hierarchy names the types
	// implementing an interface, which the usages of an interface's
	// declaration then list apart.
	TypeHierarchy bool
	// Heritage marks a server whose implementation request names the types
	// below the one declared at a position, which the usages of an
	// interface's or a class's declaration then list apart. Its answer holds
	// more than types, and the language's rule reads which are. Nil for a
	// server without one.
	Heritage *heritage
	// Clauses marks a server without an implementation request whose
	// references of a type include the names in the heritage clauses of the
	// types below it, which the usages of an interface's or a class's
	// declaration then list apart. The language's rule reads which
	// references stand there. Nil for a server without them.
	Clauses *clauses
	// SymbolLimit is the most symbols the server answers one workspace
	// symbol request with, of every kind together; an answer that long was
	// cut by the server and not by what matches. Zero for no known limit.
	SymbolLimit int
	// QueryWords marks a server that matches every word of a workspace
	// symbol query on its own against the package qualified name, so it is
	// asked the whole query. Every other server reads the query as one word
	// against the name, a space included, and is asked each word apart; the
	// quick open rule weighs the others.
	QueryWords bool
	// LibraryFiles are the extensions of the own files a server needs before
	// it names any library's symbol: a project without one, outside the
	// folders dependencies are installed in, is not sent the asks TypeAsks
	// marks as the libraries'.
	LibraryFiles []string
	// Qualified marks a server that matches a workspace symbol query holding
	// backslashes against the qualified names, its parts in their order, and
	// then names a symbol by its qualified name, see profileTypes.
	Qualified bool
	// LoadsByDocument marks a server that loads a project only for an open
	// document of it: until one is open, a request about the whole
	// workspace finds nothing.
	LoadsByDocument bool
	// Dependencies are the names of the folders the language's package
	// managers install into, at any depth. A server indexes them beside the
	// project, and what is declared below one, in whichever language, is not
	// the project's own.
	Dependencies []string
	// languageIDs maps the owned file extensions (lowercase, without dot)
	// to the LSP language identifier sent in didOpen.
	languageIDs map[string]string
	// container is how the Docker option runs this server.
	container container
}

// profiles is the fixed registry, ordered for stable rendering. Deliberately
// only the languages the navigation is verified against; a profile joins the
// list with that verification, not before.
var profiles = []*Profile{
	{
		ID:      "go",
		Label:   "Go",
		Command: []string{"gopls"},
		Server:  "gopls",
		Marker:  "go.mod",
		// The type search lists the types of the dependencies and the
		// standard library beside the project's own, which gopls names
		// only in this scope.
		Settings: map[string]any{
			"gopls": map[string]any{"symbolScope": "all"},
		},
		TypeOf:        goTypeOf,
		TypeAsks:      goTypeAsks,
		TypeHierarchy: true,
		SymbolLimit:   100,
		QueryWords:    true,
		languageIDs:   map[string]string{"go": "go"},
		container: container{
			Image:      "dev-cockpit-gopls",
			Dockerfile: goplsDockerfile + entrypointDockerfile,
			// The module downloads are what a jump into a dependency lands
			// in, so they lie in the cache directory and are readable from
			// the host under the very path the server names them by. The
			// file cache would die with the container under its default
			// XDG_CACHE_HOME and sits beside them, deliberately not among
			// the sources: it holds no source and the read route reaches
			// nothing it does not have to.
			// -modcacherw is what keeps the downloads deletable: the module
			// cache is written read only by default, and the cockpit has to
			// be able to take a deleted project's cache away again.
			CacheEnv: func(dir string) []string {
				return []string{
					"GOMODCACHE=" + dir + "/mod",
					"GOFLAGS=-modcacherw",
					"XDG_CACHE_HOME=" + dir + "/cache",
				}
			},
			CacheSources: []string{"mod"},
			ImageRoots:   []string{"/usr/local/go/src"},
		},
	},
	{
		ID:           "php",
		Label:        "PHP",
		Command:      []string{"intelephense", "--stdio"},
		Server:       "intelephense",
		Marker:       "composer.json",
		ClassName:    phpClassName,
		TypeOf:       phpTypeOf,
		Crowded:      phpCrowded,
		Clauses:      phpClauses,
		Qualified:    true,
		SymbolLimit:  1000,
		Dependencies: []string{"vendor"},
		languageIDs:  map[string]string{"php": "php"},
		container: container{
			Image:      "dev-cockpit-intelephense",
			Dockerfile: intelephenseDockerfile + entrypointDockerfile,
			// The server stores its index under its own storage paths, not
			// under os.tmpdir: without pointing them into the cache mount
			// the index died with the container and every start ran cold.
			// The per-project directory is the project boundary, the paths
			// inside it stay plain. A dependency of a PHP project lies in
			// its own vendor folder and therefore inside the project, so
			// only the server's stubs stand outside it.
			InitOptions: func(dir string) map[string]any {
				return map[string]any{
					"storagePath":       dir + "/storage",
					"globalStoragePath": dir + "/global",
				}
			},
			ImageRoots: []string{"/usr/local/lib/node_modules/intelephense/lib/stub"},
		},
	},
	{
		ID:              "typescript",
		Label:           "JS / TS",
		Command:         []string{"tsgo", "--lsp", "-stdio"},
		Server:          "tsgo",
		Marker:          "package.json",
		SilentStart:     true,
		TypeOf:          tsTypeOf,
		TypeAsks:        tsTypeAsks,
		LibraryFiles:    []string{"ts", "mts", "cts", "tsx"},
		Heritage:        &heritage{declares: tsDeclares, reads: tsHeritage},
		SymbolLimit:     256,
		LoadsByDocument: true,
		Dependencies:    []string{"node_modules"},
		languageIDs: map[string]string{
			"ts":  "typescript",
			"mts": "typescript",
			"cts": "typescript",
			"tsx": "typescriptreact",
			"js":  "javascript",
			"mjs": "javascript",
			"cjs": "javascript",
			"jsx": "javascriptreact",
		},
		container: container{
			Image:      "dev-cockpit-tsgo",
			Dockerfile: typescriptDockerfile + entrypointDockerfile,
			// A dependency of a node project lies in its own node_modules and
			// therefore inside the project. The lib.*.d.ts files are the
			// server's own and live in the image, under a directory whose name
			// carries the architecture it was built for, so the image root is
			// the installation above it. What lands outside both is what the
			// server downloads itself, the automatic type acquisition a plain
			// JavaScript project gets its @types from: it keeps those under
			// XDG_CACHE_HOME, so without pointing it into the cache mount they
			// would die with the container and a definition would land in a
			// directory nothing could read back. Only the typings subtree holds
			// source; what npm writes beside it is no more readable than gopls'
			// file cache and stays out of the roots.
			CacheEnv: func(dir string) []string {
				return []string{"XDG_CACHE_HOME=" + dir + "/cache"}
			},
			CacheSources: []string{"cache/typescript"},
			ImageRoots:   []string{"/usr/local/lib/node_modules/@typescript"},
			// A project without a configuration of its own gets one from the
			// image, which is what makes a usages list cover the project
			// instead of the opened file and its imports. What that file is
			// and when it is written stands in the build file.
			DefaultConfig: true,
		},
	},
	{
		ID:          "python",
		Label:       "Python",
		Command:     []string{"pyright-langserver", "--stdio"},
		Server:      "pyright",
		Marker:      "pyproject.toml",
		SilentStart: true,
		ListsLate:   true,
		// The image holds no interpreter, so the server finds no environment
		// on its own, and a project's dependencies lie in its own virtual
		// environment: the search paths name the site-packages of the two
		// usual ones. An answered section without autoSearchPaths would take
		// the src root away. A project with a configuration of its own is
		// read from that alone, these settings never overrule it.
		Settings: map[string]any{
			"python": map[string]any{"analysis": map[string]any{
				"autoSearchPaths": true,
				"extraPaths":      []string{".venv/lib/python3*/site-packages", "venv/lib/python3*/site-packages"},
			}},
		},
		// The server leaves a virtual environment out of the workspace on
		// its own, unless a configuration of the project says otherwise; an
		// installed package lies in site-packages, or dist-packages for the
		// system's interpreter, whatever the environment around it.
		TypeOf:       pythonTypeOf,
		Clauses:      pythonClauses,
		Dependencies: []string{"site-packages", "dist-packages"},
		languageIDs:  map[string]string{"py": "python", "pyi": "python"},
		container: container{
			Image:      "dev-cockpit-pyright",
			Dockerfile: pythonDockerfile + entrypointDockerfile,
			// The standard library is the typeshed the server carries, and so
			// are the stubs it falls back on for a package no environment it
			// knows holds. Both live in the image.
			ImageRoots: []string{"/usr/local/lib/node_modules/pyright/dist/typeshed-fallback"},
		},
	},
}

// Profiles returns the fixed profile registry in rendering order.
func Profiles() []*Profile {
	return profiles
}

// Extensions returns the file extensions the profile owns, sorted.
func (p *Profile) Extensions() []string {
	exts := make([]string, 0, len(p.languageIDs))
	for ext := range p.languageIDs {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return exts
}

// ProfileForPath returns the profile owning the file's extension and the
// LSP language id for it.
func ProfileForPath(rel string) (*Profile, string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(rel), "."))
	if ext == "" {
		return nil, "", false
	}
	for _, p := range profiles {
		if id, ok := p.languageIDs[ext]; ok {
			return p, id, true
		}
	}
	return nil, "", false
}

// Detection is a launcher's answer whether its way can run, see
// Launcher.Detect. It never starts a process.
type Detection struct {
	Found bool
	// Path is the resolved executable when found, the docker client for
	// the Docker way.
	Path string
}

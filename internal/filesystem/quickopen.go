package filesystem

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// QuickOpenLimit caps how many matches one quick open query returns. The
// palette never renders more than this, so finding more is wasted work.
const QuickOpenLimit = 100

const (
	// quickOpenTTL is how stale an index may get before a query triggers a
	// refresh. Changes the editor makes itself are applied immediately via
	// Invalidate; this bound only covers changes made behind our back, such as
	// a git checkout or a dependency install in a terminal.
	quickOpenTTL = 30 * time.Second
	// quickOpenIdle drops the index of a project nobody has searched in for
	// this long. Rebuilding costs one walk, holding it costs memory for as
	// long as the process lives.
	quickOpenIdle = 30 * time.Minute
)

// QuickOpenMatches is one answer to a quick open query: the paths to show and
// how many candidates there were in total, so the palette can say that it is
// showing only the first few.
type QuickOpenMatches struct {
	Paths   []string
	Total   int
	Indexed int
}

// quickOpenIndex is every path in one project, plus a lowercased copy so a
// query never has to lowercase anything again. The lowercased copy is what
// makes queries fast and what costs the memory.
type quickOpenIndex struct {
	paths []string
	lower []string
	built time.Time
	// ex is what this index was built with. A query under different exclusions
	// cannot be answered from it, so the setting changing is enough to rebuild.
	ex Exclusions
}

// buildQuickOpenIndex walks root once and keeps every path. It uses the same
// same skip list as the rest of the editor and applies no cap: the point is that
// the palette can reach every file, not just the first few thousand.
func buildQuickOpenIndex(root string, ex Exclusions, now time.Time) (*quickOpenIndex, error) {
	paths, err := listAllFiles(root, ex, runtime.NumCPU())
	if err != nil {
		return nil, err
	}
	ix := &quickOpenIndex{paths: paths, lower: make([]string, len(paths)), built: now, ex: ex}
	buf := make([]byte, 0, 256)
	for i, p := range paths {
		buf = appendLowerASCII(buf[:0], p)
		ix.lower[i] = string(buf)
	}
	return ix, nil
}

// query scores every path and returns the best limit matches. It keeps only
// that many in a bounded heap, so a query like "php" that matches most of the
// tree costs no more than one that matches nothing.
func (ix *quickOpenIndex) query(query, scope string, limit int) QuickOpenMatches {
	// A scope narrows the answer to one folder. The index still covers the whole
	// project, so every scope is served from the same one.
	prefix := ""
	if scope = strings.Trim(scope, "/"); scope != "" {
		prefix = scope + "/"
	}
	tokens := strings.Fields(strings.ToLower(query))

	if len(tokens) == 0 {
		// An empty query is the palette just opening: show the first entries in
		// walk order rather than scoring the whole tree.
		out := make([]string, 0, min(limit, len(ix.paths)))
		total := 0
		for _, p := range ix.paths {
			if prefix != "" && !strings.HasPrefix(p, prefix) {
				continue
			}
			total++
			if len(out) < limit {
				out = append(out, p)
			}
		}
		return QuickOpenMatches{Paths: out, Total: total, Indexed: len(ix.paths)}
	}

	keep := newMatchHeap(limit)
	total := 0
	for i, lower := range ix.lower {
		if prefix != "" && !strings.HasPrefix(ix.paths[i], prefix) {
			continue
		}
		rank, ok := scoreQuickOpen(lower, tokens)
		if !ok {
			continue
		}
		total++
		keep.push(quickOpenMatch{path: ix.paths[i], rank: rank})
	}
	return QuickOpenMatches{Paths: keep.sorted(), Total: total, Indexed: len(ix.paths)}
}

// FolderCount is one folder of a project and how many files sit under it, at
// any depth. The scope is recursive, so that is the number that says what
// picking this folder would cover.
type FolderCount struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
}

// ExtensionCount is one file name pattern of a project and how many files carry
// it, written the way the mask takes it.
type ExtensionCount struct {
	Pattern string `json:"pattern"`
	Files   int    `json:"files"`
}

// FilterFacts is what a project offers the palette's two filters: the folders
// to scope to and the file name patterns that actually occur in it. Both fall
// out of the quick open index, so the choices cost no walk of their own.
type FilterFacts struct {
	Folders    []FolderCount    `json:"folders"`
	Extensions []ExtensionCount `json:"extensions"`
	Files      int              `json:"files"`
}

const (
	// maxFilterFolders bounds what travels to the browser, which holds the
	// whole answer and filters it locally. Shallow folders are kept first:
	// they are the ones a scope is usually set to, and a project that needs
	// more than this many has folders it does not want indexed either.
	maxFilterFolders = 2000
	// maxFilterExtensions is the same bound on the other list, where the tail
	// is one-off extensions nobody scopes by.
	maxFilterExtensions = 100
)

// facts counts the folders and the extensions of one index in a single pass.
func (ix *quickOpenIndex) facts() FilterFacts {
	folders := map[string]int{}
	extensions := map[string]int{}
	for _, p := range ix.paths {
		// Every ancestor counts the file, which is what makes the number the
		// size of the scope rather than of one directory listing.
		for i := 0; i < len(p); i++ {
			if p[i] == '/' {
				folders[p[:i]]++
			}
		}
		if ext := pathExtension(p); ext != "" {
			extensions["*."+ext]++
		}
	}

	out := FilterFacts{Folders: []FolderCount{}, Extensions: []ExtensionCount{}, Files: len(ix.paths)}
	for path, files := range folders {
		out.Folders = append(out.Folders, FolderCount{Path: path, Files: files})
	}
	// Shallow first, then alphabetically: that is the order somebody clicks
	// their way down in, and it puts the useful scopes on the first screen.
	sort.Slice(out.Folders, func(i, j int) bool {
		a, b := out.Folders[i], out.Folders[j]
		da, db := strings.Count(a.Path, "/"), strings.Count(b.Path, "/")
		if da != db {
			return da < db
		}
		return a.Path < b.Path
	})
	if len(out.Folders) > maxFilterFolders {
		out.Folders = out.Folders[:maxFilterFolders]
	}

	for pattern, files := range extensions {
		out.Extensions = append(out.Extensions, ExtensionCount{Pattern: pattern, Files: files})
	}
	sort.Slice(out.Extensions, func(i, j int) bool {
		a, b := out.Extensions[i], out.Extensions[j]
		if a.Files != b.Files {
			return a.Files > b.Files
		}
		return a.Pattern < b.Pattern
	})
	if len(out.Extensions) > maxFilterExtensions {
		out.Extensions = out.Extensions[:maxFilterExtensions]
	}
	return out
}

// pathExtension answers the extension of the file at a slash separated path,
// empty when it has none. A leading dot is a name and not an extension, so
// ".gitignore" has none, and neither has a name ending in a dot.
func pathExtension(p string) string {
	name := p[strings.LastIndexByte(p, '/')+1:]
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 || dot == len(name)-1 {
		return ""
	}
	return name[dot+1:]
}

// quickOpenMatch is one scored candidate.
type quickOpenMatch struct {
	path string
	rank int
}

// scoreQuickOpen grades an already lowercased path against the query tokens.
// Every token has to appear somewhere in the path; the first token alone
// decides the rank, with a file name prefix beating a file name substring
// beating a hit anywhere else in the path.
func scoreQuickOpen(lower string, tokens []string) (int, bool) {
	for _, t := range tokens {
		if !strings.Contains(lower, t) {
			return 0, false
		}
	}
	name := lower[strings.LastIndexByte(lower, '/')+1:]
	switch {
	case strings.HasPrefix(name, tokens[0]):
		return 0, true
	case strings.Contains(name, tokens[0]):
		return 1, true
	default:
		return 2, true
	}
}

// quickOpenLess is the order the palette shows matches in: better rank first,
// then the shorter path, then alphabetically.
func quickOpenLess(a, b quickOpenMatch) bool {
	if a.rank != b.rank {
		return a.rank < b.rank
	}
	if len(a.path) != len(b.path) {
		return len(a.path) < len(b.path)
	}
	return a.path < b.path
}

// matchHeap keeps the best n matches seen and discards the rest. It is a max
// heap on "worst match": items[0] is the entry a better candidate evicts.
type matchHeap struct {
	n     int
	items []quickOpenMatch
}

func newMatchHeap(n int) *matchHeap {
	if n < 0 {
		n = 0
	}
	return &matchHeap{n: n, items: make([]quickOpenMatch, 0, n)}
}

func (h *matchHeap) push(m quickOpenMatch) {
	if h.n == 0 {
		return
	}
	if len(h.items) < h.n {
		h.items = append(h.items, m)
		h.up(len(h.items) - 1)
		return
	}
	if !quickOpenLess(m, h.items[0]) {
		return
	}
	h.items[0] = m
	h.down(0)
}

func (h *matchHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		// worse than the parent means it sorts after it
		if !quickOpenLess(h.items[parent], h.items[i]) {
			return
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

func (h *matchHeap) down(i int) {
	for {
		left, right := 2*i+1, 2*i+2
		worst := i
		if left < len(h.items) && quickOpenLess(h.items[worst], h.items[left]) {
			worst = left
		}
		if right < len(h.items) && quickOpenLess(h.items[worst], h.items[right]) {
			worst = right
		}
		if worst == i {
			return
		}
		h.items[i], h.items[worst] = h.items[worst], h.items[i]
		i = worst
	}
}

// sorted returns the kept paths in the order the palette shows them.
func (h *matchHeap) sorted() []string {
	items := make([]quickOpenMatch, len(h.items))
	copy(items, h.items)
	sort.Slice(items, func(i, j int) bool { return quickOpenLess(items[i], items[j]) })
	out := make([]string, len(items))
	for i, m := range items {
		out[i] = m.path
	}
	return out
}

// appendLowerASCII lowercases ASCII into dst, reusing its capacity. Paths are
// ASCII in practice; other bytes pass through, which is what the palette's
// tokens can match against anyway.
func appendLowerASCII(dst []byte, s string) []byte {
	if cap(dst) < len(s) {
		dst = make([]byte, 0, len(s)+64)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}

// listAllFiles returns every regular file under root, relative and slash
// separated, sorted. It stays out of the excluded directories and applies no
// cap.
//
// Unlike filepath.WalkDir this reads directories on a pool of workers, which on
// a large tree is roughly twice as fast because the walk is dominated by
// waiting on directory reads rather than by CPU. Order is restored by sorting
// at the end.
func listAllFiles(root string, ex Exclusions, workers int) ([]string, error) {
	if workers < 1 {
		workers = 1
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		files []string
		// Only the first error is interesting; a walk that cannot read one
		// subdirectory should still return the rest of the tree.
		firstErr atomic.Pointer[error]
	)
	// One slot per additional worker: the goroutine that called us does the
	// work of the first.
	sem := make(chan struct{}, workers-1)

	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		defer wg.Done()
		entries, err := os.ReadDir(dir)
		if err != nil {
			if dir == root {
				firstErr.CompareAndSwap(nil, &err)
			}
			return
		}
		local := make([]string, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				child := name
				if rel != "" {
					child = rel + "/" + name
				}
				if ex.SkipDir(child, name) {
					continue
				}
				wg.Add(1)
				select {
				case sem <- struct{}{}:
					go func(d, r string) {
						defer func() { <-sem }()
						walk(d, r)
					}(filepath.Join(dir, name), child)
				default:
					// Pool is busy: descend inline rather than pile up
					// goroutines that would only queue for the same disk.
					walk(filepath.Join(dir, name), child)
				}
				continue
			}
			if !e.Type().IsRegular() {
				continue
			}
			if rel == "" {
				local = append(local, name)
			} else {
				local = append(local, rel+"/"+name)
			}
		}
		if len(local) > 0 {
			mu.Lock()
			files = append(files, local...)
			mu.Unlock()
		}
	}

	wg.Add(1)
	walk(root, "")
	wg.Wait()

	if err := firstErr.Load(); err != nil {
		return nil, *err
	}
	sort.Strings(files)
	return files, nil
}

// QuickOpenCache holds one index per project. Queries are answered from the
// index that is there, so only the very first query of a project waits for a
// walk. An index the editor itself invalidated is rebuilt on the next query; an
// index that merely went stale is served as it is and refreshed in the
// background.
type QuickOpenCache struct {
	mu      sync.Mutex
	entries map[string]*quickOpenEntry
	// now is swappable so the staleness and eviction rules can be tested
	// without sleeping.
	now func() time.Time
}

type quickOpenEntry struct {
	// build serialises rebuilds of this project, so ten palette opens during a
	// cold start cause one walk and not ten.
	build sync.Mutex
	index atomic.Pointer[quickOpenIndex]
	// refreshing keeps one background refresh in flight at a time.
	refreshing atomic.Bool
	lastUsed   atomic.Int64
}

// NewQuickOpenCache returns an empty cache.
func NewQuickOpenCache() *QuickOpenCache {
	return &QuickOpenCache{entries: map[string]*quickOpenEntry{}, now: time.Now}
}

// Query answers a quick open query for the project at root, building or
// refreshing the index as needed.
func (c *QuickOpenCache) Query(root, query, scope string, ex Exclusions, limit int) (QuickOpenMatches, error) {
	ix, err := c.indexFor(root, ex)
	if err != nil {
		return QuickOpenMatches{}, err
	}
	return ix.query(query, scope, limit), nil
}

// Facts answers what the palette's filters can be set to in the project at
// root. It is the same lookup Query makes with a different question at the end.
func (c *QuickOpenCache) Facts(root string, ex Exclusions) (FilterFacts, error) {
	ix, err := c.indexFor(root, ex)
	if err != nil {
		return FilterFacts{}, err
	}
	return ix.facts(), nil
}

// indexFor answers a usable index for root, building one when nothing is
// cached and refreshing a stale one in the background. A built index never
// changes again, so the questions are asked outside the build lock.
func (c *QuickOpenCache) indexFor(root string, ex Exclusions) (*quickOpenIndex, error) {
	now := c.now()
	entry := c.entryFor(root, now)

	// An index built under different exclusions answers a different question, so
	// it counts as absent. Changing the setting needs no invalidation hook.
	if ix := entry.index.Load(); ix != nil && ix.ex.Equal(ex) {
		if now.Sub(ix.built) > quickOpenTTL {
			c.refreshInBackground(root, ex, entry)
		}
		return ix, nil
	}

	// Nothing usable cached: this is the one query that pays for the walk.
	entry.build.Lock()
	defer entry.build.Unlock()
	if ix := entry.index.Load(); ix != nil && ix.ex.Equal(ex) {
		return ix, nil
	}
	ix, err := buildQuickOpenIndex(root, ex, c.now())
	if err != nil {
		return nil, err
	}
	entry.index.Store(ix)
	return ix, nil
}

// Invalidate drops the index for root so the next query rebuilds it. The
// editor calls this after it changed the tree itself, which is what makes a
// file findable immediately after being created.
func (c *QuickOpenCache) Invalidate(root string) {
	c.mu.Lock()
	entry := c.entries[root]
	c.mu.Unlock()
	if entry != nil {
		entry.index.Store(nil)
	}
}

// Forget drops root from the cache entirely. Invalidate is for a project that
// changed and will be queried again, so it keeps the entry and only clears the
// index; a project that is gone has no next query, and the sweep that would
// have collected it runs on a query. Its index would sit there until somebody
// happened to search in another project.
func (c *QuickOpenCache) Forget(root string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, root)
}

// entryFor returns the cache entry for root, creating it if needed, and takes
// the opportunity to drop indexes nobody has used in a while.
//
// The use is recorded here rather than by the caller, under the same lock the
// sweep runs under. A fresh entry starts out with a zero lastUsed, which reads
// as 1970 and therefore as idle, so a caller that stored it afterwards left a
// window in which a query for another project swept the entry away while its
// walk was still running: the index was then built into an entry no longer in
// the map, and the next query paid for the walk again.
func (c *QuickOpenCache) entryFor(root string, now time.Time) *quickOpenEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	for path, e := range c.entries {
		if path == root {
			continue
		}
		if now.Sub(time.Unix(0, e.lastUsed.Load())) > quickOpenIdle {
			delete(c.entries, path)
		}
	}
	entry := c.entries[root]
	if entry == nil {
		entry = &quickOpenEntry{}
		c.entries[root] = entry
	}
	entry.lastUsed.Store(now.UnixNano())
	return entry
}

// refreshInBackground rebuilds a stale index without making the caller wait,
// then swaps it in. At most one refresh per project runs at a time.
func (c *QuickOpenCache) refreshInBackground(root string, ex Exclusions, entry *quickOpenEntry) {
	if !entry.refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer entry.refreshing.Store(false)
		entry.build.Lock()
		defer entry.build.Unlock()
		ix, err := buildQuickOpenIndex(root, ex, c.now())
		if err != nil {
			// The tree is still there next time; keeping the old index is
			// better than dropping it because one walk failed.
			return
		}
		entry.index.Store(ix)
	}()
}

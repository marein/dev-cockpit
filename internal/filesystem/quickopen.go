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
	now := c.now()
	entry := c.entryFor(root, now)
	entry.lastUsed.Store(now.UnixNano())

	// An index built under different exclusions answers a different question, so
	// it counts as absent. Changing the setting needs no invalidation hook.
	if ix := entry.index.Load(); ix != nil && ix.ex.Equal(ex) {
		if now.Sub(ix.built) > quickOpenTTL {
			c.refreshInBackground(root, ex, entry)
		}
		return ix.query(query, scope, limit), nil
	}

	// Nothing usable cached: this is the one query that pays for the walk.
	entry.build.Lock()
	defer entry.build.Unlock()
	if ix := entry.index.Load(); ix != nil && ix.ex.Equal(ex) {
		return ix.query(query, scope, limit), nil
	}
	ix, err := buildQuickOpenIndex(root, ex, c.now())
	if err != nil {
		return QuickOpenMatches{}, err
	}
	entry.index.Store(ix)
	return ix.query(query, scope, limit), nil
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

// entryFor returns the cache entry for root, creating it if needed, and takes
// the opportunity to drop indexes nobody has used in a while.
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

package filesystem

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// quickOpenTree writes a small project that exercises the skip list, the three
// ranks and the tie breakers. The layout follows php-gaming-website, a real
// project this feature has to hold up against.
func quickOpenTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"src/Kernel.php",
		"src/Chat/Application/ChatGateway.php",
		"src/ConnectFour/Domain/Game/Game.php",
		"tests/unit/ConnectFour/Domain/Game/GameTest.php",
		"config/chat/config.yml",
		"config/connect-four/config.yml",
		"node_modules/pkg/index.js",
		".git/config",
	} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestListAllFilesSkipsAndIsComplete(t *testing.T) {
	root := quickOpenTree(t)
	// Every worker count must produce the same set, including the inline path
	// taken when the pool is saturated.
	var want []string
	for _, workers := range []int{1, 2, 8} {
		got, err := listAllFiles(root, DefaultExclusionSet(), workers)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range got {
			if strings.HasPrefix(p, ".git/") {
				t.Errorf("workers=%d: the default exclusion leaked %q", workers, p)
			}
		}
		// node_modules is searchable by default now, so it has to be in there.
		if !slices.Contains(got, "node_modules/pkg/index.js") {
			t.Errorf("workers=%d: node_modules is not excluded by default, it must be indexed: %v", workers, got)
		}
		if want == nil {
			want = got
			continue
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("workers=%d: got %v, want %v", workers, got, want)
		}
	}
	if len(want) != 7 {
		t.Errorf("files = %d, want 7 (%v)", len(want), want)
	}
	if !sort.StringsAreSorted(want) {
		t.Errorf("result is not sorted: %v", want)
	}
}

// TestListAllFilesIsUncapped is the regression guard for the bug this replaces:
// the old listing stopped at 5000 entries and silently hid the rest.
func TestListAllFilesIsUncapped(t *testing.T) {
	root := t.TempDir()
	want := 5000 + 250
	for i := 0; i < want; i++ {
		if err := os.WriteFile(filepath.Join(root, "f"+itoa(i)+".php"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := listAllFiles(root, DefaultExclusionSet(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != want {
		t.Fatalf("files = %d, want %d", len(got), want)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestScoreQuickOpenRanks(t *testing.T) {
	cases := []struct {
		path  string
		query string
		rank  int
		ok    bool
	}{
		{"config/chat/config.yml", "config", 0, true},                 // file name prefix
		{"src/Chat/Application/ChatGateway.php", "gateway", 1, true},  // file name substring
		{"config/chat/config.yml", "chat", 2, true},                   // path only
		{"src/Kernel.php", "chat", 0, false},                          // no match
		{"src/ConnectFour/Domain/Game/Game.php", "game php", 0, true}, // all tokens
		{"src/Chat/Application/ChatId.php", "chat gateway", 0, false}, // one token missing
	}
	for _, c := range cases {
		lower := string(appendLowerASCII(nil, c.path))
		rank, ok := scoreQuickOpen(lower, strings.Fields(strings.ToLower(c.query)))
		if ok != c.ok {
			t.Errorf("%s / %q: ok = %v, want %v", c.path, c.query, ok, c.ok)
			continue
		}
		if ok && rank != c.rank {
			t.Errorf("%s / %q: rank = %d, want %d", c.path, c.query, rank, c.rank)
		}
	}
}

// TestMatchHeapKeepsTheBest checks the bounded heap against a full sort, which
// is what it has to be indistinguishable from.
func TestMatchHeapKeepsTheBest(t *testing.T) {
	candidates := []quickOpenMatch{
		{"a/app.yml", 0}, {"b/app-longer-name.yml", 1}, {"app/x.yml", 2},
		{"c/app.yml", 0}, {"d/my-app.yml", 1}, {"e/app.yaml", 0},
	}
	full := make([]quickOpenMatch, len(candidates))
	copy(full, candidates)
	sort.Slice(full, func(i, j int) bool { return quickOpenLess(full[i], full[j]) })

	for n := 0; n <= len(candidates)+1; n++ {
		h := newMatchHeap(n)
		for _, m := range candidates {
			h.push(m)
		}
		got := h.sorted()
		want := []string{}
		for _, m := range full[:min(n, len(full))] {
			want = append(want, m.path)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("n=%d: got %v, want %v", n, got, want)
		}
	}
}

func TestQuickOpenIndexQuery(t *testing.T) {
	root := quickOpenTree(t)
	ix, err := buildQuickOpenIndex(root, DefaultExclusionSet(), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Both candidates rank 0 here because both file names start with the
	// query, so the tie falls to the shorter path.
	//
	// That is deliberately the same order the old client side filterFiles
	// produced: score, then path length, then alphabetically. Preferring an
	// exact file name stem over a shorter path would be an improvement, but it
	// would also be a behaviour change, so it is left for its own commit.
	res := ix.query("Game", "", QuickOpenLimit)
	want := []string{
		"src/ConnectFour/Domain/Game/Game.php",
		"tests/unit/ConnectFour/Domain/Game/GameTest.php",
	}
	if !reflect.DeepEqual(res.Paths, want) {
		t.Errorf("paths = %v, want %v", res.Paths, want)
	}
	if res.Total != 2 {
		t.Errorf("total = %d, want 2", res.Total)
	}

	// A file name prefix must come before a path-only match: the gateway is
	// named chat, the config only sits in a chat folder.
	res = ix.query("chat", "", QuickOpenLimit)
	if len(res.Paths) == 0 || res.Paths[0] != "src/Chat/Application/ChatGateway.php" {
		t.Errorf("chat hits = %v", res.Paths)
	}
	if res.Indexed != 7 {
		t.Errorf("indexed = %d, want 7", res.Indexed)
	}

	if got := ix.query("zzznope", "", QuickOpenLimit); len(got.Paths) != 0 || got.Total != 0 {
		t.Errorf("no-hit query returned %v", got)
	}

	// An empty query is the palette opening: show something, capped.
	if got := ix.query("  ", "", 3); len(got.Paths) != 3 || got.Total != 7 {
		t.Errorf("empty query = %v (total %d), want 3 paths of 7", got.Paths, got.Total)
	}
}

func TestQuickOpenIndexRespectsLimit(t *testing.T) {
	root := quickOpenTree(t)
	ix, err := buildQuickOpenIndex(root, DefaultExclusionSet(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	res := ix.query("php", "", 2)
	if len(res.Paths) != 2 {
		t.Errorf("paths = %d, want 2", len(res.Paths))
	}
	if res.Total != 4 {
		t.Errorf("total = %d, want 4 (limit must not change the count)", res.Total)
	}
}

func TestQuickOpenCacheBuildsOnceAndInvalidates(t *testing.T) {
	root := quickOpenTree(t)
	cache := NewQuickOpenCache()
	clock := time.Now()
	cache.now = func() time.Time { return clock }

	res, err := cache.Query(root, "Game", "", DefaultExclusionSet(), QuickOpenLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) == 0 {
		t.Fatal("expected hits")
	}

	// A file created behind the index is not visible until something says so.
	extra := filepath.Join(root, "config", "connect-four", "routing.yml")
	if err := os.WriteFile(extra, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ = cache.Query(root, "routing", "", DefaultExclusionSet(), QuickOpenLimit)
	if len(res.Paths) != 0 {
		t.Errorf("cached index should not know the new file yet, got %v", res.Paths)
	}

	// Invalidate is what the editor calls after it wrote something.
	cache.Invalidate(root)
	res, err = cache.Query(root, "routing", "", DefaultExclusionSet(), QuickOpenLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 1 || res.Paths[0] != "config/connect-four/routing.yml" {
		t.Errorf("after Invalidate got %v, want the new file", res.Paths)
	}
}

func TestQuickOpenCacheServesStaleThenRefreshes(t *testing.T) {
	root := quickOpenTree(t)
	cache := NewQuickOpenCache()
	clock := time.Now()
	cache.now = func() time.Time { return clock }

	if _, err := cache.Query(root, "Game", "", DefaultExclusionSet(), QuickOpenLimit); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "chat", "routing.yml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Past the staleness bound the answer still comes back immediately, from
	// the old index, and a refresh is kicked off behind it.
	clock = clock.Add(quickOpenTTL + time.Second)
	res, err := cache.Query(root, "routing", "", DefaultExclusionSet(), QuickOpenLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 0 {
		t.Errorf("stale query should answer from the old index, got %v", res.Paths)
	}

	// The refresh runs in a goroutine; wait for it rather than sleeping blind.
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, err = cache.Query(root, "routing", "", DefaultExclusionSet(), QuickOpenLimit)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Paths) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background refresh did not land, got %v", res.Paths)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestQuickOpenCacheEvictsIdleProjects(t *testing.T) {
	a, b := quickOpenTree(t), quickOpenTree(t)
	cache := NewQuickOpenCache()
	clock := time.Now()
	cache.now = func() time.Time { return clock }

	if _, err := cache.Query(a, "Game", "", DefaultExclusionSet(), QuickOpenLimit); err != nil {
		t.Fatal(err)
	}
	if got := len(cache.entries); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}

	// Touching another project long after the first was last used drops the
	// first, so memory follows active use rather than total projects.
	clock = clock.Add(quickOpenIdle + time.Minute)
	if _, err := cache.Query(b, "Game", "", DefaultExclusionSet(), QuickOpenLimit); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	_, stillThere := cache.entries[a]
	count := len(cache.entries)
	cache.mu.Unlock()
	if stillThere {
		t.Error("idle project should have been evicted")
	}
	if count != 1 {
		t.Errorf("entries = %d, want 1", count)
	}
}

// TestListAllFilesMatchesAPlainWalk pins the concurrent walk against a naive
// single threaded WalkDir. The test owns this expectation rather than comparing
// against production code, so the two cannot drift together.
func TestListAllFilesMatchesAPlainWalk(t *testing.T) {
	root := quickOpenTree(t)
	var want []string
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && DefaultExclusionSet().SkipDir(relTo(root, p), d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			want = append(want, relTo(root, p))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(want)

	got, err := listAllFiles(root, DefaultExclusionSet(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("concurrent walk differs from a plain WalkDir:\n want = %v\n got  = %v", want, got)
	}
}

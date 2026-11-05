package editorintelligence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marein/dev-cockpit/internal/gitfacts"
)

const (
	// seedBudget bounds one seed: it is a shortcut and must never cost the
	// start what the server's own run would.
	seedBudget = 3 * time.Second
	// seedClockSlack keeps the seed's time behind the file clock, which
	// runs coarser than the wall clock: a file written while the seed runs
	// reads newer than the seed and is indexed again.
	seedClockSlack = 2 * time.Second
	// seedPendingFile lists in a seeded project's cache the dependency
	// folders the project lacked, one per line. The start after one of them
	// is installed seeds again, or the server would index every dependency
	// on its own; the list stays until such a seed went through.
	seedPendingFile = "index-seed-pending"
	// seedStale is the time an entry the seed cannot keep wears. The server
	// reads a missing or zero time as none at all and never reindexes such
	// a file, while every file is newer than this.
	seedStale = 1
	// composerAutoloader is what composer writes into a dependency folder
	// after every package, so it stands once an install went through.
	composerAutoloader = "autoload.php"
)

// seedSource is the index another working copy of the repository holds.
type seedSource struct {
	project string
	root    string
	dir     string
	stamp   int64
}

// seedCounts are the workspace entries of a seed: kept as indexed, left to
// the server to index again, and gone from the target, which the server
// drops.
type seedCounts struct {
	kept, reindexed, gone int
}

var errLacksDependency = errors.New("its index holds no")

// seedIntelephenseIndex fills the project's missing index before its server
// starts, from the newest index another working copy of the same repository
// holds, so a new worktree starts in a second instead of a full run. An
// entry is kept only when it provably describes the target's file, every
// other one the server indexes again, and any failure leaves no index
// behind: the server then starts cold as it always did. A source without a
// dependency folder the target has saves little, it comes last, and never
// replaces an index of the target's own.
func seedIntelephenseIndex(ctx context.Context, cacheRoot string, p *Profile, project, root string) {
	if !plainPath(root) {
		return
	}
	cache := cacheDir(cacheRoot, p.Server, project)
	storage := filepath.Join(cache, "storage")
	dst := filepath.Join(storage, intelephenseIndexDir(root))
	pending := filepath.Join(cache, seedPendingFile)
	_, err := os.Stat(dst)
	own := err == nil
	if own && !dependenciesInstalled(root, pending) {
		return
	}
	started := time.Now()
	sources := seedSources(cacheRoot, p.Server, root)
	if len(sources) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, seedBudget)
	defer cancel()
	waiting := missingDependencies(root, p.Dependencies)
	var refused []string
	var lacking []seedSource
	seeded := func(src seedSource, deps []string) bool {
		counts, err := seedFrom(ctx, src, root, storage, dst, deps)
		if err != nil {
			if errors.Is(err, errLacksDependency) && !own {
				lacking = append(lacking, src)
			}
			refused = append(refused, src.project+": "+err.Error())
			return false
		}
		writePending(pending, waiting)
		again := ""
		if len(waiting) > 0 {
			again = ", seeds again once " + strings.Join(waiting, ", ") + " is installed"
		}
		log.Printf("editor intelligence: seeded the PHP index of %s from %s in %v, %d entries kept, %d reindexed, %d gone%s",
			project, src.project, time.Since(started).Round(time.Millisecond), counts.kept, counts.reindexed, counts.gone, again)
		return true
	}
	for _, src := range sources {
		if seeded(src, p.Dependencies) {
			return
		}
		if ctx.Err() != nil {
			break
		}
	}
	for _, src := range lacking {
		if ctx.Err() != nil {
			break
		}
		if seeded(src, nil) {
			return
		}
	}
	outcome := "keeps its own"
	if !own {
		outcome = "starts cold"
		writePending(pending, waiting)
	}
	log.Printf("editor intelligence: the PHP index of %s %s, no index to seed it from: %s", project, outcome, strings.Join(refused, "; "))
}

// missingDependencies are the dependency folders the project has not
// installed yet.
func missingDependencies(root string, deps []string) []string {
	var missing []string
	for _, dep := range deps {
		if !dependencyInstalled(root, dep) {
			missing = append(missing, dep)
		}
	}
	return missing
}

func dependencyInstalled(root, dep string) bool {
	_, err := os.Stat(filepath.Join(root, dep, composerAutoloader))
	return err == nil
}

// dependenciesInstalled reports whether a dependency folder the last seed
// waited for is installed now.
func dependenciesInstalled(root, pending string) bool {
	data, err := os.ReadFile(pending)
	if err != nil {
		return false
	}
	for _, dep := range strings.Split(string(data), "\n") {
		if dep != "" && dependencyInstalled(root, dep) {
			return true
		}
	}
	return false
}

func writePending(pending string, waiting []string) {
	if len(waiting) == 0 {
		_ = os.Remove(pending)
		return
	}
	_ = os.WriteFile(pending, []byte(strings.Join(waiting, "\n")+"\n"), 0o644)
}

// seedSources are the other working copies of the root's repository that
// are projects beside it, with an index of the seed's server version, the
// newest first. The main working copy is one of them like any worktree.
func seedSources(cacheRoot, server, root string) []seedSource {
	self, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	var out []seedSource
	for _, other := range gitfacts.WorkingCopies(root) {
		resolved, err := filepath.EvalSymlinks(other)
		if err != nil || resolved == self || filepath.Dir(resolved) != filepath.Dir(self) || !plainPath(resolved) {
			continue
		}
		project := filepath.Base(resolved)
		dir := filepath.Join(cacheDir(cacheRoot, server, project), "storage", intelephenseIndexDir(resolved))
		data, err := os.ReadFile(filepath.Join(dir, intelephenseIndexFile(1)))
		if err != nil {
			continue
		}
		meta, err := readIntelephenseMeta(data)
		if err != nil || meta.Version != intelephenseSeedVersion || meta.Environment.DocumentRoot != fileURI(resolved) {
			continue
		}
		out = append(out, seedSource{project: project, root: resolved, dir: dir, stamp: meta.Timestamp})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].stamp > out[j].stamp })
	return out
}

// seedFrom writes the target's index from one source, or nothing at all.
// deps are the dependency folders the source has to hold where the target
// has them, nil for none.
func seedFrom(ctx context.Context, src seedSource, root, storage, dst string, deps []string) (seedCounts, error) {
	if fi, err := os.Stat(src.root); err != nil || !fi.IsDir() {
		return seedCounts{}, errors.New("its files are gone")
	}
	files, err := readIndexSnapshot(src.dir)
	if err != nil {
		return seedCounts{}, err
	}
	now := time.Now().Add(-seedClockSlack).UnixMilli()
	docs, counts, err := seedDocuments(ctx, files[1], src.root, root, now, deps)
	if err != nil {
		return seedCounts{}, err
	}
	if counts.kept == 0 {
		return seedCounts{}, errors.New("none of its entries holds for this project")
	}
	files[1] = docs
	if files[0], err = rewriteIntelephenseMeta(files[0], src.root, root, now); err != nil {
		return seedCounts{}, err
	}
	if files[8], err = rewriteIntelephenseConfig(files[8], src.root, root); err != nil {
		return seedCounts{}, err
	}
	for _, data := range [][]byte{files[0], files[1], files[8]} {
		if bytes.Contains(data, []byte(src.root+"/")) || bytes.Contains(data, []byte(src.root+`"`)) {
			return seedCounts{}, errors.New("its index names its own paths where the seed does not expect them")
		}
	}
	if err := ctx.Err(); err != nil {
		return seedCounts{}, err
	}
	return counts, writeIndex(storage, dst, files)
}

var (
	errIndexMoving     = errors.New("its server is writing it")
	errIndexIncomplete = errors.New("its index is incomplete")
)

// readIndexSnapshot reads one source index whole while nothing writes it.
// The server empties the directory before a save, writes the records, then
// the first file, then its workspace config on its own. A complete save
// has every file in place and no record newer than the first file, and a
// save that began meanwhile changes what a second look sees.
func readIndexSnapshot(dir string) ([][]byte, error) {
	before, err := statIndex(dir)
	if err != nil {
		return nil, err
	}
	files := make([][]byte, intelephenseIndexFiles)
	for i := range files {
		if files[i], err = os.ReadFile(filepath.Join(dir, intelephenseIndexFile(i+1))); err != nil {
			return nil, errIndexMoving
		}
	}
	after, err := statIndex(dir)
	if err != nil {
		return nil, err
	}
	for i := range before {
		if !os.SameFile(before[i], after[i]) || !before[i].ModTime().Equal(after[i].ModTime()) ||
			before[i].Size() != after[i].Size() || int64(len(files[i])) != after[i].Size() {
			return nil, errIndexMoving
		}
	}
	return files, nil
}

func statIndex(dir string) ([]fs.FileInfo, error) {
	infos := make([]fs.FileInfo, intelephenseIndexFiles)
	for i := range infos {
		fi, err := os.Stat(filepath.Join(dir, intelephenseIndexFile(i+1)))
		if err != nil || !fi.Mode().IsRegular() {
			return nil, errIndexIncomplete
		}
		infos[i] = fi
	}
	for _, fi := range infos[1 : intelephenseIndexFiles-1] {
		if fi.ModTime().After(infos[0].ModTime()) {
			return nil, errIndexMoving
		}
	}
	return infos, nil
}

type seedVerdict uint8

const (
	seedReindex seedVerdict = iota
	seedKeep
	seedGone
)

// seedCheck is one workspace entry: the file the source indexed, the
// target's file at the same place and the time the source indexed it.
type seedCheck struct {
	src, dst string
	indexed  int64
}

// seedDocuments rewrites intelephense_data_2 for the target. The ids stay,
// so the files that hold definitions and references by id carry over as
// they are. A kept entry wears the seed's time, every other one seedStale,
// so the server indexes it again.
func seedDocuments(ctx context.Context, data []byte, from, to string, now int64, deps []string) ([]byte, seedCounts, error) {
	var counts seedCounts
	docs, err := parseIndexDocuments(data)
	if err != nil {
		return nil, counts, err
	}
	fromPrefix, stubPrefix := fileURI(from)+"/", fileURI(intelephenseStubs)+"/"
	checks := make([]seedCheck, len(docs))
	held := map[string]bool{}
	for i, doc := range docs {
		if strings.HasPrefix(doc.uri, stubPrefix) {
			continue
		}
		if !strings.HasPrefix(doc.uri, fromPrefix) {
			return nil, counts, errors.New("its index holds files outside the project")
		}
		path := uriPath(doc.uri)
		if filepath.Clean(path) != path || !strings.HasPrefix(path, from+"/") {
			return nil, counts, errIndexFormat
		}
		checks[i] = seedCheck{src: path, dst: to + strings.TrimPrefix(path, from), indexed: int64(doc.indexed)}
		if top, _, ok := strings.Cut(strings.TrimPrefix(path, from+"/"), "/"); ok {
			held[top] = true
		}
	}
	for _, dep := range deps {
		if fi, err := os.Stat(filepath.Join(to, dep)); err == nil && fi.IsDir() && !held[dep] {
			return nil, counts, fmt.Errorf("%w %s", errLacksDependency, dep)
		}
	}
	verdicts, err := checkSeedDocuments(ctx, checks, now)
	if err != nil {
		return nil, counts, err
	}
	out := make([]byte, 0, len(data))
	for i, doc := range docs {
		if checks[i].src == "" {
			out = appendRawIndexDocument(out, doc)
			continue
		}
		indexed := uint64(seedStale)
		switch verdicts[i] {
		case seedKeep:
			counts.kept++
			indexed = uint64(now)
		case seedGone:
			counts.gone++
		default:
			counts.reindexed++
		}
		out = appendIndexDocument(out, doc, fileURI(to)+strings.TrimPrefix(doc.uri, fileURI(from)), indexed)
	}
	return out, counts, nil
}

// checkSeedDocuments judges the entries side by side, the workspace's files
// read in parallel. An entry without paths is the stubs' and stays out.
func checkSeedDocuments(ctx context.Context, checks []seedCheck, now int64) ([]seedVerdict, error) {
	verdicts := make([]seedVerdict, len(checks))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, b := make([]byte, 64<<10), make([]byte, 64<<10)
			for ctx.Err() == nil {
				i := int(next.Add(1)) - 1
				if i >= len(checks) {
					return
				}
				if checks[i].src != "" {
					verdicts[i] = checkSeedDocument(checks[i], now, a, b)
				}
			}
		}()
	}
	wg.Wait()
	return verdicts, ctx.Err()
}

// checkSeedDocument keeps an entry when the server would call it fresh for
// the source, the source file still stands as it was indexed, and the
// target's file holds the same bytes and is older than the seed.
func checkSeedDocument(c seedCheck, now int64, a, b []byte) seedVerdict {
	dst, err := os.Stat(c.dst)
	if errors.Is(err, fs.ErrNotExist) {
		return seedGone
	}
	if err != nil || !dst.Mode().IsRegular() || dst.ModTime().UnixMilli() > now {
		return seedReindex
	}
	src, err := os.Stat(c.src)
	if err != nil || !src.Mode().IsRegular() || src.ModTime().UnixMilli() > c.indexed || src.Size() != dst.Size() {
		return seedReindex
	}
	if !sameContent(c.src, c.dst, a, b) {
		return seedReindex
	}
	again, err := os.Stat(c.src)
	if err != nil || !again.ModTime().Equal(src.ModTime()) || again.Size() != src.Size() {
		return seedReindex
	}
	return seedKeep
}

func sameContent(pathA, pathB string, a, b []byte) bool {
	fa, err := os.Open(pathA)
	if err != nil {
		return false
	}
	defer fa.Close()
	fb, err := os.Open(pathB)
	if err != nil {
		return false
	}
	defer fb.Close()
	for {
		na, errA := io.ReadFull(fa, a)
		nb, errB := io.ReadFull(fb, b)
		if na != nb || !bytes.Equal(a[:na], b[:nb]) {
			return false
		}
		if errA != nil || errB != nil {
			return (errA == io.EOF || errA == io.ErrUnexpectedEOF) && errA == errB
		}
	}
}

// writeIndex puts the index in place in one rename, a replaced one taken
// away after it. What a seed that died half way left behind goes first. The
// files are written in the server's order, so the index reads as a complete
// save when it seeds the next working copy, see readIndexSnapshot.
func writeIndex(storage, dst string, files [][]byte) error {
	if err := os.MkdirAll(storage, 0o755); err != nil {
		return err
	}
	if leftovers, err := filepath.Glob(filepath.Join(storage, ".seed-*")); err == nil {
		for _, dir := range leftovers {
			_ = os.RemoveAll(dir)
		}
	}
	tmp, err := os.MkdirTemp(storage, ".seed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, n := range []int{2, 3, 4, 5, 6, 7, 8, 1, 9} {
		if err := os.WriteFile(filepath.Join(tmp, intelephenseIndexFile(n)), files[n-1], 0o644); err != nil {
			return err
		}
	}
	if _, err := os.Stat(dst); err == nil {
		old := tmp + "-replaced"
		if err := os.Rename(dst, old); err != nil {
			return err
		}
		defer os.RemoveAll(old)
	}
	return os.Rename(tmp, dst)
}

package filesystem

import (
	"path"
	"sort"
	"strings"
)

// QuickOpenEntry is one file or folder of a project, relative and slash
// separated, a folder without its trailing slash.
type QuickOpenEntry struct {
	Path   string
	Folder bool
}

// QuickOpenBrowse is one answer to a path typed so far.
type QuickOpenBrowse struct {
	// Folder is the deepest folder the typed path walks into, "" for the root.
	Folder string
	// Query is the rest of the typed path, matched below Folder.
	Query   string
	Entries []QuickOpenEntry
	// Total counts every entry that matched, or every entry of Folder itself
	// when the query is empty, so the caller can tell Entries was cut.
	Total int
	// Files counts the files below Folder at any depth.
	Files int
}

// Browse answers a path typed so far for the project at root. Its part up to
// the deepest folder of the index it names is where the answer stands, so a
// picked folder is walked into by typing its path. An empty rest lists the
// folder's own folders and files in the tree's order. Any other rest is
// matched against every file and folder below the folder, ranked like a quick
// open query on the path below it, so a name the folder's path holds does not
// match everything in it.
func (c *QuickOpenCache) Browse(root, typed string, ex Exclusions, limit int) (QuickOpenBrowse, error) {
	ix, err := c.indexFor(root, ex)
	if err != nil {
		return QuickOpenBrowse{}, err
	}
	return ix.browse(typed, limit), nil
}

func (ix *quickOpenIndex) browse(typed string, limit int) QuickOpenBrowse {
	prefix := ix.deepestFolder(typed)
	out := QuickOpenBrowse{Folder: strings.TrimSuffix(prefix, "/"), Query: typed[len(prefix):]}
	fileLo, fileHi := prefixRange(ix.paths, prefix)
	folderLo, folderHi := prefixRange(ix.folders, prefix)
	if prefix != "" {
		// The folder itself stands first in its own range.
		folderLo++
	}
	out.Files = fileHi - fileLo
	tokens := strings.Fields(strings.ToLower(out.Query))
	if len(tokens) == 0 {
		var folders, files []string
		for _, f := range ix.folders[folderLo:folderHi] {
			if strings.IndexByte(f[len(prefix):], '/') == len(f)-len(prefix)-1 {
				folders = append(folders, f)
			}
		}
		for _, p := range ix.paths[fileLo:fileHi] {
			if strings.IndexByte(p[len(prefix):], '/') < 0 {
				files = append(files, p)
			}
		}
		listed := append(folders, files...)
		sort.Slice(listed, func(i, j int) bool {
			a, b := listed[i], listed[j]
			return inTreeOrder(strings.HasSuffix(a, "/"), path.Base(a), strings.HasSuffix(b, "/"), path.Base(b))
		})
		out.Total = len(listed)
		out.Entries = entriesOf(listed[:min(limit, len(listed))])
		return out
	}

	keep := newMatchHeap(limit)
	for i := fileLo; i < fileHi; i++ {
		if tier, pos, ok := scoreQuickOpen(ix.paths[i][len(prefix):], ix.lower[i][len(prefix):], tokens); ok {
			out.Total++
			keep.push(quickOpenMatch{path: ix.paths[i], tier: tier, pos: pos})
		}
	}
	for i := folderLo; i < folderHi; i++ {
		if tier, pos, ok := scoreQuickOpen(ix.folders[i][len(prefix):], ix.folderLower[i][len(prefix):], tokens); ok {
			out.Total++
			keep.push(quickOpenMatch{path: ix.folders[i], tier: tier, pos: pos})
		}
	}
	out.Entries = entriesOf(keep.sorted())
	return out
}

// deepestFolder answers the longest start of typed that ends at a slash and
// names a folder of the index, slash included, "" when none does. A typed path
// that names no folder is a plain query, the way it reads without folders.
func (ix *quickOpenIndex) deepestFolder(typed string) string {
	for end := strings.LastIndexByte(typed, '/'); end > 0; end = strings.LastIndexByte(typed[:end], '/') {
		dir := typed[:end+1]
		if at := sort.SearchStrings(ix.folders, dir); at < len(ix.folders) && ix.folders[at] == dir {
			return dir
		}
	}
	return ""
}

// foldersOf answers every folder above the sorted paths, once each, sorted and
// with a trailing slash. Neighbouring paths mostly share their folder, and a
// folder already seen has its parents seen too, so each folder costs one look.
func foldersOf(paths []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	last := ""
	for _, p := range paths {
		dir := p[:strings.LastIndexByte(p, '/')+1]
		if dir == last {
			continue
		}
		last = dir
		for d := dir; d != ""; d = d[:strings.LastIndexByte(d[:len(d)-1], '/')+1] {
			if _, ok := seen[d]; ok {
				break
			}
			seen[d] = struct{}{}
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// prefixRange answers where a sorted list holds the strings that start with
// prefix. They stand together, so two searches find them.
func prefixRange(sorted []string, prefix string) (lo, hi int) {
	lo = sort.SearchStrings(sorted, prefix)
	hi = lo + sort.Search(len(sorted)-lo, func(i int) bool { return !strings.HasPrefix(sorted[lo+i], prefix) })
	return lo, hi
}

func entriesOf(paths []string) []QuickOpenEntry {
	out := make([]QuickOpenEntry, len(paths))
	for i, p := range paths {
		out[i] = QuickOpenEntry{Path: strings.TrimSuffix(p, "/"), Folder: strings.HasSuffix(p, "/")}
	}
	return out
}

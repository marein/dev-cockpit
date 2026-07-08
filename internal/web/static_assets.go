package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
)

type staticAssetManifest struct {
	byURL    map[string]staticAsset
	assetURL map[string]string
	digest   string
}

type staticAsset struct {
	name      string
	content   []byte
	immutable bool
}

func newStaticAssetManifest() (staticAssetManifest, error) {
	staticFiles, err := fs.Sub(staticAssets, "static")
	if err != nil {
		return staticAssetManifest{}, err
	}

	files := make(map[string][]byte)
	if err := fs.WalkDir(staticFiles, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(staticFiles, name)
		if err != nil {
			return err
		}
		files[name] = content
		return nil
	}); err != nil {
		return staticAssetManifest{}, fmt.Errorf("read static assets: %w", err)
	}

	manifest := staticAssetManifest{
		byURL:    make(map[string]staticAsset),
		assetURL: make(map[string]string),
	}
	// manifest.json and sw.js reference other assets by their raw paths, so
	// they get those references rewritten to the hashed URLs after every
	// other asset has one.
	rewritten := []string{"manifest.json", "sw.js"}
	for name, content := range files {
		if slices.Contains(rewritten, name) {
			continue
		}
		manifest.add(name, content)
	}
	for _, name := range rewritten {
		if content, ok := files[name]; ok {
			manifest.add(name, rewriteAssetRefs(content, manifest.assetURL))
		}
	}

	// A build id over the whole hashed asset set. It changes on any asset change,
	// so a long lived tab can tell its head (which pe.js never swaps) is stale.
	manifest.digest = assetDigest(manifest.assetURL)

	return manifest, nil
}

func assetDigest(assetURL map[string]string) string {
	keys := make([]string, 0, len(assetURL))
	for ref := range assetURL {
		keys = append(keys, ref)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, ref := range keys {
		h.Write([]byte(ref + "=" + assetURL[ref] + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (m staticAssetManifest) add(name string, content []byte) {
	originalURL := "/" + name
	hashedURL := hashedAssetURL(name, content)
	m.byURL[originalURL] = staticAsset{name: name, content: content}
	m.byURL[hashedURL] = staticAsset{name: name, content: content, immutable: true}
	m.assetURL[originalURL] = hashedURL
}

func (m staticAssetManifest) assetPath(assetPath string) string {
	if url, ok := m.assetURL[assetPath]; ok {
		return url
	}
	return assetPath
}

func hashedAssetURL(name string, content []byte) string {
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])[:12]
	ext := path.Ext(name)
	return "/" + strings.TrimSuffix(name, ext) + "." + hash + ext
}

func rewriteAssetRefs(content []byte, assetURL map[string]string) []byte {
	refs := make([]string, 0, len(assetURL))
	for ref := range assetURL {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return len(refs[i]) > len(refs[j]) })

	out := string(content)
	for _, ref := range refs {
		out = strings.ReplaceAll(out, ref, assetURL[ref])
	}
	return []byte(out)
}

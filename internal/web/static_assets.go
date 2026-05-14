package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

type staticAssetManifest struct {
	byURL    map[string]staticAsset
	assetURL map[string]string
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
	for name, content := range files {
		if name == "manifest.json" {
			continue
		}
		manifest.add(name, content)
	}
	if content, ok := files["manifest.json"]; ok {
		manifest.add("manifest.json", rewriteAssetRefs(content, manifest.assetURL))
	}

	return manifest, nil
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

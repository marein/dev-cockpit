package update

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// FeedFormat is one release feed dialect. It dresses the request the way the
// feed wants it and maps the feed's JSON onto the neutral release model.
// Everything past that point, semver ordering, pending, checksums, download,
// swap, smoke test and notes rendering, is the same for every dialect.
type FeedFormat struct {
	name    string
	prepare func(req *http.Request)
	decode  func(r io.Reader) ([]release, error)
}

// feedFormats are the values the updateFeedFormat build flag may name.
var feedFormats = map[string]FeedFormat{
	"github": githubFormat,
	"gitlab": gitlabFormat,
}

// ParseFeedFormat resolves the updateFeedFormat build flag. An unknown value
// is a build configuration error and refuses loudly: a silent fallback would
// read the feed with the wrong mapping and report no update forever.
func ParseFeedFormat(name string) (FeedFormat, error) {
	if format, ok := feedFormats[name]; ok {
		return format, nil
	}
	known := make([]string, 0, len(feedFormats))
	for k := range feedFormats {
		known = append(known, k)
	}
	sort.Strings(known)
	return FeedFormat{}, fmt.Errorf("unknown update feed format %q, known values: %s", name, strings.Join(known, ", "))
}

// release is the provider neutral picture of one published release. The feed
// format mappers fill it, everything downstream reads only this.
type release struct {
	// Tag is the version tag as the feed published it, with or without the v.
	Tag   string
	Name  string
	Notes string
	Date  string
	// Prerelease keeps a release out of pending: GitHub's prereleases and
	// drafts, GitLab's upcoming releases.
	Prerelease bool
	Assets     []releaseAsset
}

// releaseAsset is one downloadable file of a release.
type releaseAsset struct {
	Name string
	URL  string
}

package update

import (
	"encoding/json"
	"io"
	"net/http"
)

// githubFormat reads a GitHub releases feed, the shape of
// https://api.github.com/repos/<owner>/<name>/releases.
var githubFormat = FeedFormat{
	name: "github",
	prepare: func(req *http.Request) {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	},
	decode: decodeGitHub,
}

// ghRelease is one entry of the GitHub releases JSON.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	PublishedAt string    `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// decodeGitHub maps the GitHub JSON onto the neutral model. A draft is not
// installable and folds into the prerelease flag, which is what keeps both
// out of pending.
func decodeGitHub(r io.Reader) ([]release, error) {
	var rels []ghRelease
	if err := json.NewDecoder(r).Decode(&rels); err != nil {
		return nil, err
	}
	out := make([]release, 0, len(rels))
	for _, gh := range rels {
		rel := release{
			Tag:        gh.TagName,
			Name:       gh.Name,
			Notes:      gh.Body,
			Date:       gh.PublishedAt,
			Prerelease: gh.Draft || gh.Prerelease,
		}
		for _, a := range gh.Assets {
			rel.Assets = append(rel.Assets, releaseAsset{Name: a.Name, URL: a.URL})
		}
		out = append(out, rel)
	}
	return out, nil
}

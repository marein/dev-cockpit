package update

import (
	"encoding/json"
	"io"
	"net/http"
)

// gitlabFormat reads a GitLab releases feed, the shape of
// https://gitlab.com/api/v4/projects/<id>/releases. The feed wants no headers
// beyond the plain request.
var gitlabFormat = FeedFormat{
	name:    "gitlab",
	prepare: func(req *http.Request) {},
	decode:  decodeGitLab,
}

// glRelease is one entry of the GitLab releases JSON.
type glRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ReleasedAt  string `json:"released_at"`
	Upcoming    bool   `json:"upcoming_release"`
	Assets      struct {
		Links []glLink `json:"links"`
	} `json:"assets"`
}

type glLink struct {
	Name string `json:"name"`
	URL  string `json:"direct_asset_url"`
}

// decodeGitLab maps the GitLab JSON onto the neutral model. An upcoming
// release is announced ahead of its date and reads as a prerelease, so it
// stays out of pending until it is real.
func decodeGitLab(r io.Reader) ([]release, error) {
	var rels []glRelease
	if err := json.NewDecoder(r).Decode(&rels); err != nil {
		return nil, err
	}
	out := make([]release, 0, len(rels))
	for _, gl := range rels {
		rel := release{
			Tag:        gl.TagName,
			Name:       gl.Name,
			Notes:      gl.Description,
			Date:       gl.ReleasedAt,
			Prerelease: gl.Upcoming,
		}
		for _, l := range gl.Assets.Links {
			rel.Assets = append(rel.Assets, releaseAsset{Name: l.Name, URL: l.URL})
		}
		out = append(out, rel)
	}
	return out, nil
}

package update

import (
	"strings"
	"testing"
)

// The env var DEV_COCKPIT_UPDATE_API_URL reaches only a dev build. A release
// build takes the feed URL injected at build time and nothing else, so a
// stray environment cannot point it at another feed.
func TestResolveFeedURL(t *testing.T) {
	t.Run("a dev build honors the env override", func(t *testing.T) {
		t.Setenv(apiURLEnv, "http://env.test/releases")
		got := resolveFeedURL("http://injected.test/releases", true)
		if got != "http://env.test/releases" {
			t.Fatalf("resolved %q, want the env URL", got)
		}
	})
	t.Run("a release build ignores the env override", func(t *testing.T) {
		t.Setenv(apiURLEnv, "http://env.test/releases")
		got := resolveFeedURL("http://injected.test/releases", false)
		if got != "http://injected.test/releases" {
			t.Fatalf("resolved %q, want the injected URL", got)
		}
	})
	t.Run("a dev build without the env var keeps the injected URL", func(t *testing.T) {
		t.Setenv(apiURLEnv, "")
		got := resolveFeedURL("http://injected.test/releases", true)
		if got != "http://injected.test/releases" {
			t.Fatalf("resolved %q, want the injected URL", got)
		}
	})
}

// The updateFeedFormat build flag resolves to a known dialect or refuses.
// There is no silent fallback: a feed read with the wrong mapping reports no
// update forever.
func TestParseFeedFormat(t *testing.T) {
	for _, name := range []string{"github", "gitlab"} {
		format, err := ParseFeedFormat(name)
		if err != nil {
			t.Fatalf("ParseFeedFormat(%q): %v", name, err)
		}
		if format.name != name {
			t.Fatalf("ParseFeedFormat(%q) resolved to %q", name, format.name)
		}
	}
	_, err := ParseFeedFormat("bitbucket")
	if err == nil {
		t.Fatal("ParseFeedFormat accepted an unknown value")
	}
	for _, want := range []string{"unknown update feed format", "bitbucket", "github", "gitlab"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err, want)
		}
	}
}

func TestDecodeGitHub(t *testing.T) {
	sample := `[
		{
			"tag_name": "v1.2.3",
			"name": "Release 1.2.3",
			"body": "The notes.",
			"published_at": "2026-01-02T03:04:05Z",
			"draft": false,
			"prerelease": false,
			"assets": [
				{"name": "dev-cockpit_1.2.3_linux_amd64.tar.gz", "browser_download_url": "https://github.test/bin.tar.gz"},
				{"name": "dev-cockpit_1.2.3_checksums.txt", "browser_download_url": "https://github.test/sums.txt"}
			]
		},
		{"tag_name": "v1.3.0", "draft": true, "prerelease": false, "assets": []},
		{"tag_name": "v1.4.0", "draft": false, "prerelease": true, "assets": []}
	]`
	rels, err := decodeGitHub(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("decoded %d releases, want 3", len(rels))
	}
	got := rels[0]
	want := release{
		Tag:   "v1.2.3",
		Name:  "Release 1.2.3",
		Notes: "The notes.",
		Date:  "2026-01-02T03:04:05Z",
		Assets: []releaseAsset{
			{Name: "dev-cockpit_1.2.3_linux_amd64.tar.gz", URL: "https://github.test/bin.tar.gz"},
			{Name: "dev-cockpit_1.2.3_checksums.txt", URL: "https://github.test/sums.txt"},
		},
	}
	assertRelease(t, got, want)
	if !rels[1].Prerelease {
		t.Fatal("a draft did not read as a prerelease")
	}
	if !rels[2].Prerelease {
		t.Fatal("a prerelease did not read as one")
	}
}

func TestDecodeGitLab(t *testing.T) {
	sample := `[
		{
			"tag_name": "v1.2.3",
			"name": "Release 1.2.3",
			"description": "The notes.",
			"released_at": "2026-01-02T03:04:05Z",
			"upcoming_release": false,
			"assets": {
				"count": 2,
				"links": [
					{"id": 1, "name": "dev-cockpit_1.2.3_linux_amd64.tar.gz", "url": "https://gitlab.test/permalink.tar.gz", "direct_asset_url": "https://gitlab.test/bin.tar.gz", "link_type": "other"},
					{"id": 2, "name": "dev-cockpit_1.2.3_checksums.txt", "url": "https://gitlab.test/permalink.txt", "direct_asset_url": "https://gitlab.test/sums.txt", "link_type": "other"}
				]
			}
		},
		{"tag_name": "v2.0.0", "upcoming_release": true, "assets": {"count": 0, "links": []}}
	]`
	rels, err := decodeGitLab(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("decoded %d releases, want 2", len(rels))
	}
	got := rels[0]
	want := release{
		Tag:   "v1.2.3",
		Name:  "Release 1.2.3",
		Notes: "The notes.",
		Date:  "2026-01-02T03:04:05Z",
		Assets: []releaseAsset{
			{Name: "dev-cockpit_1.2.3_linux_amd64.tar.gz", URL: "https://gitlab.test/bin.tar.gz"},
			{Name: "dev-cockpit_1.2.3_checksums.txt", URL: "https://gitlab.test/sums.txt"},
		},
	}
	assertRelease(t, got, want)
	if !rels[1].Prerelease {
		t.Fatal("an upcoming release did not read as a prerelease")
	}
}

func assertRelease(t *testing.T, got, want release) {
	t.Helper()
	if got.Tag != want.Tag || got.Name != want.Name || got.Notes != want.Notes || got.Date != want.Date || got.Prerelease != want.Prerelease {
		t.Fatalf("decoded %+v, want %+v", got, want)
	}
	if len(got.Assets) != len(want.Assets) {
		t.Fatalf("decoded %d assets, want %d", len(got.Assets), len(want.Assets))
	}
	for i := range want.Assets {
		if got.Assets[i] != want.Assets[i] {
			t.Fatalf("asset %d is %+v, want %+v", i, got.Assets[i], want.Assets[i])
		}
	}
}

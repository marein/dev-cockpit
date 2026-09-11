package markdown

import (
	"strings"
	"testing"
)

func sizedResolver(media Media) MediaResolver {
	return func(destination string) (Media, bool) {
		if strings.HasPrefix(destination, "shot") {
			return media, true
		}
		return Media{}, false
	}
}

// A picture an answer points at renders with its pixel size, which is what
// holds its space open while the browser is still loading it.
func TestMediaImageCarriesItsSize(t *testing.T) {
	html, err := RenderGFMWithMedia("![the board](shot.png)\n", sizedResolver(Media{URL: "/media/shot.png", Kind: "image", Width: 1280, Height: 800}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{`src="/media/shot.png"`, `alt="the board"`, `class="dc-assistant-media"`, `width="1280"`, `height="800"`, `style="--dc-media-ratio:1280/800"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %s in %q", want, html)
		}
	}
}

// The same for the other way an answer points at a picture, a link the
// resolver claims: that one this package renders itself.
func TestMediaLinkToAnImageCarriesItsSize(t *testing.T) {
	html, err := RenderGFMWithMedia("[the board](shot.png)\n", sizedResolver(Media{URL: "/media/shot.png", Kind: "image", Width: 640, Height: 400}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{`class="dc-assistant-media"`, `width="640"`, `height="400"`, `style="--dc-media-ratio:640/400"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %s in %q", want, html)
		}
	}
}

// A size the resolver could not read says nothing at all: no empty attributes,
// no zeros, because a zero box is a picture the browser would never show.
func TestMediaWithoutASizeSaysNothing(t *testing.T) {
	for _, src := range []string{"![the board](shot.png)\n", "[the board](shot.png)\n"} {
		html, err := RenderGFMWithMedia(src, sizedResolver(Media{URL: "/media/shot.png", Kind: "image"}))
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if strings.Contains(html, "width=") || strings.Contains(html, "height=") || strings.Contains(html, "--dc-media-ratio") {
			t.Fatalf("want no size in %q", html)
		}
	}
}

// Everything the resolver does not claim keeps the plain rendering, size or
// not: an image on the web is nobody's file here.
func TestMediaLeavesForeignImagesAlone(t *testing.T) {
	html, err := RenderGFMWithMedia("![out there](https://example.test/a.png)\n", sizedResolver(Media{URL: "/media/shot.png", Kind: "image", Width: 10, Height: 10}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, `src="https://example.test/a.png"`) || strings.Contains(html, "width=") {
		t.Fatalf("the foreign image was touched: %q", html)
	}
}

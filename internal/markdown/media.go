package markdown

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Media describes how one file an answer points at should be shown.
type Media struct {
	// URL is where the browser loads the file.
	URL string
	// Kind is image, video, audio or file.
	Kind string
	// Width and Height are the pixel size of a still image, zero when the
	// resolver could not read it. They are rendered as the image's attributes,
	// which is what reserves its space before it has arrived: without them
	// every picture in a long answer pushes the text under it down the moment
	// it decodes.
	Width  int
	Height int
}

// MediaResolver maps a destination as written in the Markdown onto a Media.
// Returning false leaves the node to the default rendering, which is what
// every external link and image gets.
type MediaResolver func(destination string) (Media, bool)

// RenderGFMWithMedia renders Markdown like RenderGFM, except that a link or an
// image pointing at a file the resolver claims becomes a real player: an
// answer that writes ![](out.mp4) gets a video element instead of a broken
// image. Raw HTML stays disabled, so model output still cannot inject markup,
// and every URL in the result comes from the resolver, never from the model.
func RenderGFMWithMedia(src string, resolve MediaResolver) (string, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithASTTransformers(
			util.Prioritized(&mediaTransformer{resolve: resolve}, 100),
		)),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(
			util.Prioritized(&mediaRenderer{}, 100),
		)),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// mediaClass is what the stylesheet sizes a picture or a clip by, the same
// class on every one of them, whether this package renders it or goldmark
// does.
const mediaClass = "dc-assistant-media"

// kindMedia is the node the transformer puts in place of a claimed link or
// image that is not a still image. A dedicated node keeps the default
// renderers untouched for everything else.
var kindMedia = ast.NewNodeKind("DevCockpitMedia")

type mediaNode struct {
	ast.BaseInline
	media Media
	label string
}

func (n *mediaNode) Kind() ast.NodeKind { return kindMedia }

func (n *mediaNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"url": n.media.URL, "kind": n.media.Kind}, nil)
}

// mediaTransformer rewrites the claimed destinations. A still image only needs
// its destination and its size set, goldmark's own image renderer then does
// the right thing; the other kinds are replaced by a media node.
type mediaTransformer struct{ resolve MediaResolver }

func (t *mediaTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	type replacement struct {
		parent ast.Node
		node   ast.Node
		with   *mediaNode
	}
	var pending []replacement

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var destination []byte
		var label string
		switch node := n.(type) {
		case *ast.Image:
			destination, label = node.Destination, string(node.Text(source))
		case *ast.Link:
			destination, label = node.Destination, string(nodeText(node, source))
		default:
			return ast.WalkContinue, nil
		}
		media, ok := t.resolve(string(destination))
		if !ok {
			return ast.WalkContinue, nil
		}
		if media.Kind == "image" {
			if image, isImage := n.(*ast.Image); isImage {
				image.Destination = []byte(media.URL)
				image.SetAttributeString("class", mediaClass)
				setSize(image, media)
				return ast.WalkContinue, nil
			}
		}
		if label == "" {
			label = media.URL
		}
		pending = append(pending, replacement{parent: n.Parent(), node: n, with: &mediaNode{media: media, label: label}})
		return ast.WalkSkipChildren, nil
	})

	for _, r := range pending {
		if r.parent != nil {
			r.parent.ReplaceChild(r.parent, r.node, r.with)
		}
	}
}

// setSize puts the pixel size on an image node. goldmark's own image renderer
// writes the attributes out of the node's attributes, so the picture carries
// its box without this package rendering it. A resolver that does not know the
// size sets nothing, and the image renders as it did before.
func setSize(image *ast.Image, media Media) {
	if media.Width <= 0 || media.Height <= 0 {
		return
	}
	image.SetAttributeString("width", strconv.Itoa(media.Width))
	image.SetAttributeString("height", strconv.Itoa(media.Height))
	image.SetAttributeString("style", mediaRatioStyle(media))
}

// sizeAttributes is the same box for the image this package renders itself, a
// link that points at a picture.
func sizeAttributes(media Media) string {
	if media.Width <= 0 || media.Height <= 0 {
		return ""
	}
	return fmt.Sprintf(` width="%d" height="%d" style="%s"`, media.Width, media.Height, mediaRatioStyle(media))
}

// mediaRatioStyle hands the stylesheet the ratio the attributes describe. The
// two are not the same thing to a browser: the width attribute is a width the
// layout may no longer choose, so a cap on the height clamps the height alone
// and draws the picture out of shape. With the ratio the stylesheet writes
// that cap as the width it allows, and the height follows from it. A picture
// whose size nobody could read carries neither, and both stay auto, which is
// the one case a browser keeps the ratio by itself.
func mediaRatioStyle(media Media) string {
	return fmt.Sprintf("--dc-media-ratio:%d/%d", media.Width, media.Height)
}

// mediaRenderer renders the media node. preload is metadata on purpose: a
// phone opening a long transcript must not start pulling every video in it.
type mediaRenderer struct{}

func (r *mediaRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMedia, r.render)
}

func (r *mediaRenderer) render(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*mediaNode)
	url := util.EscapeHTML(util.URLEscape([]byte(n.media.URL), true))
	label := util.EscapeHTML([]byte(n.label))
	switch n.media.Kind {
	case "image":
		fmt.Fprintf(w, `<a href="%s" target="_blank" rel="noopener"><img src="%s" alt="%s" class="%s"%s></a>`, url, url, label, mediaClass, sizeAttributes(n.media))
	case "video":
		fmt.Fprintf(w, `<video src="%s" class="%s" controls playsinline preload="metadata"></video>`, url, mediaClass)
	case "audio":
		fmt.Fprintf(w, `<audio src="%s" class="dc-assistant-audio" controls preload="metadata"></audio>`, url)
	default:
		fmt.Fprintf(w, `<a href="%s" download>%s</a>`, url, label)
	}
	return ast.WalkSkipChildren, nil
}

// nodeText collects the plain text under a node, used as a link label.
func nodeText(n ast.Node, source []byte) []byte {
	var b bytes.Buffer
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			b.Write(t.Segment.Value(source))
			continue
		}
		b.Write(nodeText(child, source))
	}
	return b.Bytes()
}

package markdown

import (
	"bytes"
	"fmt"

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
// its destination replaced, goldmark's own image renderer then does the right
// thing; the other kinds are replaced by a media node.
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
		fmt.Fprintf(w, `<a href="%s" target="_blank" rel="noopener"><img src="%s" alt="%s" class="dc-assistant-media"></a>`, url, url, label)
	case "video":
		fmt.Fprintf(w, `<video src="%s" class="dc-assistant-media" controls playsinline preload="metadata"></video>`, url)
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

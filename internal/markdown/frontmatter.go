package markdown

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

func SplitFrontMatter(data []byte) (meta, body []byte) {
	data = bytes.TrimPrefix(data, []byte("\ufeff"))
	const sep = "---"
	first := bytes.IndexByte(data, '\n')
	if first < 0 || strings.TrimSpace(string(data[:first])) != sep {
		return nil, data
	}
	rest := data[first+1:]
	closeIdx := bytes.Index(rest, []byte("\n"+sep))
	if closeIdx < 0 {
		return nil, data
	}
	meta = rest[:closeIdx]
	body = rest[closeIdx+len("\n"+sep):]
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = nil
	}
	return meta, body
}

func WriteFrontMatter(frontMatter any, body string) ([]byte, error) {
	meta, err := yaml.Marshal(frontMatter)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(meta)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteByte('\n')
	return b.Bytes(), nil
}

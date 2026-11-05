package editorintelligence

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf16"
)

// intelephenseStubs is where the server's own stubs lie in its image. The
// server indexes them as a workspace folder ahead of the project, so they
// name the index directory too, see intelephenseIndexDir.
const intelephenseStubs = "/usr/local/lib/node_modules/intelephense/lib/stub"

// intelephenseSeedVersion is the server version whose index format the
// seed was verified against. An index another version wrote is never
// seeded from, a test holds it equal to the pinned server.
const intelephenseSeedVersion = "1.18.5"

// intelephenseIndexFiles is how many files one index holds, named
// intelephense_data_1 to _9. Only the first, second and ninth name paths.
const intelephenseIndexFiles = 9

func intelephenseIndexFile(n int) string {
	return "intelephense_data_" + strconv.Itoa(n)
}

// intelephenseIndexDir is the directory below the storage path the server
// keeps a workspace's index in: FNV-1a over the UTF-16 units of a space and
// the folder URIs, stubs first, read as a signed 32 bit number whose
// absolute value is written in hex.
func intelephenseIndexDir(root string) string {
	hash := uint32(2166136261)
	for _, unit := range utf16.Encode([]rune(" " + fileURI(intelephenseStubs) + fileURI(root))) {
		hash ^= uint32(unit)
		hash *= 16777619
	}
	n := int64(int32(hash))
	if n < 0 {
		n = -n
	}
	return strconv.FormatInt(n, 16)
}

// plainPath reports whether every rune of the path stays itself in a file
// URI, under the cockpit's encoding and the server's alike. Only then do
// the URIs and the index directory computed here match the server's.
func plainPath(path string) bool {
	if path == "" {
		return false
	}
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '.', r == '_', r == '~', r == '-':
		default:
			return false
		}
	}
	return true
}

// indexDocument is one entry of intelephense_data_2, a length delimited
// DocumentIdentifier: the URI (field 1), the time the server indexed it in
// milliseconds (field 4), and every other field as it stood.
type indexDocument struct {
	raw     []byte
	uri     string
	indexed uint64
	rest    []byte
}

var errIndexFormat = errors.New("the index has an unknown shape")

func parseIndexDocuments(data []byte) ([]indexDocument, error) {
	var docs []indexDocument
	for len(data) > 0 {
		size, n := binary.Uvarint(data)
		if n <= 0 || size > uint64(len(data)-n) {
			return nil, errIndexFormat
		}
		msg := data[n : n+int(size)]
		data = data[n+int(size):]
		doc, err := parseIndexDocument(msg)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func parseIndexDocument(msg []byte) (indexDocument, error) {
	doc := indexDocument{raw: msg}
	hasURI := false
	for rest := msg; len(rest) > 0; {
		tag, n := binary.Uvarint(rest)
		if n <= 0 {
			return doc, errIndexFormat
		}
		field, wire := tag>>3, tag&7
		size := n
		var value uint64
		switch wire {
		case 0:
			v, m := binary.Uvarint(rest[n:])
			if m <= 0 {
				return doc, errIndexFormat
			}
			value, size = v, n+m
		case 1:
			size = n + 8
		case 2:
			l, m := binary.Uvarint(rest[n:])
			if m <= 0 || l > uint64(len(rest)-n-m) {
				return doc, errIndexFormat
			}
			size = n + m + int(l)
			if field == 1 {
				doc.uri, hasURI = string(rest[n+m:size]), true
			}
		case 5:
			size = n + 4
		default:
			return doc, errIndexFormat
		}
		if size > len(rest) {
			return doc, errIndexFormat
		}
		switch {
		case field == 1 && wire == 2:
		case field == 4 && wire == 0:
			doc.indexed = value
		default:
			doc.rest = append(doc.rest, rest[:size]...)
		}
		rest = rest[size:]
	}
	if !hasURI {
		return doc, errIndexFormat
	}
	return doc, nil
}

// appendIndexDocument writes the document under a new URI and indexed
// time.
func appendIndexDocument(out []byte, doc indexDocument, uri string, indexed uint64) []byte {
	msg := binary.AppendUvarint(nil, 1<<3|2)
	msg = binary.AppendUvarint(msg, uint64(len(uri)))
	msg = append(msg, uri...)
	msg = append(msg, doc.rest...)
	msg = binary.AppendUvarint(msg, 4<<3)
	msg = binary.AppendUvarint(msg, indexed)
	out = binary.AppendUvarint(out, uint64(len(msg)))
	return append(out, msg...)
}

func appendRawIndexDocument(out []byte, doc indexDocument) []byte {
	out = binary.AppendUvarint(out, uint64(len(doc.raw)))
	return append(out, doc.raw...)
}

// intelephenseMeta is what intelephense_data_1 says about its index.
type intelephenseMeta struct {
	Timestamp   int64 `json:"timestamp"`
	Environment struct {
		DocumentRoot string `json:"documentRoot"`
	} `json:"environment"`
	Version string `json:"intelephenseVersion"`
}

func readIntelephenseMeta(data []byte) (intelephenseMeta, error) {
	var meta intelephenseMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, errIndexFormat
	}
	return meta, nil
}

// rewriteIntelephenseMeta moves intelephense_data_1 from the source's
// workspace to the target's, every other key kept as it stood.
func rewriteIntelephenseMeta(data []byte, from, to string, timestamp int64) ([]byte, error) {
	meta, err := readIntelephenseMeta(data)
	if err != nil {
		return nil, err
	}
	if meta.Version != intelephenseSeedVersion {
		return nil, fmt.Errorf("its index was written by intelephense %q", meta.Version)
	}
	if meta.Environment.DocumentRoot != fileURI(from) {
		return nil, errIndexFormat
	}
	var top, env map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil || json.Unmarshal(top["environment"], &env) != nil {
		return nil, errIndexFormat
	}
	var werr error
	env["documentRoot"] = plainJSON(fileURI(to), &werr)
	top["environment"] = plainJSON(env, &werr)
	top["timestamp"] = plainJSON(timestamp, &werr)
	out := plainJSON(top, &werr)
	return out, werr
}

// rewriteIntelephenseConfig moves intelephense_data_9 from the source's
// workspace folder to the target's. A folder of any other name is a shape
// the seed does not know.
func rewriteIntelephenseConfig(data []byte, from, to string) ([]byte, error) {
	var top map[string]json.RawMessage
	var folders [][]json.RawMessage
	if json.Unmarshal(data, &top) != nil || json.Unmarshal(top["folderConfigMap"], &folders) != nil {
		return nil, errIndexFormat
	}
	var werr error
	for _, pair := range folders {
		var uri string
		if len(pair) != 2 || json.Unmarshal(pair[0], &uri) != nil || uri != fileURI(from) {
			return nil, errIndexFormat
		}
		pair[0] = plainJSON(fileURI(to), &werr)
	}
	top["folderConfigMap"] = plainJSON(folders, &werr)
	out := plainJSON(top, &werr)
	return out, werr
}

// plainJSON encodes the way the server's JSON.stringify does, nothing
// escaped for HTML, and keeps the first error.
func plainJSON(v any, werr *error) json.RawMessage {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil && *werr == nil {
		*werr = err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

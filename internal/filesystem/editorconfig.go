package filesystem

import (
	"strconv"

	editorconfig "github.com/editorconfig/editorconfig-core-go/v2"
)

// EditorConfig holds the indentation properties EditorConfig resolves for one
// file. A zero field means the property is unset, so the client keeps its own
// default for it.
type EditorConfig struct {
	IndentStyle string `json:"indentStyle,omitempty"` // "tab" or "space"
	IndentSize  int    `json:"indentSize,omitempty"`  // columns; 0 when unset or "tab"
	TabWidth    int    `json:"tabWidth,omitempty"`
}

// EditorConfigForFile resolves the EditorConfig indentation properties that
// apply to root/rel by walking the .editorconfig cascade upward from the file.
// A missing, unreadable or malformed config yields a zero EditorConfig, never
// an error: editorconfig is advisory and must not block opening a file.
func EditorConfigForFile(root, rel string) EditorConfig {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return EditorConfig{}
	}
	def, err := editorconfig.GetDefinitionForFilename(target)
	if err != nil || def == nil {
		return EditorConfig{}
	}
	ec := EditorConfig{
		IndentStyle: def.IndentStyle,
	}
	if n, err := strconv.Atoi(def.IndentSize); err == nil {
		ec.IndentSize = n
	}
	// Only an explicit tab_width is authoritative. def.TabWidth also folds in
	// indent_size (the editorconfig default), which would wrongly override the
	// user's tab width setting for tab indented files that merely inherit an
	// indent_size, so read the raw key instead.
	if n, err := strconv.Atoi(def.Raw["tab_width"]); err == nil {
		ec.TabWidth = n
	}
	return ec
}

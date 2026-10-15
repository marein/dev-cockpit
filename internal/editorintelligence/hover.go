package editorintelligence

import (
	"encoding/json"
	"strings"
)

// hoverText is the text of a hover answer, empty for none, and the range of
// the symbol it describes when the server names one. The text is the value
// of its MarkupContent, or the older MarkedString forms one after another, a
// language tagged one written as the fenced block a markdown hover holds.
func hoverText(raw json.RawMessage) (string, *lspRange, error) {
	if trimmed := strings.TrimSpace(string(raw)); trimmed == "" || trimmed == "null" {
		return "", nil, nil
	}
	var hover struct {
		Contents json.RawMessage `json:"contents"`
		Range    *lspRange       `json:"range"`
	}
	if err := json.Unmarshal(raw, &hover); err != nil {
		return "", nil, err
	}
	text, err := markedText(hover.Contents)
	return text, hover.Range, err
}

func markedText(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "" || trimmed == "null":
		return "", nil
	case strings.HasPrefix(trimmed, `"`):
		var text string
		err := json.Unmarshal(raw, &text)
		return text, err
	case strings.HasPrefix(trimmed, "["):
		var parts []json.RawMessage
		if err := json.Unmarshal(raw, &parts); err != nil {
			return "", err
		}
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			text, err := markedText(part)
			if err != nil {
				return "", err
			}
			if text != "" {
				texts = append(texts, text)
			}
		}
		return strings.Join(texts, "\n\n"), nil
	}
	var content struct {
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(raw, &content); err != nil {
		return "", err
	}
	if content.Language != "" {
		return "```" + content.Language + "\n" + content.Value + "\n```", nil
	}
	return content.Value, nil
}

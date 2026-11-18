package assistant

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// Picked only changes how the thread shows a message, the assistant reads the
// message's Content. Every text in it is the page's own and stays plain text.
type Picked struct {
	Note     string          `json:"note,omitempty"`
	Title    string          `json:"title,omitempty"`
	Address  string          `json:"address,omitempty"`
	Viewport string          `json:"viewport,omitempty"`
	Pages    int             `json:"pages,omitempty"`
	Chips    []PickedChip    `json:"chips"`
	Sections []PickedSection `json:"sections"`
}

type PickedChip struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type PickedSection struct {
	Kind    string        `json:"kind"`
	Title   string        `json:"title"`
	Rows    []PickedRow   `json:"rows,omitempty"`
	Lines   []PickedLine  `json:"lines,omitempty"`
	Blocks  []PickedBlock `json:"blocks,omitempty"`
	Picture string        `json:"picture,omitempty"`
}

type PickedRow struct {
	Term  string `json:"term"`
	Value string `json:"value"`
	Code  bool   `json:"code,omitempty"`
}

type PickedLine struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	Meta string `json:"meta,omitempty"`
}

type PickedBlock struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// A card holds what its message holds, at most MaxPromptBytes, plus its
// JSON shape.
const MaxPickedBytes = 64 << 10

const (
	maxPickedChips    = 4
	maxPickedSections = 20
	maxPickedRows     = 12
	maxPickedLines    = 60
	maxPickedBlocks   = 3
)

var (
	pickedChipKinds    = []string{"element", "error", "warn", "image"}
	pickedSectionKinds = []string{"element", "log", "image"}
	pickedLineKinds    = []string{"error", "warn", "nav", "mark"}
)

var errPicked = errors.New("The card could not be read.")

func ParsePicked(raw string) (Picked, error) {
	if len(raw) > MaxPickedBytes {
		return Picked{}, errors.New("The card is too long.")
	}
	var p Picked
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Picked{}, errPicked
	}
	return p, nil
}

// A card has to fit its message: the note begins the message and every
// picture is one of its files.
func (p Picked) check(text string, attachments []Attachment) error {
	if !strings.HasPrefix(text, p.Note) {
		return errors.New("The card does not match the message.")
	}
	ok := within(p.Title, 200) && within(p.Address, 2048) && within(p.Viewport, 40) &&
		p.Pages >= 0 && p.Pages <= maxPickedSections &&
		len(p.Chips) <= maxPickedChips && len(p.Sections) > 0 && len(p.Sections) <= maxPickedSections
	for _, chip := range p.Chips {
		ok = ok && slices.Contains(pickedChipKinds, chip.Kind) && within(chip.Text, 200)
	}
	for _, s := range p.Sections {
		ok = ok && slices.Contains(pickedSectionKinds, s.Kind) && within(s.Title, 300) &&
			len(s.Rows) <= maxPickedRows && len(s.Lines) <= maxPickedLines && len(s.Blocks) <= maxPickedBlocks
		for _, row := range s.Rows {
			ok = ok && within(row.Term, 40) && within(row.Value, 2400)
		}
		for _, line := range s.Lines {
			ok = ok && slices.Contains(pickedLineKinds, line.Kind) && within(line.Text, 2400) && within(line.Meta, 200)
		}
		for _, block := range s.Blocks {
			ok = ok && within(block.Label, 40) && within(block.Text, 9000)
		}
		if s.Picture != "" && !slices.ContainsFunc(attachments, func(a Attachment) bool { return a.Name == s.Picture }) {
			return errors.New("A drawing's picture is not attached.")
		}
	}
	if !ok {
		return errPicked
	}
	var encoded bytes.Buffer
	enc := json.NewEncoder(&encoded)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil || encoded.Len() > MaxPickedBytes {
		return errors.New("The card is too long.")
	}
	return nil
}

func within(s string, runes int) bool {
	return utf8.RuneCountInString(s) <= runes
}

func (p *Picked) Pictures() []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, s := range p.Sections {
		if s.Picture != "" {
			out = append(out, s.Picture)
		}
	}
	return out
}

func (s *Service) PromptPicked(id, prompt string, picked Picked, via string, attachments ...Attachment) (Run, error) {
	text, err := validatePrompt(prompt, len(attachments))
	if err != nil {
		return Run{}, err
	}
	picked.Note = strings.TrimSpace(picked.Note)
	if err := picked.check(text, attachments); err != nil {
		return Run{}, err
	}
	return s.send(id, false, func(Instance) (Message, error) {
		return Message{Content: text, Attachments: attachments, Picked: &picked, Via: via}, nil
	})
}

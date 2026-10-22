package assistant

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/markdown"
)

// AskLanguage names the fenced block an answer asks the user with, a
// format instructions.md.tmpl teaches: a question on a line of its own, its
// options under it, one per line behind "- ", or behind "- [ ] " where the
// question takes several picks.
const AskLanguage = "ask"

const (
	// MaxChoiceQuestions bounds the questions of one block, and the answers a
	// send may carry.
	MaxChoiceQuestions = 8
	maxChoiceOptions   = 8
	maxChoiceRunes     = 300
	// MaxChoiceNoteRunes bounds what the user writes beside one question.
	MaxChoiceNoteRunes = 2000
)

// Question is one question of an ask block with the options it offers,
// Multi where it takes several of them.
type Question struct {
	Text    string
	Options []string
	Multi   bool
}

// Choices are the questions an answer asks: the first well formed ask
// block among its top level fenced blocks, Fence its place among them.
type Choices struct {
	Fence     int
	Questions []Question
}

// Pick is the answer to one question: the option picked, -1 for none, the
// options picked where the question takes several, in the question's order,
// and what the user wrote beside it, a note on the options or the answer
// itself.
type Pick struct {
	Option  int    `json:"option"`
	Options []int  `json:"options,omitempty"`
	Text    string `json:"text,omitempty"`
}

// Picked is every option the pick picked, whichever kind of question it
// answers.
func (p Pick) Picked() []int {
	if p.Option >= 0 {
		return []int{p.Option}
	}
	return p.Options
}

func (p Pick) same(other Pick) bool {
	return p.Option == other.Option && p.Text == other.Text && slices.Equal(p.Options, other.Options)
}

// Answers names the answer whose questions a user message answers and what
// was picked there.
type Answers struct {
	Message string `json:"message"`
	Picks   []Pick `json:"picks"`
}

// Choices answers the questions of a finished chat answer. A note and a pushed
// answer ask nothing: the one is the cockpit speaking, the other came from a
// session the user does not talk to. A malformed block is no block, it stays
// the code block it was written as.
func (m Message) Choices() (Choices, bool) {
	if m.Role != RoleAssistant || m.State != StateComplete || m.Note != nil || m.Auto || !strings.Contains(m.Content, AskLanguage) {
		return Choices{}, false
	}
	for i, fence := range markdown.Fences(m.Content) {
		if fence.Language != AskLanguage {
			continue
		}
		if questions, ok := parseChoices(fence.Body); ok {
			return Choices{Fence: i, Questions: questions}, true
		}
	}
	return Choices{}, false
}

// Ends names the answers whose questions the user message at index ends, the
// ones since the user last wrote. A page pulls them again with it: an
// answered block shows the picks, a passed one keeps its card and says Not
// answered.
func Ends(messages []Message, index int) []string {
	var ids []string
	for i := index - 1; i >= 0 && messages[i].Role != RoleUser; i-- {
		if _, ok := messages[i].Choices(); ok {
			ids = append(ids, messages[i].ID)
		}
	}
	return ids
}

func parseChoices(body string) ([]Question, bool) {
	var questions []Question
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if option, ok := strings.CutPrefix(line, "- "); ok {
			option, multi := strings.CutPrefix(strings.TrimSpace(option), "[ ]")
			option = strings.TrimSpace(option)
			if len(questions) == 0 || option == "" || utf8.RuneCountInString(option) > maxChoiceRunes {
				return nil, false
			}
			last := &questions[len(questions)-1]
			if len(last.Options) == maxChoiceOptions || len(last.Options) > 0 && last.Multi != multi {
				return nil, false
			}
			last.Multi = multi
			last.Options = append(last.Options, option)
			continue
		}
		if len(questions) == MaxChoiceQuestions || utf8.RuneCountInString(line) > maxChoiceRunes || !answerable(questions) {
			return nil, false
		}
		questions = append(questions, Question{Text: line})
	}
	return questions, len(questions) > 0 && answerable(questions)
}

func answerable(questions []Question) bool {
	return len(questions) == 0 || len(questions[len(questions)-1].Options) >= 2
}

// ReplyTo answers the first message of the user after the one at index, the
// one that answered it or moved the thread on past it.
func ReplyTo(messages []Message, index int) (Message, bool) {
	for _, m := range messages[index+1:] {
		if m.Role == RoleUser {
			return m, true
		}
	}
	return Message{}, false
}

// answerMessage writes the picks for the questions of one answer as the
// user's message. picks carries one entry per question in order, more only
// when they are empty.
func answerMessage(c Instance, messageID string, picks []Pick) (Message, error) {
	choices, err := openChoices(c, messageID)
	if err != nil {
		return Message{}, err
	}
	clean, err := checkPicks(choices.Questions, picks)
	if err != nil {
		return Message{}, err
	}
	text, err := validatePrompt(answerText(choices.Questions, clean), 0)
	if err != nil {
		return Message{}, err
	}
	return Message{Content: text, Answers: &Answers{Message: messageID, Picks: clean}}, nil
}

// openChoices answers the questions of one answer that still take answers.
func openChoices(c Instance, messageID string) (Choices, error) {
	at := slices.IndexFunc(c.Messages, func(m Message) bool { return m.ID == messageID })
	if at < 0 {
		return Choices{}, errors.New("That answer is not in this assistant.")
	}
	choices, ok := c.Messages[at].Choices()
	if !ok {
		return Choices{}, errors.New("That answer asks nothing.")
	}
	if _, ok := ReplyTo(c.Messages, at); ok {
		return Choices{}, errors.New("These questions were answered already.")
	}
	return choices, nil
}

// SaveAnswers stores what is picked and written in one open block without
// sending it, beside the composer's words in the same draft, and says whether
// that changed anything. The words are kept as typed, a send trims them. A
// repeated save writes nothing and announces nothing, like the composer's.
func (s *Service) SaveAnswers(id, messageID string, picks []Pick) (bool, error) {
	if _, ok := s.store.Load(id); !ok {
		return false, errors.New("Assistant not found.")
	}
	return s.drafts.Of(id).saveAnswers(messageID, func() ([]Pick, error) {
		c, ok := s.store.Load(id)
		if !ok {
			return nil, errors.New("Assistant not found.")
		}
		choices, err := openChoices(c, messageID)
		if err != nil {
			return nil, err
		}
		return fitPicks(choices.Questions, picks)
	})
}

func checkPicks(questions []Question, picks []Pick) ([]Pick, error) {
	trimmed := make([]Pick, len(picks))
	for i, p := range picks {
		trimmed[i] = Pick{Option: p.Option, Options: p.Options, Text: strings.TrimSpace(p.Text)}
	}
	clean, err := fitPicks(questions, trimmed)
	if err != nil {
		return nil, err
	}
	for _, p := range clean {
		if len(p.Picked()) == 0 && p.Text == "" {
			return nil, errors.New("Answer every question first.")
		}
	}
	return clean, nil
}

// fitPicks lays picks over the questions, one per question in order, an
// option of -1 and no words where none came. More than the questions may
// come only when they are empty.
func fitPicks(questions []Question, picks []Pick) ([]Pick, error) {
	fit := make([]Pick, len(questions))
	for i := range fit {
		fit[i].Option = -1
	}
	for i, p := range picks {
		if i >= len(questions) {
			if p.Option >= 0 || len(p.Options) > 0 || strings.TrimSpace(p.Text) != "" {
				return nil, errors.New("There are more answers than questions.")
			}
			continue
		}
		if p.Option < -1 || p.Option >= len(questions[i].Options) {
			return nil, errors.New("That option does not exist.")
		}
		if questions[i].Multi && p.Option >= 0 || !questions[i].Multi && len(p.Options) > 0 {
			return nil, errors.New("That answer does not fit its question.")
		}
		options, err := fitOptions(questions[i], p.Options)
		if err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(p.Text) > MaxChoiceNoteRunes {
			return nil, errors.New("An answer is too long.")
		}
		fit[i] = Pick{Option: p.Option, Options: options, Text: p.Text}
	}
	return fit, nil
}

// fitOptions puts the options picked on a question that takes several into
// the question's order. Each stands once, so a list never grows past the
// options the question offers.
func fitOptions(question Question, options []int) ([]int, error) {
	if len(options) == 0 {
		return nil, nil
	}
	if len(options) > len(question.Options) {
		return nil, errors.New("That option does not exist.")
	}
	sorted := slices.Sorted(slices.Values(options))
	for i, option := range sorted {
		if option < 0 || option >= len(question.Options) {
			return nil, errors.New("That option does not exist.")
		}
		if i > 0 && sorted[i-1] == option {
			return nil, errors.New("An option is picked twice.")
		}
	}
	return sorted, nil
}

// answerText is what the assistant reads: every question with the options
// picked and what was written beside it, plain text that needs no format. A
// question that takes several comes back as its whole list with the picks
// checked, since a joined line could not tell an option holding a comma from
// two options.
func answerText(questions []Question, picks []Pick) string {
	var b strings.Builder
	for i, q := range questions {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(q.Text)
		p := picks[i]
		switch {
		case len(p.Picked()) == 0:
			b.WriteString("\nAnswer in my own words: " + p.Text)
			continue
		case q.Multi:
			for option, text := range q.Options {
				box := "\n- [ ] "
				if slices.Contains(p.Options, option) {
					box = "\n- [x] "
				}
				b.WriteString(box + text)
			}
		default:
			b.WriteString("\nAnswer: " + q.Options[p.Option])
		}
		if p.Text != "" {
			b.WriteString("\nNote: " + p.Text)
		}
	}
	return b.String()
}

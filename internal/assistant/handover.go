package assistant

import "strings"

// Handover is what an assistant hands the one it starts on the user's word:
// CoderID picks the coder that answers, empty for the first installed one,
// Name is what it is called, empty to be named after its first message, and
// Message is that first message, empty for none.
type Handover struct {
	CoderID string
	Name    string
	Message string
}

// CreateWith is Create with a name and a first message, what `assistant-new`
// asks for. The new assistant shares nothing with the one that asked but the
// memory every assistant shares, and its first message is answered the way a
// message the user sent is. The message is checked before anything exists, so
// a refused one leaves nothing behind. Run is the turn that answers it, empty
// without a message; an error after the assistant was made comes back beside
// it, the assistant stands.
func (s *Service) CreateWith(h Handover) (Instance, Run, error) {
	message := ""
	if strings.TrimSpace(h.Message) != "" {
		var err error
		if message, err = validatePrompt(h.Message, 0); err != nil {
			return Instance{}, Run{}, err
		}
	}
	created, err := s.Create(h.CoderID)
	if err != nil {
		return Instance{}, Run{}, err
	}
	if name := strings.TrimSpace(h.Name); name != "" {
		if err := s.Rename(created.ID, name); err != nil {
			return created, Run{}, err
		}
		created, _ = s.Get(created.ID)
	}
	if message == "" {
		return created, Run{}, nil
	}
	run, err := s.Prompt(created.ID, message)
	if err != nil {
		return created, Run{}, err
	}
	created.Title = run.Title
	return created, run, nil
}

// Prompt is Send from outside the composer, what `assistant-send-prompt` and
// the first message of `assistant-new` ask for: the message goes in as one the
// user sent, answered now or waiting in the queue behind the turn that runs,
// and nothing tells it apart. The composer did not send it, so the words the
// user has not sent yet stay where they are.
func (s *Service) Prompt(id, prompt string) (Run, error) {
	text, err := validatePrompt(prompt, 0)
	if err != nil {
		return Run{}, err
	}
	return s.send(id, false, func(Instance) (Message, error) {
		return Message{Content: text}, nil
	})
}

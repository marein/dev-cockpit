package web

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/web/render"
)

func choicesView(messageID string, choices assistant.Choices) *render.ChatChoicesView {
	view := &render.ChatChoicesView{MessageID: messageID, NoteMax: assistant.MaxChoiceNoteRunes}
	for _, q := range choices.Questions {
		question := render.ChatQuestionView{Text: q.Text}
		for _, option := range q.Options {
			question.Options = append(question.Options, render.ChatOptionView{Text: option})
		}
		view.Questions = append(view.Questions, question)
	}
	return view
}

// answerChoices settles the questions of the message at index. The first
// message of the user after it ends them, the answer sent from them or any
// other, so a block is answered once and a thread that moved on past it
// leaves it behind. While none stands, the page answers them at postURL, and
// a page without one shows them read only.
func answerChoices(view *render.ChatMessageView, messages []assistant.Message, index int, postURL string) {
	var choices *render.ChatChoicesView
	for _, part := range view.Parts {
		if part.Choices != nil {
			choices = part.Choices
		}
	}
	if choices == nil {
		return
	}
	reply, replied := assistant.ReplyTo(messages, index)
	switch {
	case replied && reply.Answers != nil && reply.Answers.Message == choices.MessageID:
		choices.Answered = true
		for i, pick := range reply.Answers.Picks {
			if i >= len(choices.Questions) {
				break
			}
			question := &choices.Questions[i]
			if pick.Option >= 0 && pick.Option < len(question.Options) {
				question.Options[pick.Option].Picked = true
			}
			question.Note = plainTextHTML(pick.Text)
		}
	case replied:
		choices.Passed = true
	case postURL != "":
		choices.PostURL = postURL
		choices.FragmentURL = postURL + "/messages/" + choices.MessageID
	}
}

// assistantAnswer sends the answers to the questions of one answer as the
// user's next message. A field per question carries the option picked
// (pick-<n>, its index) and the words beside it (note-<n>); the service
// checks them against the questions as stored and writes the text.
func (s *Server) assistantAnswer(c *gin.Context, id string) {
	if err := s.assistantWriteGuard(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	picks := make([]assistant.Pick, assistant.MaxChoiceQuestions)
	for i := range picks {
		picks[i] = assistant.Pick{Option: -1, Text: c.PostForm("note-" + strconv.Itoa(i))}
		raw := c.PostForm("pick-" + strconv.Itoa(i))
		if raw == "" {
			continue
		}
		option, err := strconv.Atoi(raw)
		if err != nil || option < 0 {
			s.assistantActionError(c, id, errors.New("That option does not exist."))
			return
		}
		picks[i].Option = option
	}
	run, err := s.assistants.Answer(id, c.PostForm("message_id"), picks)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	s.assistantRunResponse(c, id, run)
}

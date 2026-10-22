package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/web/render"
)

func choicesView(messageID string, choices assistant.Choices) *render.ChatChoicesView {
	view := &render.ChatChoicesView{MessageID: messageID, NoteMax: assistant.MaxChoiceNoteRunes}
	for _, q := range choices.Questions {
		question := render.ChatQuestionView{Text: q.Text, Multi: q.Multi}
		for _, option := range q.Options {
			question.Options = append(question.Options, render.ChatOptionView{Text: option})
		}
		view.Questions = append(view.Questions, question)
	}
	return view
}

// choicesAnswering is how a page answers the open blocks: the owner's
// assistant it posts to and what is picked and written in them so far. The
// zero value answers nothing, a guest's page and a blocked assistant show the
// blocks read only and nothing of the draft.
type choicesAnswering struct {
	instanceID string
	drafts     map[string][]assistant.Pick
}

// ownerChoices is how the owner's page answers the questions of an answer,
// not at all while the assistant takes no message.
func (s *Server) ownerChoices(instanceID string, writable bool) choicesAnswering {
	if !writable {
		return choicesAnswering{}
	}
	draft, _ := s.assistants.Draft(instanceID)
	return choicesAnswering{instanceID: instanceID, drafts: draft.Answers}
}

// answerChoices settles the questions of the message at index. The first
// message of the user after it ends them, the answer sent from them or any
// other, so a block is answered once and a thread that moved on past it
// leaves it behind. While none stands, the page answers them as answering
// says, starting from what was picked and written so far, and a page that
// answers nothing shows them read only.
func answerChoices(view *render.ChatMessageView, messages []assistant.Message, index int, answering choicesAnswering) {
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
			markPicked(question, pick)
			question.Note = plainTextHTML(pick.Text)
		}
	case replied:
		choices.Passed = true
	case answering.instanceID != "":
		base := "/assistants/" + answering.instanceID
		choices.AssistantID = answering.instanceID
		choices.PostURL = base
		choices.FragmentURL = base + "/messages/" + choices.MessageID
		choices.DraftURL = base + "/draft"
		for i, pick := range answering.drafts[choices.MessageID] {
			if i >= len(choices.Questions) {
				break
			}
			question := &choices.Questions[i]
			picked := markPicked(question, pick)
			question.Draft = pick.Text
			if picked || strings.TrimSpace(pick.Text) != "" {
				choices.Done++
			}
		}
	}
}

// markPicked marks the options one pick picked and says whether there were
// any.
func markPicked(question *render.ChatQuestionView, pick assistant.Pick) bool {
	picked := false
	for _, option := range pick.Picked() {
		if option >= 0 && option < len(question.Options) {
			question.Options[option].Picked = true
			picked = true
		}
	}
	return picked
}

// assistantAnswer sends the answers to the questions of one answer as the
// user's next message. A field per question carries the option picked
// (pick-<n>, its index), or one field per option picked where the question
// takes several (picks-<n>), and the words beside it (note-<n>); the service
// checks them against the questions as stored and writes the text.
func (s *Server) assistantAnswer(c *gin.Context, id string) {
	if err := s.assistantWriteGuard(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	picks, err := formPicks(c)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	run, err := s.assistants.Answer(id, c.PostForm("message_id"), picks)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	s.assistantRunResponse(c, id, run)
}

// assistantAnswersDraft stores what is picked and written in an open block
// while the user is at it, in the fields Send posts. It goes through the
// composer's write guard, a blocked assistant answers nothing. Only a save
// that changed something tells the other devices.
func (s *Server) assistantAnswersDraft(c *gin.Context, id string) {
	if err := s.assistantWriteGuard(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	picks, err := formPicks(c)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	changed, err := s.assistants.SaveAnswers(id, c.PostForm("message_id"), picks)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if changed {
		s.publishDraft(id)
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

func formPicks(c *gin.Context) ([]assistant.Pick, error) {
	picks := make([]assistant.Pick, assistant.MaxChoiceQuestions)
	for i := range picks {
		picks[i] = assistant.Pick{Option: -1, Text: c.PostForm("note-" + strconv.Itoa(i))}
		if raw := c.PostForm("pick-" + strconv.Itoa(i)); raw != "" {
			option, err := formOption(raw)
			if err != nil {
				return nil, err
			}
			picks[i].Option = option
		}
		for _, raw := range c.PostFormArray("picks-" + strconv.Itoa(i)) {
			option, err := formOption(raw)
			if err != nil {
				return nil, err
			}
			picks[i].Options = append(picks[i].Options, option)
		}
	}
	return picks, nil
}

func formOption(raw string) (int, error) {
	option, err := strconv.Atoi(raw)
	if err != nil || option < 0 {
		return 0, errors.New("That option does not exist.")
	}
	return option, nil
}

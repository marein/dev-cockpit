package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const choicesInstance = "11111111-1111-4111-8111-111111111111"

func choicesServer(t *testing.T) *Server {
	t.Helper()
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	return &Server{assistants: conversations, workspace: workspace}
}

func choicesThread(rest ...assistant.Message) []assistant.Message {
	asked := assistant.Message{ID: "a1", Role: assistant.RoleAssistant, State: assistant.StateComplete,
		Content: "Before <b>the</b> questions.\n\n```ask\nWhich database?\n- PostgreSQL\n- SQLite <x>\nDeploy now?\n- Yes\n- No\n```\n\nAfter them."}
	return append([]assistant.Message{asked}, rest...)
}

func renderChoices(t *testing.T, s *Server, messages []assistant.Message, answering choicesAnswering) string {
	t.Helper()
	view := s.assistantMessageView(choicesInstance, messages[0], false, true, "claude")
	answerChoices(&view, messages, 0, answering)
	return renderTemplate(t, testTemplates(t), "chat_message.gohtml", render.ChatMessageData{Message: view})
}

func TestAnOpenBlockStandsBetweenItsTextAsAForm(t *testing.T) {
	html := renderChoices(t, choicesServer(t), choicesThread(), choicesAnswering{instanceID: choicesInstance})
	before := strings.Index(html, "Before")
	block := strings.Index(html, "<dc-assistant-choices")
	after := strings.Index(html, "After them.")
	if before < 0 || block < before || after < block {
		t.Fatalf("want the text, the block and the rest in order: %s", html)
	}
	for _, want := range []string{
		`data-choices-state="open"`,
		`post-url="/assistants/` + choicesInstance + `"`,
		`fragment-url="/assistants/` + choicesInstance + `/messages/a1"`,
		`name="pick-1" value="1"`,
		`name="note-0"`,
		`SQLite &lt;x&gt;`,
		`data-choices-send>`,
		"0 of 2 answered",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %q in %s", want, html)
		}
	}
	if strings.Contains(html, "language-ask") || strings.Contains(html, "<b>") || strings.Count(html, "disabled") != 1 {
		t.Fatalf("want the block out of the markdown and nothing disabled but Send: %s", html)
	}
}

func TestAnAnsweredBlockShowsThePicksReadOnly(t *testing.T) {
	reply := assistant.Message{ID: "u1", Role: assistant.RoleUser, State: assistant.StateComplete, Content: "…",
		Answers: &assistant.Answers{Message: "a1", Picks: []assistant.Pick{{Option: 1, Text: "line one\nline <two>"}, {Option: -1, Text: "after Friday"}}}}
	html := renderChoices(t, choicesServer(t), choicesThread(reply), choicesAnswering{instanceID: choicesInstance})
	for _, want := range []string{
		`data-choices-state="answered"`,
		`name="pick-0" value="1" id="choices-a1-0-1" autocomplete="off" checked`,
		"line one<br>line &lt;two&gt;",
		"after Friday",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %q in %s", want, html)
		}
	}
	for _, unwanted := range []string{"post-url", "data-choices-send", "<textarea", `value="0" id="choices-a1-1-0" autocomplete="off" checked`, "Answered"} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("want no %q in %s", unwanted, html)
		}
	}
	if !strings.Contains(html, "<fieldset") || !strings.Contains(html, "min-w-0\" disabled>") {
		t.Fatalf("want the fieldset disabled: %s", html)
	}
}

func TestTheAnswerStandsAsASlimRowUnderItsBlock(t *testing.T) {
	reply := assistant.Message{ID: "u1", Role: assistant.RoleUser, State: assistant.StateComplete, Content: "Answer: PostgreSQL",
		CreatedAt: time.Date(2026, 10, 20, 21, 3, 0, 0, time.UTC),
		Answers:   &assistant.Answers{Message: "a1", Picks: []assistant.Pick{{Option: 0}, {Option: 1}}}}
	next := assistant.Message{ID: "a2", Role: assistant.RoleAssistant, State: assistant.StateComplete, Content: "Done."}
	views := choicesServer(t).assistantMessageViews(assistant.Instance{Summary: assistant.Summary{ID: choicesInstance, CoderID: "claude"}, Messages: choicesThread(reply, next)}, false)
	var ids []string
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	if strings.Join(ids, ",") != "a1,u1,a2" {
		t.Fatalf("want the asked message, the answer and the next answer, got %v", ids)
	}
	html := renderTemplate(t, testTemplates(t), "chat_message.gohtml", render.ChatMessageData{Message: views[1]})
	for _, want := range []string{`data-role="user"`, "data-assistant-answered", "You", `<dc-time datetime="2026-10-20T21:03:00Z"`, "answered</span>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %q in the row: %s", want, html)
		}
	}
	if strings.Contains(html, "PostgreSQL") || strings.Contains(html, "data-assistant-text") {
		t.Fatalf("want nothing of the answer's text in the row: %s", html)
	}
}

func TestAThreadThatMovedOnLeavesTheBlockBehind(t *testing.T) {
	other := assistant.Message{ID: "u1", Role: assistant.RoleUser, State: assistant.StateQueued, Content: "something else"}
	html := renderChoices(t, choicesServer(t), choicesThread(other), choicesAnswering{instanceID: choicesInstance})
	if !strings.Contains(html, `data-choices-state="passed"`) || !strings.Contains(html, "Not answered") || strings.Contains(html, " checked") || strings.Contains(html, "post-url") {
		t.Fatalf("want the block over without a pick: %s", html)
	}
	views := choicesServer(t).assistantMessageViews(assistant.Instance{Summary: assistant.Summary{ID: choicesInstance, CoderID: "claude"}, Messages: choicesThread(other)}, false)
	row := renderTemplate(t, testTemplates(t), "chat_message.gohtml", render.ChatMessageData{Message: views[1]})
	if strings.Contains(row, "data-assistant-answered") || !strings.Contains(row, "something else") {
		t.Fatalf("want the other message as it always stood, no answered row: %s", row)
	}
}

func TestABlockThePageCannotAnswerIsReadOnly(t *testing.T) {
	for name, answering := range map[string]choicesAnswering{"guest": {}, "blocked": choicesServer(t).ownerChoices(choicesInstance, false)} {
		html := renderChoices(t, choicesServer(t), choicesThread(), answering)
		if !strings.Contains(html, `data-choices-state="readonly"`) || strings.Contains(html, "post-url") || strings.Contains(html, "data-choices-send") {
			t.Fatalf("%s: want the block read only: %s", name, html)
		}
	}
}

func TestAMalformedBlockStaysTheCodeItWasWrittenAs(t *testing.T) {
	s := choicesServer(t)
	messages := []assistant.Message{{ID: "a1", Role: assistant.RoleAssistant, State: assistant.StateComplete, Content: "Pick:\n\n```ask\nWhich?\n- only one\n```\n"}}
	html := renderChoices(t, s, messages, choicesAnswering{instanceID: choicesInstance})
	if strings.Contains(html, "dc-assistant-choices") || !strings.Contains(html, `class="language-ask"`) || !strings.Contains(html, "- only one") {
		t.Fatalf("want the block as code: %s", html)
	}
}

func TestAChoicesBlockStaysCode(t *testing.T) {
	s := choicesServer(t)
	messages := []assistant.Message{{ID: "a1", Role: assistant.RoleAssistant, State: assistant.StateComplete, Content: "Pick:\n\n```choices\nWhich?\n- one\n- other\n```\n"}}
	html := renderChoices(t, s, messages, choicesAnswering{instanceID: choicesInstance})
	if strings.Contains(html, "dc-assistant-choices") || !strings.Contains(html, `class="language-choices"`) || !strings.Contains(html, "- other") {
		t.Fatalf("want the block as code: %s", html)
	}
}

// An open block stands as the draft left it, rendered by the server: the
// picks checked, the notes in their fields as typed, the count and Send as
// the script would set them. Where the draft answers one question only, Send
// stays off.
func TestAnOpenBlockStandsFilledFromTheDraft(t *testing.T) {
	drafts := map[string][]assistant.Pick{"a1": {{Option: 1, Text: "\nline one\nline <two>"}, {Option: -1, Text: "after Friday"}}}
	html := renderChoices(t, choicesServer(t), choicesThread(), choicesAnswering{instanceID: choicesInstance, drafts: drafts})
	for _, want := range []string{
		`data-choices-state="open"`,
		`draft-url="/assistants/` + choicesInstance + `/draft"`,
		`assistant-id="` + choicesInstance + `"`,
		`name="pick-0" value="1" id="choices-a1-0-1" autocomplete="off" checked`,
		"data-choices-note>\n\nline one\nline &lt;two&gt;</textarea>",
		"data-choices-note>\nafter Friday</textarea>",
		"2 of 2 answered",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %q in %s", want, html)
		}
	}
	if strings.Count(html, " checked") != 1 || strings.Contains(html, "disabled") {
		t.Fatalf("want one pick and Send on: %s", html)
	}
	half := map[string][]assistant.Pick{"a1": {{Option: 0}}}
	html = renderChoices(t, choicesServer(t), choicesThread(), choicesAnswering{instanceID: choicesInstance, drafts: half})
	if !strings.Contains(html, "1 of 2 answered") || !strings.Contains(html, " disabled data-choices-send") {
		t.Fatalf("want one of two answered and Send off: %s", html)
	}
}

// choicesOwner is an assistant whose thread ends in the open block of
// choicesThread, with a pick and a note in its draft.
func choicesOwner(t *testing.T) (*Server, assistant.Instance) {
	t.Helper()
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	created, err := conversations.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	created.Messages = choicesThread()
	assistant.NewStore(stateDir).Save(created)
	if _, err := conversations.SaveAnswers(created.ID, "a1", []assistant.Pick{{Option: 1}, {Option: -1, Text: "half an answer of the owner"}}); err != nil {
		t.Fatalf("save answers: %v", err)
	}
	return &Server{assistants: conversations, workspace: workspace, bus: eventbus.New()}, created
}

// The owner's page fills the block from the assistant's draft. A blocked
// assistant shows it read only and nothing of the draft.
func TestTheOwnersPageStartsFromTheDraftAndABlockedOneShowsNone(t *testing.T) {
	s, created := choicesOwner(t)
	for blocked, want := range map[bool]bool{false: true, true: false} {
		views := s.assistantMessageViews(created, blocked)
		html := renderTemplate(t, testTemplates(t), "chat_message.gohtml", render.ChatMessageData{Message: views[0]})
		filled := strings.Contains(html, "half an answer of the owner") && strings.Contains(html, `value="1" id="choices-a1-0-1" autocomplete="off" checked`)
		if filled != want || blocked && (strings.Contains(html, " checked") || !strings.Contains(html, `data-choices-state="readonly"`)) {
			t.Fatalf("blocked %v: want the draft shown %v: %s", blocked, want, html)
		}
	}
}

// The block saves into the draft with form=answers-draft, the fields Send
// posts. A save that changed something tells the other devices, a repeated
// one nobody, a refused one answers the refusal, and the draft read carries
// the answers beside the words.
func TestAnAnswersDraftSaveTellsTheOtherDevicesOnlyOfAChange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, created := choicesOwner(t)
	events, cancel := s.bus.Subscribe()
	defer cancel()
	r := gin.New()
	r.POST("/assistants/:id", s.handleAssistantAction)
	r.GET("/assistants/:id/draft", s.handleAssistantDraft)
	post := func(form url.Values) int {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/assistants/"+created.ID, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	told := func() int {
		n := 0
		for {
			select {
			case ev := <-events:
				if ev.Type == "draft" {
					n++
				}
			default:
				return n
			}
		}
	}
	form := url.Values{"form": {"answers-draft"}, "message_id": {"a1"}, "pick-0": {"0"}, "note-1": {" Friday "}}
	if code := post(form); code != http.StatusOK || told() != 1 {
		t.Fatalf("want the save stored and told once, got %d", code)
	}
	if code := post(form); code != http.StatusOK || told() != 0 {
		t.Fatalf("want a repeated save to tell nobody, got %d", code)
	}
	if code := post(url.Values{"form": {"answers-draft"}, "message_id": {"a1"}, "pick-0": {"5"}}); code != http.StatusBadRequest || told() != 0 {
		t.Fatalf("want a bad option refused quietly, got %d", code)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assistants/"+created.ID+"/draft", nil))
	var draft struct {
		Answers map[string][]assistant.Pick `json:"answers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &draft); err != nil || len(draft.Answers["a1"]) != 2 || draft.Answers["a1"][0].Option != 0 || draft.Answers["a1"][1].Text != " Friday " {
		t.Fatalf("want the draft read to carry the answers as typed, got %s", rec.Body.String())
	}
}

func mixedThread(rest ...assistant.Message) []assistant.Message {
	asked := assistant.Message{ID: "a1", Role: assistant.RoleAssistant, State: assistant.StateComplete,
		Content: "```ask\nWhich checks?\n- [ ] Unit tests\n- [ ] Lint\n- [ ] e2e\nDeploy now?\n- Yes\n- No\n```\n"}
	return append([]assistant.Message{asked}, rest...)
}

// A question that takes several picks stands as checkboxes drawn like the
// radios of one that takes one, filled from every pick of the draft, and an
// answered one shows every pick. A pick or words answer it.
func TestAQuestionThatTakesSeveralPicksStandsAsCheckboxes(t *testing.T) {
	html := renderChoices(t, choicesServer(t), mixedThread(), choicesAnswering{instanceID: choicesInstance})
	for _, want := range []string{
		`role="group" aria-labelledby="choices-a1-0"`,
		`role="radiogroup" aria-labelledby="choices-a1-1"`,
		`<input type="checkbox" class="btn-check" name="picks-0" value="2" id="choices-a1-0-2" autocomplete="off">`,
		`<input type="radio" class="btn-check" name="pick-1" value="0" id="choices-a1-1-0" autocomplete="off">`,
		`class="btn btn-sm btn-outline-primary text-wrap text-break text-start" for="choices-a1-0-2" data-choices-option>e2e</label>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %q in %s", want, html)
		}
	}
	drafts := map[string][]assistant.Pick{"a1": {{Option: -1, Options: []int{0, 2}}}}
	html = renderChoices(t, choicesServer(t), mixedThread(), choicesAnswering{instanceID: choicesInstance, drafts: drafts})
	if !strings.Contains(html, `name="picks-0" value="0" id="choices-a1-0-0" autocomplete="off" checked`) || !strings.Contains(html, `name="picks-0" value="2" id="choices-a1-0-2" autocomplete="off" checked`) ||
		strings.Count(html, " checked") != 2 || !strings.Contains(html, "1 of 2 answered") {
		t.Fatalf("want both picks of the draft checked and one question answered: %s", html)
	}
	words := map[string][]assistant.Pick{"a1": {{Option: -1, Text: "only the fast ones"}, {Option: 1}}}
	html = renderChoices(t, choicesServer(t), mixedThread(), choicesAnswering{instanceID: choicesInstance, drafts: words})
	if !strings.Contains(html, "2 of 2 answered") || strings.Contains(html, "disabled") {
		t.Fatalf("want words alone to answer the question: %s", html)
	}
	reply := assistant.Message{ID: "u1", Role: assistant.RoleUser, State: assistant.StateComplete, Content: "…",
		Answers: &assistant.Answers{Message: "a1", Picks: []assistant.Pick{{Option: -1, Options: []int{1, 2}, Text: "before Friday"}, {Option: 0}}}}
	html = renderChoices(t, choicesServer(t), mixedThread(reply), choicesAnswering{instanceID: choicesInstance})
	for _, want := range []string{`data-choices-state="answered"`, `name="picks-0" value="1" id="choices-a1-0-1" autocomplete="off" checked`, `name="picks-0" value="2" id="choices-a1-0-2" autocomplete="off" checked`, `name="pick-1" value="0" id="choices-a1-1-0" autocomplete="off" checked`, "before Friday"} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %q in %s", want, html)
		}
	}
	if strings.Count(html, " checked") != 3 {
		t.Fatalf("want the three picks checked: %s", html)
	}
}

// The picks of a question that takes several come as one picks-<n> field
// each and land in the draft in the question's order; a pick named twice or
// in the field of the other kind is refused.
func TestPicksOfSeveralAreSavedFromTheirFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	created, _ := conversations.Create("claude")
	created.Messages = mixedThread()
	assistant.NewStore(stateDir).Save(created)
	s := &Server{assistants: conversations, workspace: workspace, bus: eventbus.New()}
	r := gin.New()
	r.POST("/assistants/:id", s.handleAssistantAction)
	post := func(form url.Values) int {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/assistants/"+created.ID, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	for name, form := range map[string]url.Values{
		"twice":         {"picks-0": {"1", "1"}},
		"one on many":   {"pick-0": {"1"}},
		"many on one":   {"picks-1": {"0", "1"}},
		"not a number":  {"picks-0": {"x"}},
		"out of range":  {"picks-0": {"3"}},
		"below nothing": {"picks-0": {"-1"}},
	} {
		form.Set("form", "answers-draft")
		form.Set("message_id", "a1")
		if code := post(form); code != http.StatusBadRequest {
			t.Errorf("%s: want a refusal, got %d", name, code)
		}
	}
	if code := post(url.Values{"form": {"answers-draft"}, "message_id": {"a1"}, "picks-0": {"2", "0"}, "pick-1": {"1"}}); code != http.StatusOK {
		t.Fatalf("want the picks saved, got %d", code)
	}
	draft, _ := conversations.Draft(created.ID)
	if got := draft.Answers["a1"]; len(got) != 2 || !slices.Equal(got[0].Options, []int{0, 2}) || got[0].Option != -1 || got[1].Option != 1 {
		t.Fatalf("want both picks in order beside the one pick, got %+v", got)
	}
}

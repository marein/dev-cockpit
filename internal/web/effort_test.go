package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	codercopilot "github.com/marein/dev-cockpit/internal/coder/copilot"
	coderopencode "github.com/marein/dev-cockpit/internal/coder/opencode"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/tmux"
)

type leveledCoder struct{}

func (leveledCoder) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude", CheckEffort: leveledCheck}}
}

// leveledCheck takes low, high and max, and no level at all on a model named
// lean, the way opencode's big-pickle takes no variant.
func leveledCheck(model, level string) error {
	if model == "lean" {
		return fmt.Errorf("%s takes no effort level.", model)
	}
	_, err := assistant.CleanEffortIn(level, []string{"low", "high", "max"})
	return err
}

// The ring's menu posts the effort beside every model: stored when the coder
// takes the level, refused with the levels named otherwise, and the sentence
// names an effort behind its model.
func TestTheRingStoresTheEffortsBesideTheModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, leveledCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	created, err := conversations.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s := &Server{assistants: conversations, workspace: workspace}
	r := gin.New()
	r.POST("/assistants/:id", s.handleAssistantAction)
	post := func(form url.Values) (int, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/assistants/"+created.ID, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		r.ServeHTTP(rec, req)
		var answer map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &answer)
		return rec.Code, answer
	}
	code, answer := post(url.Values{"form": {"model"}, "model": {"opus"}, "effort": {"high"}, "check_effort": {"low"}})
	if code != http.StatusOK || answer["effort"] != "high" || answer["checkEffort"] != "low" || answer["message"] != "Chat and triggers on opus at high effort, checks on opus at low effort." {
		t.Fatalf("want the efforts stored and named, got %d %v", code, answer)
	}
	picks, _ := answer["models"].(map[string]any)
	if picks["chatEffort"] != "high" || picks["checkEffort"] != "low" {
		t.Fatalf("want the frame's reading to carry the efforts, got %v", picks)
	}
	code, answer = post(url.Values{"form": {"model"}, "trigger_effort": {"xhigh"}})
	if code != http.StatusBadRequest || answer["error"] != "Triggers: The effort is one of low, high, max." {
		t.Fatalf("want a level the coder does not take refused with its levels, got %d %v", code, answer)
	}
	stored, _ := conversations.Get(created.ID)
	if stored.TriggerEffort != "" || stored.Effort != "high" {
		t.Fatalf("a refused save moved a pick: %+v", stored.Summary)
	}
	code, answer = post(url.Values{"form": {"model"}, "check_model": {"lean"}})
	if code != http.StatusBadRequest || answer["error"] != "Checks: lean takes no effort level." {
		t.Fatalf("want a checks model that does not take the checks' own effort refused, got %d %v", code, answer)
	}
	code, answer = post(url.Values{"form": {"model"}, "check_model": {"lean"}, "check_effort": {""}})
	if code != http.StatusOK || answer["message"] != "Chat and triggers on opus at high effort, checks on lean." {
		t.Fatalf("want checks on a model of their own to run at its own level, got %d %v", code, answer)
	}
	code, answer = post(url.Values{"form": {"model"}, "check_effort": {"high"}})
	if code != http.StatusBadRequest || answer["error"] != "Checks: lean takes no effort level." {
		t.Fatalf("want the checks' pair refused by name, got %d %v", code, answer)
	}
	code, answer = post(url.Values{"form": {"model"}, "model": {"lean"}})
	if code != http.StatusBadRequest || answer["error"] != "Chat: lean takes no effort level." {
		t.Fatalf("want a chat model that does not take the chat's effort refused, got %d %v", code, answer)
	}
}

// The empty entry of an effort select follows the model select above it:
// while that is empty it names what the model's level gives, beside a model
// of its own it is the CLI's level, and the element gets both words.
func TestAnEffortSelectsEmptyEntryFollowsItsModel(t *testing.T) {
	follow := effortPick("check_effort", "", modelSameChatLabel, "", []string{"low", "high"})
	own := effortPick("check_effort", "", modelSameChatLabel, "lean", []string{"low", "high"})
	if follow.Options[0].Label != "Same as chat" || own.Options[0].Label != "Default (CLI)" {
		t.Fatalf("want Same as chat and Default (CLI), got %q and %q", follow.Options[0].Label, own.Options[0].Label)
	}
	if own.FollowLabel != "Same as chat" || own.OwnLabel != "Default (CLI)" {
		t.Fatalf("the element does not get both words: %+v", own)
	}
}

// The settings pages carry an effort beside every default model: the tab's
// three and the coder's start effort, a refused level storing nothing.
func TestTheSettingsPagesRoundTripTheEfforts(t *testing.T) {
	r, s := modelSettingsServer(t)
	page := getPage(t, r, "/settings/assistant/models")
	for _, field := range []string{`name="effort-chat-claude"`, `name="effort-check-claude"`, `name="effort-trigger-claude"`} {
		if !strings.Contains(page, field) {
			t.Fatalf("the Models tab has no %s", field)
		}
	}
	if rec := postForm(t, r, "/settings/assistant/models", url.Values{"effort-chat-claude": {"high"}, "effort-check-claude": {"low"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	if got := s.modelDefaults("claude"); got.ChatEffort != "high" || got.CheckEffort != "low" {
		t.Fatalf("want the tab's efforts stored, got %+v", got)
	}
	postForm(t, r, "/settings/assistant/models", url.Values{"effort-chat-claude": {"turbo"}, "chat-claude": {"opus"}})
	if got := s.modelDefaults("claude"); got.ChatEffort != "high" || got.Chat != "" {
		t.Fatalf("a refused level must store nothing, got %+v", got)
	}
	if page := getPage(t, r, "/settings/assistant/models"); !strings.Contains(page, `<option value="high" selected>high</option>`) {
		t.Fatal("the stored effort does not come back selected")
	}

	base := s.coderBase(s.coders[0])
	postForm(t, r, base+"/models", url.Values{"start": {"opus"}, "start_effort": {"max"}})
	if got := s.modelDefaults("claude"); got.Start != "opus" || got.StartEffort != "max" {
		t.Fatalf("want the start model and effort stored, got %+v", got)
	}
	postForm(t, r, base+"/models", url.Values{"start": {"haiku"}, "start_effort": {"turbo"}})
	if got := s.modelDefaults("claude"); got.Start != "opus" || got.StartEffort != "max" {
		t.Fatalf("a refused start effort must leave both as they were, got %+v", got)
	}
	if page := getPage(t, r, base+"/models"); !strings.Contains(page, `name="start_effort"`) || !strings.Contains(page, `<option value="max" selected>max</option>`) {
		t.Fatal("the coder's Models section does not show the stored start effort")
	}
}

// A level the coder does not take is refused before the session exists,
// with the levels named, and one it takes reaches the start.
func TestCoderCreateChecksTheEffortBeforeTheSessionExists(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	projects := project.NewRepository(root, recent.New(filepath.Join(t.TempDir(), "recent.json")))
	s := &Server{
		coders:   []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), codercopilot.New(nil), projects)},
		projects: projects,
	}
	form := url.Values{"name": {"probe"}, "project": {filepath.Join(root, "missing")}, "effort": {"turbo"}}
	rec := createCoder(t, s, form)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "none, minimal, low, medium, high, xhigh, max") {
		t.Fatalf("want the levels named before anything starts, got %d: %s", rec.Code, rec.Body.String())
	}
	form.Set("effort", "minimal")
	rec = createCoder(t, s, form)
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), "effort") {
		t.Fatalf("a level the coder takes must reach the session start, got %d: %s", rec.Code, rec.Body.String())
	}
}

type leveledStoreCoders struct{ store *settings.Store }

func (c leveledStoreCoders) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude", Defaults: func() assistant.ModelDefaults {
		return assistant.ModelDefaultsFor(c.store, "claude")
	}, CheckEffort: leveledCheck}}
}

// The tab's efforts are copied onto a new assistant like its models, a
// trigger posted with an effort keeps its own, one the owner's coder does not
// take is refused, and the answer names what the reaction would run at
// without the flag.
func TestTheEffortsReachANewAssistantAndATriggerKeepsItsOwn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	store := settings.New(filepath.Join(stateDir, "settings.json"))
	conversations, _, err := assistant.New(stateDir, leveledStoreCoders{store: store}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{assistants: conversations, settings: store, watcher: assistant.NewWatcher(conversations, assistant.NewJobs(assistant.NewStore(stateDir)), nil, nil)}
	store.Set(assistant.EffortDefaultKey("claude", assistant.ModelPurposeChat), "high")
	store.Set(assistant.EffortDefaultKey("claude", assistant.ModelPurposeTrigger), "low")
	made, err := conversations.Create("claude")
	if err != nil || made.Effort != "high" || made.TriggerEffort != "low" || made.CheckEffort != "" {
		t.Fatalf("want the tab's efforts copied onto the new assistant, got %+v %v", made.Summary, err)
	}
	post := func(form url.Values) (int, map[string]any) {
		t.Helper()
		form.Set("form", "new")
		form.Set("assistant", made.ID)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, "/assistants/triggers", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		c.Request = req
		s.handleAssistantTriggersAction(c)
		var answer map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &answer)
		return rec.Code, answer
	}
	code, own := post(url.Values{"event": {"job-done"}, "task": {"summarize"}, "effort": {"max"}})
	if code != http.StatusOK || own["effort"] != "max" || own["effortDefault"] != "low" {
		t.Fatalf("want the trigger's own effort with the default it replaced, got %d %v", code, own)
	}
	if trigger, ok := conversations.Events().Get(own["id"].(string)); !ok || trigger.Effort != "max" {
		t.Fatalf("want the trigger's own effort stored, got %+v", trigger)
	}
	if code, refused := post(url.Values{"event": {"job-done"}, "task": {"summarize"}, "effort": {"xhigh"}}); code == http.StatusOK || !strings.Contains(refused["error"].(string), "low, high, max") {
		t.Fatalf("want a level the owner's coder does not take refused, got %d %v", code, refused)
	}
}

// An opencode start effort needs a start model, its levels are a model's
// variants: the pair is refused on the page that sets it, nothing is stored,
// and with a model the pair is checked by the model's own variants.
func TestTheStartEffortIsRefusedWhereItCannotRun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	store := settings.New(filepath.Join(stateDir, "settings.json"))
	projects := project.NewRepository(t.TempDir(), nil)
	s := &Server{settings: store, coders: []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), coderopencode.New("", store), projects)}}
	co := s.coders[0]
	r := gin.New()
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.POST(s.coderBase(co)+"/models", s.handleCoderModelsSave(co))
	rec := postForm(t, r, s.coderBase(co)+"/models", url.Values{"start": {""}, "start_effort": {"high"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save: %d", rec.Code)
	}
	if got := s.modelDefaults("opencode"); got.StartEffort != "" || got.Start != "" {
		t.Fatalf("a start effort without a start model was stored: %+v", got)
	}
	if err := coder.CheckEffortFor(co.Coder(), "", "high"); err == nil || !strings.Contains(err.Error(), "only together with a model") {
		t.Fatalf("want the refusal to say what is missing, got %v", err)
	}
}

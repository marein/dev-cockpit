package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	coderclaude "github.com/marein/dev-cockpit/internal/coder/claude"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// The ring's three empty entries: the Chat entry names the coder's start
// default, the one level below a chat pick, Coder default (CLI) while none
// stands, and never the Models tab's chat default, which is copied onto a
// new assistant as its pick and stands on no chain; the Checks and the
// Triggers entry read Same as chat whatever the tab holds.
func TestTheRingsEmptyEntriesNameTheCodersDefault(t *testing.T) {
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	s := &Server{settings: store}
	empties := func() string {
		data := s.assistantModelsData(assistant.Instance{Summary: assistant.Summary{CoderID: "claude"}})
		return data.Chat.Options[0].Label + "," + data.Check.Options[0].Label + "," + data.Trigger.Options[0].Label
	}
	if got := empties(); got != "Coder default (CLI),Same as chat,Same as chat" {
		t.Fatalf("want the fresh ring on the CLI and the chat, got %q", got)
	}
	for purpose, name := range map[assistant.ModelPurpose]string{
		assistant.ModelPurposeChat: "haiku", assistant.ModelPurposeCheck: "sonnet", assistant.ModelPurposeTrigger: "opus",
	} {
		store.Set(assistant.ModelDefaultKey("claude", purpose), name)
	}
	if got := empties(); got != "Coder default (CLI),Same as chat,Same as chat" {
		t.Fatalf("want the Models tab's defaults on no entry of the ring, got %q", got)
	}
	store.Set(assistant.ModelDefaultKey("claude", assistant.ModelPurposeStart), "fable")
	if got := empties(); got != "Coder default (fable),Same as chat,Same as chat" {
		t.Fatalf("want the Chat entry naming the coder's start default, got %q", got)
	}
}

// A select holds the empty entry first, the repository's names with the
// stored value selected, and a stored value the repository does not hold as
// an entry of its own, so a name stored before the repository knew it is not
// dropped; once the repository remembers it, it is a plain entry and never
// doubled.
func TestAModelSelectShowsWhatIsStored(t *testing.T) {
	repo := coder.NewModelRepository(nil, "claude", "", func() []string { return []string{"opus", "haiku"} })
	pick := modelPick("model", "haiku", modelDefaultLabel("fable"), repo)
	var labels, selected []string
	for _, option := range pick.Options {
		labels = append(labels, option.Label)
		if option.Selected {
			selected = append(selected, option.Value)
		}
	}
	if strings.Join(labels, ",") != "Default (fable),opus,haiku" || strings.Join(selected, ",") != "haiku" {
		t.Fatalf("want the list with the stored value selected, got %v selected %v", labels, selected)
	}
	if modelDefaultLabel("") != "Default (CLI)" || coderDefaultLabel("") != "Coder default (CLI)" || coderDefaultLabel("opus") != "Coder default (opus)" {
		t.Fatal("the empty entries do not name the level they stand on")
	}
	if pick.Current != "haiku" || pick.MaxRunes != assistant.MaxModelRunes {
		t.Fatalf("want the stored value and the bound on the pick, got %+v", pick)
	}

	typed := modelPick("model", "claude-haiku-4-5", modelSameChatLabel, repo)
	last := typed.Options[len(typed.Options)-1]
	if len(typed.Options) != 4 || last.Value != "claude-haiku-4-5" || !last.Selected || typed.Options[0].Label != modelSameChatLabel || typed.Options[0].Selected {
		t.Fatalf("want a stored value the repository does not hold as the selected entry, got %+v", typed.Options)
	}
	if err := repo.Add("claude-haiku-4-5"); err != nil {
		t.Fatal(err)
	}
	remembered := modelPick("model", "claude-haiku-4-5", modelSameChatLabel, repo)
	if len(remembered.Options) != 4 || !remembered.Options[3].Selected || remembered.Options[3].Value != "claude-haiku-4-5" {
		t.Fatalf("want a remembered name as a plain selected entry, never doubled, got %+v", remembered.Options)
	}

	none := modelPick("model", "", modelDefaultLabel(""), coder.ModelRepositoryFor(nil))
	if len(none.Options) != 1 || !none.Options[0].Selected || none.Options[0].Value != "" {
		t.Fatalf("want the empty entry alone and selected without a list and a value, got %+v", none.Options)
	}
}

// One sentence for the toast and the flash, saying what the chat, the checks
// and the triggers run on, with the fallback spelled out: the coder's start
// default is named where a pick left the choice to it, and a purpose without
// a pick of its own folds into the chat's clause, because it then follows
// what the chat resolves to, the chat pick included, whatever the Models tab
// holds, which are creation defaults and no run time ones, the chat default
// among them.
func TestTheModelsMessageSpellsOutTheFallback(t *testing.T) {
	for _, tc := range []struct {
		entry    assistant.Summary
		defaults assistant.ModelDefaults
		want     string
	}{
		{assistant.Summary{}, assistant.ModelDefaults{}, "Chat, checks and triggers on the CLI's default."},
		{assistant.Summary{Model: "opus"}, assistant.ModelDefaults{}, "Chat, checks and triggers on opus."},
		{assistant.Summary{Model: "opus", CheckModel: "haiku"}, assistant.ModelDefaults{}, "Chat and triggers on opus, checks on haiku."},
		{assistant.Summary{CheckModel: "haiku"}, assistant.ModelDefaults{}, "Chat and triggers on the CLI's default, checks on haiku."},
		{assistant.Summary{Model: "opus", TriggerModel: "haiku"}, assistant.ModelDefaults{}, "Chat and checks on opus, triggers on haiku."},
		{assistant.Summary{Model: "opus", CheckModel: "haiku", TriggerModel: "sonnet"}, assistant.ModelDefaults{}, "Chat on opus, checks on haiku, triggers on sonnet."},
		{assistant.Summary{}, assistant.ModelDefaults{Chat: "sonnet"}, "Chat, checks and triggers on the CLI's default."},
		{assistant.Summary{}, assistant.ModelDefaults{Start: "fable"}, "Chat, checks and triggers on fable."},
		{assistant.Summary{}, assistant.ModelDefaults{Chat: "sonnet", Start: "fable"}, "Chat, checks and triggers on fable."},
		{assistant.Summary{}, assistant.ModelDefaults{Chat: "sonnet", Check: "haiku", Trigger: "opus"}, "Chat, checks and triggers on the CLI's default."},
		{assistant.Summary{Model: "opus"}, assistant.ModelDefaults{Chat: "sonnet", Check: "haiku"}, "Chat, checks and triggers on opus."},
		{assistant.Summary{Model: "opus"}, assistant.ModelDefaults{Chat: "sonnet"}, "Chat, checks and triggers on opus."},
		{assistant.Summary{Model: "opus"}, assistant.ModelDefaults{Trigger: "haiku"}, "Chat, checks and triggers on opus."},
		{assistant.Summary{}, assistant.ModelDefaults{Check: "haiku"}, "Chat, checks and triggers on the CLI's default."},
		{assistant.Summary{Model: "sonnet", CheckModel: "sonnet", TriggerModel: "sonnet"}, assistant.ModelDefaults{}, "Chat on sonnet, checks on sonnet, triggers on sonnet."},
	} {
		if got := assistantModelsMessage(tc.entry, tc.defaults); got != tc.want {
			t.Fatalf("%+v with %+v reads as %q, want %q", tc.entry, tc.defaults, got, tc.want)
		}
	}
}

// The ring's menu posts form=model to the assistant's own path: the three
// fields are stored cleaned, the JSON answer carries them with the sentence
// the page toasts, a refused name changes nothing and answers the refusal, a
// request without a field leaves that one standing, and a name the coder's
// list does not hold is remembered by its repository, which the models read
// then lists as added.
func TestTheModelFormStoresTheThreeModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	created, err := conversations.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	claude := coderclaude.New("", nil)
	s := &Server{
		assistants: conversations, workspace: workspace,
		coders: []*coder.Manager{coder.NewManager(config.Config{}, tmux.New(), claude, project.NewRepository(t.TempDir(), nil))},
	}
	r := gin.New()
	r.POST("/assistants/:id", s.handleAssistantAction)
	r.GET("/assistants/models", s.handleAssistantModels)

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

	code, answer := post(url.Values{"form": {"model"}, "model": {" opus "}, "check_model": {"haiku"}, "trigger_model": {"sonnet"}})
	if code != http.StatusOK || answer["model"] != "opus" || answer["checkModel"] != "haiku" || answer["triggerModel"] != "sonnet" || answer["message"] != "Chat on opus, checks on haiku, triggers on sonnet." {
		t.Fatalf("want the three models stored and answered with the sentence, got %d %v", code, answer)
	}
	stored, _ := conversations.Get(created.ID)
	if stored.Model != "opus" || stored.CheckModel != "haiku" || stored.TriggerModel != "sonnet" {
		t.Fatalf("want the models on the instance, got %+v", stored.Summary)
	}
	// The answer carries the reading the stream's models frame carries, so
	// the page applies its own save through the very shape a frame from
	// another tab arrives in and can tell the older of the two.
	picks, _ := answer["models"].(map[string]any)
	if picks["assistant"] != created.ID || picks["chat"] != "opus" || picks["check"] != "haiku" || picks["trigger"] != "sonnet" || picks["updatedAt"] == "" || picks["updatedAt"] == nil {
		t.Fatalf("want the answer to carry the picks with the assistant's id and their stamp, got %v", answer["models"])
	}

	code, answer = post(url.Values{"form": {"model"}, "model": {"two words"}, "check_model": {""}, "trigger_model": {""}})
	if code != http.StatusBadRequest || !strings.Contains(answer["error"].(string), "no spaces") {
		t.Fatalf("want a refused name answered with the refusal, got %d %v", code, answer)
	}
	code, answer = post(url.Values{"form": {"model"}, "trigger_model": {"-bad"}})
	if code != http.StatusBadRequest || !strings.Contains(answer["error"].(string), "dash") {
		t.Fatalf("want a refused trigger model answered with the refusal, got %d %v", code, answer)
	}
	stored, _ = conversations.Get(created.ID)
	if stored.Model != "opus" || stored.CheckModel != "haiku" || stored.TriggerModel != "sonnet" {
		t.Fatalf("want a refused save to change nothing, got %+v", stored.Summary)
	}

	code, answer = post(url.Values{"form": {"model"}, "check_model": {"claude-haiku-4-5"}})
	if code != http.StatusOK || answer["message"] != "Chat on opus, checks on claude-haiku-4-5, triggers on sonnet." {
		t.Fatalf("want a typed name stored and the other models left standing, got %d %v", code, answer)
	}
	repo := coder.ModelRepositoryFor(claude)
	if !repo.Exists("claude-haiku-4-5") {
		t.Fatal("want the typed name remembered by the coder's repository")
	}

	code, answer = post(url.Values{"form": {"model"}, "check_model": {""}})
	if code != http.StatusOK || answer["message"] != "Chat and checks on opus, triggers on sonnet." {
		t.Fatalf("want an emptied check model cleared and the checks following the chat pick, got %d %v", code, answer)
	}
	stored, _ = conversations.Get(created.ID)
	if stored.Model != "opus" || stored.CheckModel != "" || stored.TriggerModel != "sonnet" {
		t.Fatalf("want the chat and trigger models kept and the check model cleared, got %+v", stored.Summary)
	}

	code, answer = post(url.Values{"form": {"model"}, "trigger_model": {""}})
	if code != http.StatusOK || answer["triggerModel"] != "" || answer["message"] != "Chat, checks and triggers on opus." {
		t.Fatalf("want an emptied trigger model cleared and the triggers following the chat pick, got %d %v", code, answer)
	}
	stored, _ = conversations.Get(created.ID)
	if stored.Model != "opus" || stored.CheckModel != "" || stored.TriggerModel != "" {
		t.Fatalf("want the trigger model cleared, got %+v", stored.Summary)
	}

	get := func(path string) (int, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var answer map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &answer)
		return rec.Code, answer
	}
	code, answer = get("/assistants/models?coder=claude")
	coders, _ := answer["coders"].([]any)
	if code != http.StatusOK || len(coders) != 1 {
		t.Fatalf("want the one coder's list, got %d %v", code, answer)
	}
	entry, _ := coders[0].(map[string]any)
	models, _ := entry["models"].([]any)
	var rows []string
	for _, m := range models {
		row, _ := m.(map[string]any)
		rows = append(rows, row["source"].(string)+":"+row["name"].(string))
	}
	if entry["id"] != "claude" || strings.Join(rows, ",") != "cli:fable,cli:opus,cli:sonnet,cli:haiku,added:claude-haiku-4-5" {
		t.Fatalf("want the aliases as the CLI's and the typed name as added, got %v", rows)
	}
	if note, _ := entry["note"].(string); !strings.Contains(note, "newest of each family") {
		t.Fatalf("want the coder's note on the entry, got %v", entry)
	}
	if code, answer := get("/assistants/models?coder=nope"); code != http.StatusNotFound || answer["error"] == nil {
		t.Fatalf("want an unknown coder refused, got %d %v", code, answer)
	}
	if code, answer := get("/assistants/models"); code != http.StatusOK || len(answer["coders"].([]any)) != 1 {
		t.Fatalf("want every serving coder without a name, got %d %v", code, answer)
	}
}

// The two instance reads carry the models, which is what `assistant-list` and
// `assistant-show` read.
func TestTheInstanceReadsCarryTheModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	store := assistant.NewStore(stateDir)
	id := "33333333-3333-4333-8333-333333333333"
	store.Save(assistant.Instance{Summary: assistant.Summary{ID: id, Title: "Models", CoderID: "claude", Model: "opus", CheckModel: "haiku", TriggerModel: "sonnet"}})
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{assistants: conversations, workspace: workspace, watcher: assistant.NewWatcher(conversations, assistant.NewJobs(store), nil, nil)}
	r := gin.New()
	r.GET("/assistants/instances", s.handleAssistantInstances)
	r.GET("/assistants/instances/:id", s.handleAssistantInstanceRead)
	get := func(path string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d: %s", path, rec.Code, rec.Body.String())
		}
		var answer map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return answer
	}
	list, _ := get("/assistants/instances")["assistants"].([]any)
	if len(list) != 1 {
		t.Fatalf("want the one assistant listed, got %d", len(list))
	}
	if entry, _ := list[0].(map[string]any); entry["model"] != "opus" || entry["checkModel"] != "haiku" || entry["triggerModel"] != "sonnet" {
		t.Fatalf("want the models on the list entry, got %v", entry)
	}
	if one := get("/assistants/instances/" + id); one["model"] != "opus" || one["checkModel"] != "haiku" || one["triggerModel"] != "sonnet" {
		t.Fatalf("want the models on the transcript read, got %v", one)
	}
}

// The resolved read behind `assistant-models-get` answers the chain as a turn
// really runs it: an assistant whose chat is picked at the ring and nothing
// else set reads that pick for its chat, its checks and its triggers alike,
// each from the ring, and the checks and the triggers say they took it as
// the chat, because Same as chat means the assistant's resolved chat. A
// check pick of its own stands on the checks alone and is nobody's chat, and
// a Triggers pick of its own stands on the triggers alone as a pick, not as
// the chat.
func TestTheResolvedModelsReadFollowsTheChatPick(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	created, err := conversations.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	s := &Server{assistants: conversations, workspace: workspace}
	r := gin.New()
	r.GET("/assistants/models/resolved", s.handleAssistantModelsResolved)
	type choice struct {
		Model      string `json:"model"`
		Source     string `json:"source"`
		SameAsChat bool   `json:"sameAsChat"`
	}
	read := func() map[string]choice {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/assistants/models/resolved", nil)
		req.Header.Set(localapi.AssistantHeader, created.ID)
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("resolved read: %d %s", rec.Code, rec.Body.String())
		}
		var answer struct {
			Chat, Check, Trigger choice
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("resolved read: %v", err)
		}
		return map[string]choice{"chat": answer.Chat, "check": answer.Check, "trigger": answer.Trigger}
	}
	if _, err := conversations.SetModels(created.ID, assistant.ModelChoice{Chat: "fable", ChatSet: true}); err != nil {
		t.Fatalf("set the chat model: %v", err)
	}
	for purpose, got := range read() {
		if got.Model != "fable" || got.Source != "pick" || got.SameAsChat != (purpose != "chat") {
			t.Fatalf("want the %s resolution on the ring's chat pick fable, as the chat for the checks and the trigger, got %+v", purpose, got)
		}
	}
	if _, err := conversations.SetModels(created.ID, assistant.ModelChoice{Check: "haiku", CheckSet: true}); err != nil {
		t.Fatalf("set the check model: %v", err)
	}
	got := read()
	if got["check"] != (choice{Model: "haiku", Source: "pick"}) || got["chat"].Model != "fable" || got["trigger"] != (choice{Model: "fable", Source: "pick", SameAsChat: true}) {
		t.Fatalf("want the check pick on the checks alone, got %v", got)
	}
	if _, err := conversations.SetModels(created.ID, assistant.ModelChoice{Trigger: "sonnet", TriggerSet: true}); err != nil {
		t.Fatalf("set the trigger model: %v", err)
	}
	got = read()
	if got["trigger"] != (choice{Model: "sonnet", Source: "pick"}) || got["chat"].Model != "fable" || got["check"].Model != "haiku" {
		t.Fatalf("want the trigger pick on the triggers alone and never as the chat, got %v", got)
	}
}

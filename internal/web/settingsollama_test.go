package web

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/ollama"
	"github.com/marein/dev-cockpit/internal/ollama/ollamatest"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/internal/web/render"
)

func settingsOllamaServer(t *testing.T) (*gin.Engine, *Server) {
	t.Helper()
	r, s := modelSettingsServer(t)
	s.settings.Set(ollama.HostKey, "http://127.0.0.1:1")
	s.ollama = ollama.New(s.settings, "http://127.0.0.1:1/catalog", "")
	signedIn := func(h gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			ginsessions.Default(c).Set(sessionUserKey, "admin")
			h(c)
		}
	}
	r.GET(settingsOllamaPath, signedIn(s.handleSettingsOllama))
	r.POST(settingsOllamaPath, signedIn(s.handleSettingsOllamaSave))
	r.POST("/settings/coders/ollama/models/add", signedIn(s.handleSettingsOllamaModelAdd))
	r.POST("/settings/coders/ollama/models/delete", signedIn(s.handleSettingsOllamaModelDelete))
	r.GET("/ctx/:area", signedIn(s.handleCtx))
	return r, s
}

func TestTheCockpitSheetMarksTheOllamaEntryForItsPage(t *testing.T) {
	r, _ := settingsOllamaServer(t)
	withFakeOllamaOnPath(t)
	sheet := getPage(t, r, "/ctx/cockpit?path=/settings/coders/ollama")
	if !strings.Contains(sheet, `href="/settings/coders/ollama" class="list-group-item list-group-item-action is-nested active"`) {
		t.Fatal("want the Ollama row active in the cockpit sheet of its page")
	}
	if strings.Contains(sheet, `data-settings-coder="claude" data-dc-focus="work"`) && strings.Contains(sheet, `is-nested active" data-settings-coder="claude"`) {
		t.Fatal("want the coder row not marked on the Ollama page")
	}
	other := getPage(t, r, "/ctx/cockpit?path=/settings/coders/claude/models")
	if !strings.Contains(other, `is-nested active" data-settings-coder="claude"`) || strings.Contains(other, `href="/settings/coders/ollama" class="list-group-item list-group-item-action is-nested active"`) {
		t.Fatal("want a coder page to mark its coder row and not the Ollama row")
	}
}

func withFakeOllamaOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, ollama.Executable), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestTheOllamaPageRendersWholeWithTheStatusLineAndTheTabBar(t *testing.T) {
	r, _ := settingsOllamaServer(t)
	withFakeOllamaOnPath(t)
	page := getPage(t, r, settingsOllamaPath)
	if !strings.Contains(page, "dc-tabbar") {
		body := strings.Index(page, "<body")
		t.Fatalf("want the tab bar on the page, got the body head %q", page[body:min(len(page), body+1200)])
	}
	if !strings.HasSuffix(strings.TrimSpace(page), "</html>") {
		t.Fatalf("want the page rendered to its end, got %q", page[max(0, len(page)-200):])
	}
	if !strings.Contains(page, `id="settings-ollama-address"`) || !strings.Contains(page, `name="host"`) || !strings.Contains(page, "data-ollama-models") {
		t.Fatal("want the host field and the model list on the page")
	}
	for _, gone := range []string{"data-ollama-status", "found on PATH", "The server answers", "Signed in as", "Nobody is signed in"} {
		if strings.Contains(page, gone) {
			t.Fatalf("want no status line on the page, found %q", gone)
		}
	}
	if !strings.Contains(page, `href="/settings/coders/ollama" class="list-group-item list-group-item-action is-nested active"`) || !strings.Contains(page, `<span>Ollama</span><span class="text-secondary dc-row-note">Launcher</span>`) {
		t.Fatal("want the Ollama entry nested under Coder in the settings navigation with the Launcher note")
	}
}

func TestTheOllamaSectionIsAbsentWithoutTheExecutable(t *testing.T) {
	r, _ := settingsOllamaServer(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, settingsOllamaPath, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET without the executable: %d, want 404", rec.Code)
	}
	if rec := postForm(t, r, settingsOllamaPath, url.Values{"host": {"127.0.0.1:11434"}}); rec.Code != http.StatusNotFound {
		t.Fatalf("POST without the executable: %d, want 404", rec.Code)
	}
	if page := getPage(t, r, "/settings/assistant/models"); strings.Contains(page, `href="/settings/coders/ollama"`) || strings.Contains(page, "is-nested") {
		t.Fatal("want no Ollama entry and no nested coder group in the settings navigation without the executable")
	}
	withFakeOllamaOnPath(t)
	if page := getPage(t, r, settingsOllamaPath); !strings.Contains(page, `<h1 class="dc-work-title">Ollama</h1>`) || !strings.Contains(page, `<span class="ms-auto d-flex align-items-center gap-2 text-secondary small flex-shrink-0" data-coder-head><span class="dc-term-icon" aria-hidden="true"><svg class="coder-icon" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg" aria-hidden="true" data-launcher="ollama">`) || strings.Contains(page, "dc-work-tabs") {
		t.Fatal("want the page once the executable is on PATH, without a restart")
	}
	if page := getPage(t, r, "/settings/assistant/models"); !strings.Contains(page, `href="/settings/coders/ollama" class="list-group-item list-group-item-action is-nested"`) || !strings.Contains(page, `data-settings-coder="claude" data-dc-focus="work"`) {
		t.Fatal("want the Ollama entry nested beside the coder once the executable is on PATH")
	}
}

func TestTheOllamaPageAddsAndRemovesNamesAndRefusesOllamasOwn(t *testing.T) {
	r, s := settingsOllamaServer(t)
	stateDir := t.TempDir()
	ollamatest.Cache(stateDir, []string{"qwen3.5:cloud"}, []string{"nemotron-3-ultra:cloud", "gpt-oss:120b-cloud"}, nil, true)
	s.ollama = ollama.New(s.settings, "", stateDir)
	withFakeOllamaOnPath(t)
	page := getPage(t, r, settingsOllamaPath)
	if !strings.Contains(page, `data-ollama-model="nemotron-3-ultra:cloud" data-ollama-source="catalog"`) || !strings.Contains(page, `data-ollama-model="qwen3.5:cloud" data-ollama-source="server"`) || !strings.Contains(page, `data-ollama-model="gpt-oss:120b-cloud" data-ollama-source="catalog"`) || strings.Contains(page, `data-ollama-source="added"`) {
		t.Fatal("want the server's and the catalog's names listed with their sources and nothing added yet")
	}
	if rec := postForm(t, r, "/settings/coders/ollama/models/add", url.Values{"name": {"two words"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("add refused name: %d", rec.Code)
	}
	if rec := postForm(t, r, "/settings/coders/ollama/models/add", url.Values{"name": {" mistral-large:cloud "}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	page = getPage(t, r, settingsOllamaPath)
	if !strings.Contains(page, `data-ollama-model="mistral-large:cloud" data-ollama-source="added"`) || strings.Contains(page, `data-ollama-model="two words"`) {
		t.Fatal("want the typed name listed as added and the refused one absent")
	}
	if !strings.Contains(page, `<input type="hidden" name="name" value="mistral-large:cloud">`) || strings.Contains(page, `<input type="hidden" name="name" value="qwen3.5:cloud">`) || strings.Contains(page, `<input type="hidden" name="name" value="nemotron-3-ultra:cloud">`) {
		t.Fatal("want a remove for the added name and none for a server or catalog one")
	}
	if got := s.settings.Get(ollama.ModelsKey); got != `["mistral-large:cloud"]` {
		t.Fatalf("want the name stored under the ollama key, got %q", got)
	}
	refused := postForm(t, r, "/settings/coders/ollama/models/delete", url.Values{"name": {"nemotron-3-ultra:cloud"}})
	if refused.Code != http.StatusSeeOther {
		t.Fatalf("delete a catalog name: %d", refused.Code)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, settingsOllamaPath, nil)
	for _, cookie := range refused.Result().Cookies() {
		req.AddCookie(cookie)
	}
	r.ServeHTTP(rec, req)
	if page := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(page, "nemotron-3-ultra:cloud comes from Ollama and cannot be removed.") || !strings.Contains(page, `data-ollama-model="nemotron-3-ultra:cloud"`) {
		t.Fatalf("want the refusal shown and the catalog name kept, got %d", rec.Code)
	}
	if rec := postForm(t, r, "/settings/coders/ollama/models/delete", url.Values{"name": {"mistral-large:cloud"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete added: %d", rec.Code)
	}
	if page := getPage(t, r, settingsOllamaPath); strings.Contains(page, `data-ollama-model="mistral-large:cloud"`) {
		t.Fatal("want the added name gone")
	}
}

func TestTheOllamaModelRoutesAreAbsentWithoutTheExecutable(t *testing.T) {
	r, _ := settingsOllamaServer(t)
	for _, path := range []string{"/settings/coders/ollama/models/add", "/settings/coders/ollama/models/delete"} {
		if rec := postForm(t, r, path, url.Values{"name": {"mistral-large:cloud"}}); rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s without the executable: %d, want 404", path, rec.Code)
		}
	}
}

func TestTheWarningLineStandsUnderTheNewCoderSelectWhileSomethingIsMissing(t *testing.T) {
	r, s := settingsOllamaServer(t)
	r.GET("/coders/new", s.handleCoderNew)
	withFakeOllamaOnPath(t)
	snapshot := func(reachable bool) {
		stateDir := t.TempDir()
		ollamatest.Cache(stateDir, nil, nil, nil, reachable)
		s.ollama = ollama.New(s.settings, "", stateDir)
	}
	snapshot(false)
	for _, path := range []string{"/coders/new", "/settings/assistant/models"} {
		if page := getPage(t, r, path); !strings.Contains(page, "data-ollama-warning>"+ollama.ErrServer.Error()+"</div>") {
			t.Fatalf("want the warning on %s while the snapshot says the server does not answer", path)
		}
	}
	snapshot(true)
	for _, path := range []string{"/coders/new", "/settings/coders/claude/models"} {
		if page := getPage(t, r, path); strings.Contains(page, "data-ollama-warning") {
			t.Fatalf("want no warning on %s while the server answers", path)
		}
	}
	snapshot(false)
	t.Setenv("PATH", t.TempDir())
	if page := getPage(t, r, "/coders/new"); strings.Contains(page, "data-ollama-warning") {
		t.Fatal("want no warning while ollama is not installed")
	}
}

func testTemplates(t *testing.T) *template.Template {
	t.Helper()
	serves, err := pluginhost.ConfigureServe(nil, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := newStaticAssetManifest(serves)
	if err != nil {
		t.Fatal(err)
	}
	return render.HTMLTemplate(assets.assetPath, "test", "test", serves)
}

func renderTemplate(t *testing.T, tpl *template.Template, name string, data any) string {
	t.Helper()
	var out bytes.Buffer
	if err := tpl.ExecuteTemplate(&out, name, data); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out.String()
}

func TestTheMarkRendersForALaunchedSessionOnly(t *testing.T) {
	tpl := testTemplates(t)
	icon := func(launcher string) string {
		return renderTemplate(t, tpl, "steered_icon.gohtml", map[string]any{"ID": "sid", "Coder": "claude", "Launcher": launcher, "Steered": false, "Working": false})
	}
	marked := icon("ollama")
	for _, want := range []string{`<span class="dc-term-icon running" data-launcher="ollama"`, `<span class="dc-icon-badged" title="Claude via Ollama" aria-label="Claude via Ollama">`, `<svg class="coder-icon" viewBox="0 0 24 24"`, `<svg class="dc-icon-badge" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg" aria-hidden="true" data-launcher="ollama">`} {
		if !strings.Contains(marked, want) {
			t.Fatalf("want %s in the marked icon, got %s", want, marked)
		}
	}
	plain := icon("")
	if strings.Contains(plain, "data-launcher") || strings.Contains(plain, "dc-icon-badged") || !strings.Contains(plain, `<svg class="coder-icon" viewBox="0 0 24 24"`) {
		t.Fatalf("want the coder's own icon without the badge for another session, got %s", plain)
	}
}

func TestTheCoderIconWithTheLauncherIsTheCoderBadgedWithTheLlama(t *testing.T) {
	tpl := testTemplates(t)
	claude := renderTemplate(t, tpl, "coder_icon_claude_paths.gohtml", nil)
	llama := renderTemplate(t, tpl, "coder_icon_ollama_paths.gohtml", nil)
	if !strings.HasPrefix(llama, `<path d="M`) || strings.Count(llama, "<path") != 1 {
		t.Fatalf("want one path of the Ollama logo, got %s", llama)
	}
	badged := renderTemplate(t, tpl, "coder_icon.gohtml", map[string]any{"ID": "claude", "Launcher": "ollama"})
	if !strings.HasPrefix(badged, `<span class="dc-icon-badged" title="Claude via Ollama" aria-label="Claude via Ollama">`) || !strings.HasSuffix(badged, "</svg></span>") {
		t.Fatalf("want one badged container around the coder icon, got %s", badged)
	}
	if !strings.Contains(badged, claude) || !strings.Contains(badged, llama) || strings.Count(badged, "<svg") != 2 {
		t.Fatalf("want the claude paths and the ollama paths in that container, got %s", badged)
	}
	if !strings.Contains(badged, `<svg class="dc-icon-badge" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg" aria-hidden="true" data-launcher="ollama">`) || strings.Contains(badged, "<i ") {
		t.Fatalf("want the llama as the badge svg and no font glyph, got %s", badged)
	}
	plain := renderTemplate(t, tpl, "coder_icon.gohtml", map[string]any{"ID": "claude"})
	if strings.Contains(plain, "dc-icon-badged") || !strings.Contains(plain, claude) || strings.Contains(plain, llama) {
		t.Fatalf("want the bare coder icon without a launcher, got %s", plain)
	}
	own := renderTemplate(t, tpl, "coder_icon.gohtml", map[string]any{"ID": "ollama"})
	if !strings.HasPrefix(own, `<svg class="coder-icon" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg" aria-hidden="true" data-launcher="ollama">`) || !strings.Contains(own, llama) || strings.Contains(own, "dc-icon-badged") {
		t.Fatalf("want Ollama's own entry as the plain llama, got %s", own)
	}
}

func TestAnAssistantAnswerNamesItsModelInItsHead(t *testing.T) {
	_, s := settingsOllamaServer(t)
	tpl := testTemplates(t)
	bubble := func(model string) string {
		view := s.assistantMessageView("inst", assistant.Message{ID: "m1", Role: assistant.RoleAssistant, Content: "hello", CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), State: assistant.StateComplete, Model: model}, false, true, "claude")
		view.AudioURL = "/assistants/inst/messages/m1/audio"
		return renderTemplate(t, tpl, "chat_message.gohtml", render.ChatMessageData{Message: view})
	}
	launched := bubble("ollama/nemotron-3-ultra:cloud")
	name := `<span class="text-secondary small text-truncate" title="nemotron-3-ultra" data-assistant-model="ollama/nemotron-3-ultra:cloud">nemotron-3-ultra</span>`
	at, stamp, speaker, body := strings.Index(launched, name), strings.Index(launched, `<dc-time datetime="2026-09-30T12:00:00Z"`), strings.Index(launched, speakerButton), strings.Index(launched, `data-assistant-text`)
	if at < 0 || stamp < 0 || speaker < 0 || !(stamp < at && at < speaker && speaker < body) {
		t.Fatalf("want the model in the head after the time and before the speaker, got %s", launched)
	}
	if between := strings.TrimSpace(launched[at+len(name) : speaker]); between != "" {
		t.Fatalf("want the model right before the speaker, got %q between them", between)
	}
	if strings.Contains(launched, "dc-msg-model") || strings.Contains(launched, "<svg") || strings.Contains(launched, "data-launcher") {
		t.Fatalf("want text only in the head and no line of its own, got %s", launched)
	}
	if alias := bubble("opus"); !strings.Contains(alias, `title="opus" data-assistant-model="opus">opus</span>`) {
		t.Fatalf("want the alias as it is, got %s", alias)
	}
	if none := bubble(""); strings.Contains(none, "data-assistant-model") {
		t.Fatalf("want no model element on an answer without a model, got %s", none)
	}
}

const speakerButton = `<button type="button" class="btn btn-icon btn-sm btn-ghost-secondary px-2 position-relative flex-shrink-0"`

func TestANoteNamesItsModelInTheHeadWideAndInTheFoldNarrow(t *testing.T) {
	_, s := settingsOllamaServer(t)
	tpl := testTemplates(t)
	long := strings.Repeat("The job is finished and every file it was about is written. ", 5)
	note := func(model, content string) string {
		view := s.assistantMessageView("inst", assistant.Message{ID: "n1", Role: assistant.RoleCockpit, Content: content, CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), State: assistant.StateComplete, Model: model, Note: &assistant.Note{Source: assistant.NoteCheck, Headline: "DONE: readme-task", Verdict: string(assistant.VerdictDone)}}, false, true, "claude")
		view.AudioURL = "/assistants/inst/messages/n1/audio"
		return renderTemplate(t, tpl, "chat_message.gohtml", render.ChatMessageData{Message: view})
	}
	wide := `<span class="text-secondary small text-truncate d-none d-md-block" title="gpt-oss:120b-cloud" data-assistant-model="ollama/gpt-oss:120b-cloud">gpt-oss:120b-cloud</span>`
	narrow := `<span class="text-secondary small text-truncate d-block d-md-none mt-1" title="gpt-oss:120b-cloud" data-assistant-model="ollama/gpt-oss:120b-cloud">gpt-oss:120b-cloud</span>`
	folded := note("ollama/gpt-oss:120b-cloud", long)
	head := folded[strings.Index(folded, `data-assistant-note="check"`):]
	head = head[:strings.Index(head, "</div>")]
	at, speaker := strings.Index(head, wide), strings.Index(head, speakerButton)
	if at < 0 || speaker < 0 || strings.TrimSpace(head[at+len(wide):speaker]) != "" {
		t.Fatalf("want the model in the note's head right before the speaker with the wide class, got %s", head)
	}
	rest := folded[strings.Index(folded, "data-assistant-note-rest"):]
	rest = rest[:strings.Index(rest, "data-assistant-text")]
	if !strings.Contains(rest, narrow) {
		t.Fatalf("want the model as the first line inside the fold with the narrow class, got %s", rest)
	}
	if strings.Count(folded, "data-assistant-model=") != 2 || strings.Contains(folded, "dc-msg-model") {
		t.Fatalf("want the model twice, once per width, and no line of its own, got %s", folded)
	}
	whole := note("ollama/gpt-oss:120b-cloud", "Short and done.")
	if strings.Contains(whole, "data-assistant-note-rest") || !strings.Contains(whole, wide) || strings.Index(whole, narrow) < 0 || strings.Index(whole, narrow) > strings.Index(whole, "data-assistant-text") {
		t.Fatalf("want a note without a fold to carry the narrow line under its head above the text, got %s", whole)
	}
	if none := note("", long); strings.Contains(none, "data-assistant-model") {
		t.Fatalf("want no model element on a note without a model, got %s", none)
	}
}

func TestTheModelsFrameFlagsAnOllamaPick(t *testing.T) {
	_, s := settingsOllamaServer(t)
	withFakeOllamaOnPath(t)
	frame := s.modelsFrame("claude", assistant.ModelPicks{Chat: "ollama/qwen3.5:cloud"})
	for deadline := time.Now().Add(5 * time.Second); frame.Warning == "" && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		frame = s.modelsFrame("claude", assistant.ModelPicks{Chat: "ollama/qwen3.5:cloud"})
	}
	if frame.Launcher != "ollama" || frame.Warning != ollama.ErrServer.Error() {
		t.Fatalf("want the launcher flagged and the warning carried, got %+v", frame)
	}
	if plain := s.modelsFrame("claude", assistant.ModelPicks{Chat: "haiku"}); plain.Launcher != "" {
		t.Fatalf("want an alias unflagged, got %+v", plain)
	}
	t.Setenv("PATH", t.TempDir())
	if none := s.modelsFrame("claude", assistant.ModelPicks{Chat: "ollama/qwen3.5:cloud"}); none.Launcher != "ollama" || none.Warning != "" {
		t.Fatalf("want the flag by the prefix rule alone and no warning while ollama is not installed, got %+v", none)
	}
}

func TestTheTabStripRendersAResumableOllamaSessionWithTheMark(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	r, s := settingsOllamaServer(t)
	r.GET("/terminal-tabs", s.handleTerminalTabsFragment)
	withFakeOllamaOnPath(t)
	workdir := filepath.Join(s.projects.Root, "smoke")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := s.coders[0].Coder()
	for _, start := range []coder.SessionStart{
		{SessionID: "11111111-2222-4333-8444-555555555555", Workdir: workdir, Model: "ollama/qwen3.5:cloud"},
		{SessionID: "11111111-2222-4333-8444-666666666666", Workdir: workdir, Model: "haiku"},
	} {
		claude.SessionRuntime().StartCommand(start)
		claude.SessionRuntime().(coder.SessionStarter).SessionStarted(start)
	}
	transcripts := filepath.Join(home, ".claude", "projects", "smoke")
	if err := os.MkdirAll(transcripts, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"11111111-2222-4333-8444-555555555555", "11111111-2222-4333-8444-666666666666"} {
		line := `{"type":"user","cwd":"` + workdir + `","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"hello"}}` + "\n"
		if err := os.WriteFile(filepath.Join(transcripts, id+".jsonl"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/terminal-tabs", nil))
	page := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(page, `data-resume-id="11111111-2222-4333-8444-555555555555"`) || !strings.Contains(page, `data-resume-id="11111111-2222-4333-8444-666666666666"`) {
		t.Fatalf("want both stored sessions as resume rows, got %d: %s", rec.Code, page)
	}
	if !strings.Contains(page, `data-notify-target="11111111-2222-4333-8444-555555555555" data-launcher="ollama"`) {
		t.Fatal("want the remembered session's resume row carrying the mark")
	}
	if strings.Contains(page, `data-notify-target="11111111-2222-4333-8444-666666666666" data-launcher`) {
		t.Fatal("want the plain session's resume row without the mark")
	}
	if !strings.HasSuffix(strings.TrimSpace(page), "</nav>") && !strings.Contains(page[len(page)-200:], "</div>") {
		t.Fatalf("want the strip rendered to its end, got the end %q", page[max(0, len(page)-200):])
	}
}

func TestTheTabStripTemplateRendersRunningInactiveAndResumableSessions(t *testing.T) {
	serves, err := pluginhost.ConfigureServe(nil, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := newStaticAssetManifest(serves)
	if err != nil {
		t.Fatal(err)
	}
	tpl := render.HTMLTemplate(assets.assetPath, "test", "test", serves)
	launched := render.TerminalTab{ID: "aaa", Name: "through ollama", URL: "/coders/aaa", Project: "smoke", Coder: "claude", Launcher: "ollama", Kind: "coder", Working: true}
	plain := render.TerminalTab{ID: "bbb", Name: "plain", URL: "/coders/bbb", Project: "smoke", Coder: "claude", Kind: "coder", HasNews: true}
	shell := render.TerminalTab{ID: "ccc", Name: "a shell", URL: "/shells/ccc", Project: "smoke", Kind: "shell"}
	group := render.StripTab{TerminalTab: render.TerminalTab{ID: "grp", Name: "pair", URL: "/splits/grp", Project: "smoke", Kind: "split", Group: "grp"}, Members: []render.TerminalTab{launched, shell}}
	page := render.Page{
		CSRFToken:    "token",
		Steered:      map[string]bool{"bbb": true},
		SteerPrefill: map[string]string{"bbb": "carry on"},
		Working:      map[string]bool{"aaa": true},
		QuickNav: render.QuickNav{
			Active:         []render.TerminalTab{launched, plain, shell},
			Strip:          []render.StripTab{{TerminalTab: launched}, {TerminalTab: plain}, {TerminalTab: shell}, group},
			CurrentID:      "bbb",
			CurrentProject: "smoke",
			CurrentPath:    "/coders/bbb",
			AllProjects: []render.ProjectNav{{
				Name: "smoke", Path: "/tmp/smoke", EditorURL: "/projects/smoke/editor", NewCoderURL: "/coders/new?project=smoke", NewShellURL: "/shells/new?project=smoke", Active: true,
				Terminals: []render.ProjectNavItem{
					{ID: "aaa", Name: "through ollama", URL: "/coders/aaa", Kind: "coder", Coder: "claude", Launcher: "ollama"},
					{ID: "bbb", Name: "plain", URL: "/coders/bbb", Kind: "coder", Coder: "claude", HasNews: true},
					{ID: "ccc", Name: "a shell", URL: "/shells/ccc", Kind: "shell"},
				},
				InactiveCoders: []render.ProjectNavItem{
					{ID: "ddd", Name: "stored through ollama", URL: "/coders/ddd/resume", Coder: "claude", Launcher: "ollama", HasNews: true},
					{ID: "eee", Name: "stored plain", URL: "/coders/eee/resume", Coder: "claude"},
				},
			}},
		},
	}
	var out bytes.Buffer
	if err := tpl.ExecuteTemplate(&out, "terminal_tabs.gohtml", page); err != nil {
		t.Fatalf("the tab strip does not render: %v", err)
	}
	strip := out.String()
	for _, want := range []string{`data-tab-id="aaa"`, `data-tab-id="bbb"`, `data-tab-id="ccc"`, `data-tab-id="grp"`, `data-resume-id="ddd"`, `data-resume-id="eee"`} {
		if !strings.Contains(strip, want) {
			t.Fatalf("want %s in the strip", want)
		}
	}
	for _, want := range []string{`data-notify-target="aaa" data-launcher="ollama"`, `data-notify-target="ddd" data-launcher="ollama"`} {
		if !strings.Contains(strip, want) {
			t.Fatalf("want the mark on %s", want)
		}
	}
	for _, id := range []string{"bbb", "ccc", "eee"} {
		if strings.Contains(strip, `data-notify-target="`+id+`" data-launcher`) {
			t.Fatalf("want no mark on %s", id)
		}
	}
}

package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/ollama"
	"github.com/marein/dev-cockpit/internal/ollama/ollamatest"
	"github.com/marein/dev-cockpit/internal/settings"
)

func withOllamaOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ollama"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func withoutOllamaOnPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

const launcherHead = "cd '/work' && exec ollama 'launch' 'claude' '--model' 'qwen3.5:cloud' '--yes' '--' "

func TestStartCommandWrapsAnOllamaPickInTheLauncher(t *testing.T) {
	r := runtime{sessions: newOllamaSessions(t.TempDir())}
	command := r.StartCommand(coder.SessionStart{
		SessionID: "sid", Name: "name", Workdir: "/work", Model: " ollama/qwen3.5:cloud ", Task: "-dxdebug.idekey=PHPSTORM",
	})
	if !strings.HasPrefix(command, launcherHead) {
		t.Fatalf("want the launcher in front of claude's flags, got %s", command)
	}
	rest := strings.TrimPrefix(command, launcherHead)
	if strings.Contains(rest, "--model") {
		t.Fatalf("the launcher puts the model in front itself, claude must get none: %s", command)
	}
	if !strings.Contains(rest, "--session-id 'sid'") || !strings.Contains(rest, "--name 'name'") || !strings.Contains(rest, "--settings") {
		t.Fatalf("want every flag of a plain start behind the launcher, got %s", command)
	}
	if !strings.HasSuffix(rest, " -- '-dxdebug.idekey=PHPSTORM'") || strings.Count(rest, " -- ") != 1 {
		t.Fatalf("want claude's own separator before the task kept, got %s", command)
	}
	plain := r.StartCommand(coder.SessionStart{SessionID: "sid", Workdir: "/work", Model: "haiku"})
	if strings.Contains(plain, "ollama") || !strings.Contains(plain, "exec claude") {
		t.Fatalf("a plain pick must not touch the launcher: %s", plain)
	}
}

func TestResumeCommandWrapsARememberedSessionAndANormalOneNot(t *testing.T) {
	r := runtime{sessions: newOllamaSessions(t.TempDir())}
	start := coder.SessionStart{SessionID: "sid", Workdir: "/work", Model: "ollama/qwen3.5:cloud"}
	r.StartCommand(start)
	r.SessionStarted(start)
	resume := r.ResumeCommand("sid", "/work", true)
	if !strings.HasPrefix(resume, launcherHead) || !strings.HasSuffix(resume, " --resume 'sid'") || !strings.Contains(resume, "--permission-mode auto") {
		t.Fatalf("want the remembered model on the launcher and the resume behind it, got %s", resume)
	}
	other := r.ResumeCommand("other", "/work", true)
	if strings.Contains(other, "ollama") || !strings.HasPrefix(other, "cd '/work' && exec claude ") || !strings.HasSuffix(other, " --resume 'other'") {
		t.Fatalf("a session started without the launcher resumes without it: %s", other)
	}
	again := runtime{sessions: newOllamaSessions(filepath.Dir(r.sessions.path))}
	if got := again.ResumeCommand("sid", "/work", false); !strings.HasPrefix(got, launcherHead) {
		t.Fatalf("want the model remembered on disk, got %s", got)
	}
}

func TestAStartCommandWritesNothingAndTheStartedSessionIsRemembered(t *testing.T) {
	dir := t.TempDir()
	r := runtime{sessions: newOllamaSessions(dir)}
	start := coder.SessionStart{SessionID: "sid", Workdir: "/work", Model: "ollama/qwen3.5:cloud"}
	if !strings.HasPrefix(r.StartCommand(start), launcherHead) {
		t.Fatal("want the launcher on the command")
	}
	if _, err := os.Stat(filepath.Join(dir, ollamaSessionsFile)); !os.IsNotExist(err) || r.sessions.modelOf("sid") != "" {
		t.Fatalf("want the builder to write nothing, got %v and %q", err, r.sessions.modelOf("sid"))
	}
	r.SessionStarted(start)
	if r.sessions.modelOf("sid") != "qwen3.5:cloud" {
		t.Fatalf("want the started session remembered, got %q", r.sessions.modelOf("sid"))
	}
	r.SessionStarted(coder.SessionStart{SessionID: "plain", Workdir: "/work", Model: "haiku"})
	if r.sessions.modelOf("plain") != "" {
		t.Fatal("a plain start is nothing to remember")
	}
}

func TestTheLaunchedSessionsAreReadOnceAndAfterEveryWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ollamaSessionsFile)
	if err := os.WriteFile(path, []byte(`{"aaa":"qwen3.5:cloud"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newOllamaSessions(dir)
	if s.modelOf("aaa") != "qwen3.5:cloud" {
		t.Fatalf("want the file read at construction, got %q", s.modelOf("aaa"))
	}
	if err := os.WriteFile(path, []byte(`{"aaa":"changed:cloud"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if s.modelOf("aaa") != "qwen3.5:cloud" || s.modelOf("bbb") != "" {
		t.Fatalf("want two lookups answered from the one load and never from the file, got %q %q", s.modelOf("aaa"), s.modelOf("bbb"))
	}
	s.remember("bbb", "glm-5.1:cloud")
	if s.modelOf("aaa") != "qwen3.5:cloud" || s.modelOf("bbb") != "glm-5.1:cloud" {
		t.Fatalf("want the write to carry the held map and be read back, got %q %q", s.modelOf("aaa"), s.modelOf("bbb"))
	}
	if held := s.load(); held["aaa"] != "qwen3.5:cloud" || held["bbb"] != "glm-5.1:cloud" || len(held) != 2 {
		t.Fatalf("want the file to hold what the map holds, got %v", held)
	}
	s.forget("aaa")
	if s.modelOf("aaa") != "" || s.modelOf("bbb") != "glm-5.1:cloud" {
		t.Fatalf("want the forgotten name gone from the map after the write, got %q %q", s.modelOf("aaa"), s.modelOf("bbb"))
	}
}

func TestAnOllamaNameTypedWithALeadingDashIsRefused(t *testing.T) {
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	c := New(t.TempDir(), "", store, ollama.New(store, "http://127.0.0.1:1/catalog", ""))
	err := c.ModelRepository().Add("ollama/-x")
	if err == nil || !strings.Contains(err.Error(), "cannot start with a dash") {
		t.Fatalf("want the shared sentence for a dash behind the prefix, got %v", err)
	}
	if got := store.Get(ollama.ModelsKey); got != "" {
		t.Fatalf("want nothing stored for a refused name, got %s", got)
	}
	if err := c.ModelRepository().Add("ollama/glm-5.1:cloud"); err != nil || store.Get(ollama.ModelsKey) != `["glm-5.1:cloud"]` {
		t.Fatalf("want a clean name stored bare, got %v and %s", err, store.Get(ollama.ModelsKey))
	}
}

func TestEnvCarriesTheOllamaHostWhenConfigured(t *testing.T) {
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	r := runtime{launcher: ollama.New(store, "http://127.0.0.1:1/catalog", "")}
	if env := r.Env(); env["OLLAMA_HOST"] != "" || env["CLAUDE_CODE_NO_FLICKER"] != "1" {
		t.Fatalf("want no host without a setting, got %v", env)
	}
	store.Set(ollama.HostKey, "http://ollama.home:11434")
	if env := r.Env(); env["OLLAMA_HOST"] != "http://ollama.home:11434" || env["CLAUDE_CODE_NO_FLICKER"] != "1" {
		t.Fatalf("want the configured host beside the session environment, got %v", env)
	}
}

func TestTheTurnCommandRunsAnOllamaPickThroughTheLauncher(t *testing.T) {
	withOllamaOnPath(t)
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	store.Set(ollama.HostKey, "http://ollama.home:11434")
	r := &runner{sessions: stubSessions{}, launcher: ollama.New(store, "http://127.0.0.1:1/catalog", "")}
	cmd, err := r.Command(assistant.TurnRequest{SessionID: sessionID, Title: "an assistant", Workdir: "/work", Prompt: "-hello", Model: "ollama/qwen3.5:cloud"})
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(cmd.Args, " ")
	if cmd.Name != "ollama" || !strings.HasPrefix(argv, "launch claude --model qwen3.5:cloud --yes -- -p --session-id "+sessionID+" ") {
		t.Fatalf("want the launcher around the turn, got %s %s", cmd.Name, argv)
	}
	if strings.Contains(argv, "--model ollama") || strings.Count(argv, "--model") != 1 {
		t.Fatalf("claude must get no model of its own: %s", argv)
	}
	if !strings.HasSuffix(argv, " --verbose -- -hello") {
		t.Fatalf("want the prompt behind claude's own separator, got %s", argv)
	}
	if strings.Join(cmd.Env, ",") != "OLLAMA_HOST=http://ollama.home:11434" {
		t.Fatalf("want the host in the turn environment, got %v", cmd.Env)
	}
	plain, err := r.Command(assistant.TurnRequest{SessionID: sessionID, Workdir: "/work", Prompt: "hello", Model: "haiku"})
	if err != nil || plain.Name != "claude" || plain.Env != nil || !strings.Contains(strings.Join(plain.Args, " "), " --model haiku ") {
		t.Fatalf("a plain pick must run claude as before, got %+v %v", plain, err)
	}
}

func TestATurnOnAnOllamaPickIsRefusedWithoutTheExecutable(t *testing.T) {
	withoutOllamaOnPath(t)
	r := &runner{sessions: stubSessions{}}
	_, err := r.Command(assistant.TurnRequest{SessionID: sessionID, Workdir: "/work", Prompt: "hello", Model: "ollama/qwen3.5:cloud"})
	if err == nil || err.Error() != "The ollama executable was not found on PATH." {
		t.Fatalf("want the one sentence about the executable, got %v", err)
	}
	if _, err := r.Command(assistant.TurnRequest{SessionID: sessionID, Workdir: "/work", Prompt: "hello", Model: "haiku"}); err != nil {
		t.Fatalf("a plain pick needs no ollama: %v", err)
	}
}

func modelNames(models []coder.Model) string {
	var parts []string
	for _, m := range models {
		parts = append(parts, m.Source+":"+m.Name)
	}
	return strings.Join(parts, ",")
}

func TestTheCoderRefusesAnOllamaPickWithoutTheExecutableAndListsTheNamesWithIt(t *testing.T) {
	withoutOllamaOnPath(t)
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	down := t.TempDir()
	ollamatest.Cache(down, []string{"qwen3.5:cloud"}, []string{"nemotron-3-ultra:cloud"}, nil, false)
	c := New(t.TempDir(), "", store, ollama.New(store, "", down))
	if err := c.CheckModel("ollama/qwen3.5:cloud"); err == nil || !strings.Contains(err.Error(), "ollama executable") {
		t.Fatalf("want the refusal, got %v", err)
	}
	if err := c.CheckModel("haiku"); err != nil {
		t.Fatalf("a plain pick is not checked against ollama: %v", err)
	}
	if got := modelNames(c.ModelRepository().List()); got != "cli:fable,cli:opus,cli:sonnet,cli:haiku" {
		t.Fatalf("want no ollama names without the executable, got %s", got)
	}
	if strings.Contains(c.ModelRepository().Note(), "Ollama") {
		t.Fatalf("want the note silent about Ollama, got %q", c.ModelRepository().Note())
	}
	withOllamaOnPath(t)
	if err := c.CheckModel("ollama/qwen3.5:cloud"); err == nil || err.Error() != "The Ollama server does not answer." {
		t.Fatalf("want the pick refused while the snapshot says the server does not answer, got %v", err)
	}
	up := t.TempDir()
	ollamatest.Cache(up, []string{"qwen3.5:cloud"}, []string{"nemotron-3-ultra:cloud"}, nil, true)
	c = New(t.TempDir(), "", store, ollama.New(store, "", up))
	if err := c.CheckModel("ollama/qwen3.5:cloud"); err != nil {
		t.Fatalf("want the pick accepted while the snapshot says the server answers: %v", err)
	}
	want := "cli:fable,cli:opus,cli:sonnet,cli:haiku,ollama:ollama/qwen3.5:cloud,ollama:ollama/nemotron-3-ultra:cloud"
	if got := modelNames(c.ModelRepository().List()); got != want {
		t.Fatalf("want the shared names behind the prefix, marked ollama\n got %s\nwant %s", got, want)
	}
	if !c.ModelRepository().Exists("ollama/qwen3.5:cloud") {
		t.Fatal("want a listed ollama name to exist")
	}
	if !strings.Contains(c.ModelRepository().Note(), "cloud models only") {
		t.Fatalf("want the note to say what ollama/ names are, got %q", c.ModelRepository().Note())
	}
}

func TestAnAssistantsPickOutlivesTheCockpitAndLeavesWithItsSession(t *testing.T) {
	withOllamaOnPath(t)
	home, stateDir, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	launcher := ollama.New(nil, "http://127.0.0.1:1/catalog", "")
	c := New(stateDir, "", nil, launcher)
	r := &runner{sessions: c.sessions, launcher: launcher, launched: c.launched}
	if _, err := r.Command(assistant.TurnRequest{SessionID: sessionID, Title: "an assistant", Workdir: cwd, Prompt: "hello", Model: "ollama/nemotron-3-ultra:cloud"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Command(assistant.TurnRequest{SessionID: "plain-session", Workdir: cwd, Prompt: "hello", Model: "haiku"}); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, filepath.Join(home, ".claude", "projects"), "p1", sessionID, cwdLine(cwd), titleLine("an assistant"))

	restarted := New(stateDir, "", nil, launcher)
	again := &runner{sessions: restarted.sessions, launcher: launcher, launched: restarted.launched}
	stderr := "Upgrade required\nError: error running upgrade: could not open a new TTY\n"
	p := again.Parse(sessionID, make(chan assistant.Event, 1)).(*claudeParser)
	if p.picked != "ollama/nemotron-3-ultra:cloud" {
		t.Fatalf("want the pick read back after a restart, got %q", p.picked)
	}
	if err := p.Diagnose(errors.New("exit status 1"), stderr); err == nil || err.Error() != "ollama/nemotron-3-ultra:cloud needs a paid Ollama plan, pick another model at the ring." {
		t.Fatalf("want the plan sentence after a restart, got %v", err)
	}
	if picked := again.Parse("plain-session", make(chan assistant.Event, 1)).(*claudeParser).picked; picked != "" {
		t.Fatalf("want a plain turn remembered as nothing, got %q", picked)
	}

	if _, err := again.Command(assistant.TurnRequest{SessionID: sessionID, Resume: true, Workdir: cwd, Prompt: "hello", Model: "haiku"}); err != nil {
		t.Fatal(err)
	}
	if picked := New(stateDir, "", nil, launcher).launched.modelOf(sessionID); picked != "" {
		t.Fatalf("want a turn on a plain pick to forget the launcher, got %q", picked)
	}
	if _, err := again.Command(assistant.TurnRequest{SessionID: sessionID, Resume: true, Workdir: cwd, Prompt: "hello", Model: "ollama/glm-5.1:cloud"}); err != nil {
		t.Fatal(err)
	}
	if err := again.DeleteSession(sessionID); err != nil {
		t.Fatal(err)
	}
	if picked := New(stateDir, "", nil, launcher).launched.modelOf(sessionID); picked != "" {
		t.Fatalf("want the deleted session forgotten on disk, got %q", picked)
	}
}

func TestDeleteSessionForgetsTheOllamaModel(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	launched := newOllamaSessions(t.TempDir())
	launched.remember("aaa", "qwen3.5:cloud")
	launched.remember("bbb", "glm-5.1:cloud")
	r := &sessionRepository{stateRoot: root, ollama: launched}
	writeTranscript(t, root, "p1", "aaa", cwdLine(cwd), titleLine("one"))
	if err := r.DeleteSession("aaa"); err != nil {
		t.Fatal(err)
	}
	if launched.modelOf("aaa") != "" || launched.modelOf("bbb") != "glm-5.1:cloud" {
		t.Fatalf("want the deleted session forgotten and the other kept, got %v", launched.load())
	}
	if err := r.DeleteSession("bbb"); err == nil {
		t.Fatal("want a session without a transcript refused")
	}
	if launched.modelOf("bbb") != "glm-5.1:cloud" {
		t.Fatal("a refused delete must keep the model")
	}
}

func TestThePrefixedNamesOfTheClaudeListBelongToTheOllamaPage(t *testing.T) {
	withOllamaOnPath(t)
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	store.Set(ollama.HostKey, "http://127.0.0.1:1")
	repo := New(t.TempDir(), "", store, ollama.New(store, "http://127.0.0.1:1/catalog", "")).ModelRepository()
	if err := repo.Add(" ollama/mistral-large:cloud "); err != nil {
		t.Fatal(err)
	}
	if err := repo.Add("claude-haiku-4-5"); err != nil {
		t.Fatal(err)
	}
	if got := store.Get(ollama.ModelsKey); got != `["mistral-large:cloud"]` {
		t.Fatalf("want the prefixed name in the shared list, got %q", got)
	}
	if got := store.Get(assistant.ModelAddedKey("claude")); got != `["claude-haiku-4-5"]` {
		t.Fatalf("want only the plain name under claude's own key, got %q", got)
	}
	if got := modelNames(repo.List()); !strings.Contains(got, "ollama:ollama/mistral-large:cloud") || !strings.HasSuffix(got, "added:claude-haiku-4-5") {
		t.Fatalf("want the shared name listed as ollama and the plain one as added, got %s", got)
	}
	if err := repo.Delete("ollama/mistral-large:cloud"); err == nil || !strings.Contains(err.Error(), "Ollama settings page") {
		t.Fatalf("want a prefixed delete pointed to the Ollama page, got %v", err)
	}
	if got := store.Get(ollama.ModelsKey); got != `["mistral-large:cloud"]` {
		t.Fatalf("want the shared list untouched by the refused delete, got %q", got)
	}
	if err := repo.Delete("claude-haiku-4-5"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Lookup(assistant.ModelAddedKey("claude")); ok {
		t.Fatal("want claude's own added name gone")
	}
}

func TestTheCoderNamesTheLauncherOfARememberedSession(t *testing.T) {
	c := New(t.TempDir(), "", nil, ollama.New(nil, "http://127.0.0.1:1/catalog", ""))
	c.launched.remember("sid", "qwen3.5:cloud")
	if got := coder.LauncherForSession(c, "sid"); got != "ollama" {
		t.Fatalf("want the remembered session marked as launched, got %q", got)
	}
	if got := coder.LauncherForSession(c, "other"); got != "" {
		t.Fatalf("want another session unmarked, got %q", got)
	}
	if got := coder.LauncherFor(c).SessionModel("sid"); got != "ollama/qwen3.5:cloud" {
		t.Fatalf("want the remembered session's pick with its prefix, got %q", got)
	}
	if got := coder.LauncherFor(c).Launcher("ollama/qwen3.5:cloud"); got != "ollama" {
		t.Fatalf("want an ollama/ pick named as the launcher's, got %q", got)
	}
	if got := coder.LauncherFor(c).Launcher("haiku"); got != "" {
		t.Fatalf("want a plain pick without a launcher, got %q", got)
	}
}

func TestTheLaunchersUpgradeDialogIsOnePlainSentence(t *testing.T) {
	withOllamaOnPath(t)
	r := &runner{sessions: stubSessions{}, launched: newOllamaSessions(t.TempDir())}
	if _, err := r.Command(assistant.TurnRequest{SessionID: sessionID, Workdir: "/work", Prompt: "hello", Model: "ollama/nemotron-3-ultra:cloud"}); err != nil {
		t.Fatal(err)
	}
	stderr := "Upgrade required\nError: error running upgrade: could not open a new TTY: open /dev/tty: no such device or address\n"
	p := r.Parse(sessionID, make(chan assistant.Event, 1))
	if err := p.Diagnose(errors.New("exit status 1"), stderr); err == nil || err.Error() != "ollama/nemotron-3-ultra:cloud needs a paid Ollama plan, pick another model at the ring." {
		t.Fatalf("want the plan sentence naming the pick, got %v", err)
	}
	plain := r.Parse("other-session", make(chan assistant.Event, 1))
	if err := plain.Diagnose(errors.New("exit status 1"), stderr); err != nil {
		t.Fatalf("want a plain claude turn to keep its own wording for those words, got %v", err)
	}
	r.launched.remember("remembered", "glm-5.1:cloud")
	remembered := r.Parse("remembered", make(chan assistant.Event, 1))
	if err := remembered.Diagnose(errors.New("exit status 1"), "Error: error running upgrade: could not open a new TTY\n"); err == nil || err.Error() != "ollama/glm-5.1:cloud needs a paid Ollama plan, pick another model at the ring." {
		t.Fatalf("want the plan sentence naming a remembered session's model, got %v", err)
	}
	if err := p.Diagnose(errors.New("exit status 1"), "something else went wrong\n"); err != nil {
		t.Fatalf("want other stderr left to the generic wording, got %v", err)
	}
}

func TestTheRingWindowIsAskedForThePickBeforeTheReportedName(t *testing.T) {
	usage := func(picked string) (*assistant.ContextUsage, []string) {
		var asked []string
		r := &runner{sessions: stubSessions{}}
		events := make(chan assistant.Event, 8)
		p := r.Parse(sessionID, events).(*claudeParser)
		p.picked = picked
		p.window = func(model string) int {
			asked = append(asked, model)
			if model == "ollama/nemotron-3-ultra:cloud" {
				return 262144
			}
			return 0
		}
		lines := []string{
			`{"type":"assistant","message":{"model":"nemotron-3-ultra","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":25000,"cache_read_input_tokens":443}}}`,
			`{"type":"result","subtype":"success","is_error":false,"result":"hi","session_id":"` + sessionID + `","modelUsage":{"nemotron-3-ultra":{"contextWindow":0}}}`,
		}
		for _, line := range lines {
			if err := p.Line([]byte(line)); err != nil {
				t.Fatal(err)
			}
		}
		close(events)
		for ev := range events {
			if ev.Kind == assistant.EventUsage {
				return ev.Usage, asked
			}
		}
		t.Fatal("no usage reading")
		return nil, nil
	}
	got, asked := usage("ollama/nemotron-3-ultra:cloud")
	if got.Window != 262144 || got.Tokens != 25443 || got.Model != "nemotron-3-ultra" {
		t.Fatalf("want the pick's window under the reported name, got %+v", got)
	}
	if len(asked) != 1 || asked[0] != "ollama/nemotron-3-ultra:cloud" {
		t.Fatalf("want the pick asked first and the reported name not at all, got %q", asked)
	}
	if got, asked = usage(""); got.Window != 0 || len(asked) != 1 || asked[0] != "nemotron-3-ultra" {
		t.Fatalf("want a turn without a pick to ask for the reported name alone, got %+v after %q", got, asked)
	}
}

func TestTheRingWindowOfAnOllamaPickComesFromTheLauncher(t *testing.T) {
	windows := map[string]int{"nemotron-3-ultra:cloud": 262144}
	r := &runner{sessions: stubSessions{}}
	events := make(chan assistant.Event, 8)
	p := r.Parse(sessionID, events).(*claudeParser)
	p.window = func(model string) int { return windows[model] }
	lines := []string{
		`{"type":"assistant","message":{"model":"nemotron-3-ultra:cloud","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1000,"cache_read_input_tokens":500}}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"hi","session_id":"` + sessionID + `","modelUsage":{"nemotron-3-ultra:cloud":{"contextWindow":0}}}`,
	}
	for _, line := range lines {
		if err := p.Line([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	close(events)
	for ev := range events {
		if ev.Kind == assistant.EventUsage {
			if ev.Usage.Window != 262144 || ev.Usage.Tokens != 1500 || ev.Usage.Model != "nemotron-3-ultra:cloud" {
				t.Fatalf("want the launcher's window on the reading, got %+v", ev.Usage)
			}
			return
		}
	}
	t.Fatal("no usage reading")
}

func TestThePrefixRuleTheLaunchShapeAndTheWindowBehindThePrefix(t *testing.T) {
	if name, ok := ollamaPick(" ollama/qwen3.5:cloud "); !ok || name != "qwen3.5:cloud" {
		t.Fatalf("want the name behind the prefix, got %q %v", name, ok)
	}
	for _, raw := range []string{"haiku", "ollama/", "", "Ollama/x"} {
		if _, ok := ollamaPick(raw); ok {
			t.Fatalf("%q is no Ollama pick", raw)
		}
	}
	if got := strings.Join(ollamaLaunch("qwen3.5:cloud"), " "); got != "launch claude --model qwen3.5:cloud --yes --" {
		t.Fatalf("want the launcher's argv shape, got %s", got)
	}
	stateDir := t.TempDir()
	ollamatest.Cache(stateDir, []string{"nemotron-3-ultra:cloud"}, nil, map[string]int{"nemotron-3-ultra": 262144}, true)
	r := &runner{sessions: stubSessions{}, launcher: ollama.New(nil, "", stateDir)}
	p := r.Parse(sessionID, make(chan assistant.Event, 1)).(*claudeParser)
	for _, model := range []string{"ollama/nemotron-3-ultra:cloud", "ollama/nemotron-3-ultra", "nemotron-3-ultra:cloud"} {
		if got := p.window(model); got != 262144 {
			t.Fatalf("window(%q) = %d, want the snapshot's window with or without the prefix", model, got)
		}
	}
	if got := (&runner{sessions: stubSessions{}}).Parse(sessionID, make(chan assistant.Event, 1)).(*claudeParser).window("ollama/x"); got != 0 {
		t.Fatalf("want no window without a launcher, got %d", got)
	}
}

func TestALauncherTurnWithoutOllamasSignInSaysSo(t *testing.T) {
	withOllamaOnPath(t)
	r := &runner{sessions: stubSessions{}, launched: newOllamaSessions(t.TempDir())}
	if _, err := r.Command(assistant.TurnRequest{SessionID: sessionID, Workdir: "/work", Prompt: "hi", Model: "ollama/glm-5.2:cloud"}); err != nil {
		t.Fatal(err)
	}
	prompt := "You need to be signed in to Ollama to run Cloud models.\n\nIf your browser did not open, navigate to:\n    https://ollama.com/connect?name=eax&key=c3NoLWVkMjU1MTkgQUFBQUMzTnphQzFsWkRJMU5URTVBQUFBSUV4YW1wbGU\n\n"
	refused := "Error: glm-5.2:cloud requires sign in\n"
	signin := "Ollama is not signed in on this machine. Run ollama signin in a terminal and send this again."
	p := r.Parse(sessionID, make(chan assistant.Event, 1))
	for label, stderr := range map[string]string{"refusal": refused, "sign in prompt": prompt, "both": prompt + refused} {
		if err := p.Diagnose(errors.New("exit status 1"), stderr); err == nil || err.Error() != signin {
			t.Fatalf("%s: want the Ollama sign in sentence, got %v", label, err)
		}
	}
	plain := r.Parse("plain-session", make(chan assistant.Event, 1))
	err := plain.Diagnose(errors.New("exit status 1"), "Error: Not logged in. Please run /login.")
	if err == nil || !strings.HasPrefix(err.Error(), "The coder is not logged in on this machine.") {
		t.Fatalf("want a plain claude turn without its login to keep the coder's sentence, got %v", err)
	}
}

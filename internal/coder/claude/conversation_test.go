package claude

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// The fixtures are lines as claude 2.1 records them, taken from real
// sessions on this host, shortened and anonymised. The keys around the
// message are the record's own (parentUuid, promptId, cwd, version...), so a
// reading that stumbles over one of them stumbles here. A turn is a run of
// assistant entries, one per text or tool call, every tool result comes back
// as a user entry that carries nothing but the result, and a lot of what the
// CLI writes in the user's role was typed by nobody.
const (
	askLine     = `{"parentUuid":null,"isSidechain":false,"promptId":"p1","type":"user","message":{"role":"user","content":[{"type":"text","text":"list the files"}]},"uuid":"u-ask","timestamp":"2026-09-18T10:50:56.891Z","permissionMode":"default","origin":{"kind":"human"},"promptSource":"typed","userType":"external","entrypoint":"cli","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280","gitBranch":"master"}`
	bashLine    = `{"parentUuid":"u-ask","isSidechain":false,"message":{"model":"claude-fable-5-1","id":"msg_01","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls /tmp\necho done","description":"List files"},"caller":{"type":"direct"}}],"stop_reason":null},"requestId":"req_01","type":"assistant","uuid":"a-bash","timestamp":"2026-09-18T10:50:59.382Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	bashDone    = `{"parentUuid":"a-bash","isSidechain":false,"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01","type":"tool_result","content":"caps\nhooks","is_error":false}]},"uuid":"u-bashdone","timestamp":"2026-09-18T10:51:13.915Z","toolUseResult":{"stdout":"caps\nhooks","stderr":"","interrupted":false,"isImage":false},"sourceToolAssistantUUID":"a-bash","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	thinkLine   = `{"parentUuid":"u-bashdone","isSidechain":false,"message":{"model":"claude-fable-5-1","id":"msg_02","type":"message","role":"assistant","content":[{"type":"thinking","thinking":"Two files, nothing more to do.","signature":"CAQSjwoKEA"}]},"type":"assistant","uuid":"a-think","timestamp":"2026-09-18T10:51:16.356Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	replyLine   = `{"parentUuid":"a-think","isSidechain":false,"message":{"model":"claude-fable-5-1","id":"msg_02","type":"message","role":"assistant","content":[{"type":"text","text":"Two files:\n\n- caps\n- hooks"}]},"type":"assistant","uuid":"a-reply","timestamp":"2026-09-18T10:51:16.363Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	colorLine   = `{"parentUuid":"a-reply","isSidechain":false,"promptId":"p2","type":"user","message":{"role":"user","content":[{"type":"text","text":"pick a color"}]},"uuid":"u-color","timestamp":"2026-09-18T10:51:23.882Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	askUseLine  = `{"parentUuid":"u-color","isSidechain":false,"message":{"model":"claude-fable-5-1","id":"msg_03","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_02","name":"AskUserQuestion","input":{"questions":[{"question":"Which color do you like?","header":"Color","multiSelect":false,"options":[{"label":"Red","description":"a warm one"},{"label":"Blue","description":"a cool one"}]}]}}]},"type":"assistant","uuid":"a-askuse","timestamp":"2026-09-18T10:51:26.210Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	askDone     = `{"parentUuid":"a-askuse","isSidechain":false,"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_02","type":"tool_result","content":"Your questions have been answered: \"Which color do you like?\"=\"Red\". You can now continue with these answers in mind."}]},"uuid":"u-askdone","timestamp":"2026-09-18T10:51:26.390Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	redLine     = `{"parentUuid":"u-askdone","isSidechain":false,"message":{"model":"claude-fable-5-1","id":"msg_04","type":"message","role":"assistant","content":[{"type":"text","text":"Red."}]},"type":"assistant","uuid":"a-red","timestamp":"2026-09-18T10:51:27.200Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	fetchFailed = `{"parentUuid":"a-fetch","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"Claude Code is unable to fetch from example.org\nThe host refused the connection.","is_error":true,"tool_use_id":"t-fetch"}]},"uuid":"u-fetchfail","timestamp":"2026-09-18T11:03:11.000Z","toolUseResult":"Error: Claude Code is unable to fetch from example.org","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	abortLine   = `{"parentUuid":"a-bash","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]},"uuid":"u-abort","timestamp":"2026-09-18T10:57:47.735Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	noteLine    = `{"parentUuid":"u-abort","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nThe session was renamed to \"caps and hooks\".\n</system-reminder>"}]},"isMeta":true,"uuid":"u-note","timestamp":"2026-09-18T10:57:48.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	sideLine    = `{"parentUuid":null,"isSidechain":true,"agentId":"a9c283bee86ec3b21","message":{"role":"assistant","content":[{"type":"text","text":"a subagent speaking"}]},"type":"assistant","uuid":"a-side","timestamp":"2026-09-18T10:51:00.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`

	// What nobody typed, in the user's role.
	reminderLine   = `{"parentUuid":"a-reply","isSidechain":false,"promptId":"p3","type":"user","message":{"role":"user","content":[{"type":"text","text":"Since your last answer the file moved.\n\n<system-reminder>\nThe file /home/me/proj/a.go was modified by the user, either by the user themselves or by a linter.\n</system-reminder>"},{"type":"text","text":"<system-reminder>\nAs you answer the user's questions, you can use the following context:\n# gitStatus\nCurrent branch: master\n</system-reminder>"}]},"uuid":"u-reminder","timestamp":"2026-09-18T11:00:00.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	ideLine        = `{"parentUuid":"u-reminder","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<ide_selection>The user selected the lines 3 to 5 from /home/me/proj/a.go:\nfunc main() {\n}\n\nThis may or may not be related to the current task.</ide_selection>\nwhat does this do"}]},"uuid":"u-ide","timestamp":"2026-09-18T11:00:10.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	imageLine      = `{"parentUuid":"u-ide","isSidechain":false,"promptId":"p4","type":"user","message":{"role":"user","content":[{"type":"text","text":"why does [Image #1] look cut off"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAHw"}}]},"uuid":"u-image","timestamp":"2026-09-18T11:00:20.000Z","imagePasteIds":[1],"cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	imageMetaLine  = `{"parentUuid":"u-image","isSidechain":false,"promptId":"p4","type":"user","message":{"role":"user","content":"[Image: original 2152x104, displayed at 2000x97. Multiply coordinates by 1.08 to map to original image.]"},"isMeta":true,"turnCompanion":true,"uuid":"u-imagemeta","timestamp":"2026-09-18T11:00:20.100Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	pastedLine     = `{"parentUuid":"u-imagemeta","isSidechain":false,"promptId":"p5","type":"user","message":{"role":"user","content":"\n\n<pasted_content id=\"60dc\">\nGood work, one thing stands between this and done.\n</pasted_content>\n\nfix it"},"uuid":"u-pasted","timestamp":"2026-09-18T11:00:30.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	caveatLine     = `{"parentUuid":"u-pasted","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<local-command-caveat>Caveat: The messages below were generated by the user while running local commands. DO NOT respond to these messages or otherwise consider them in your response unless the user explicitly asks you to.</local-command-caveat>"}]},"isMeta":true,"uuid":"u-caveat","timestamp":"2026-09-18T11:01:00.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	bareCaveatLine = `{"parentUuid":"u-pasted","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<local-command-caveat>Caveat: The messages below were generated by the user while running local commands. DO NOT respond to these messages or otherwise consider them in your response unless the user explicitly asks you to.</local-command-caveat>"}]},"uuid":"u-barecaveat","timestamp":"2026-09-18T11:01:00.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	commandLine1   = `{"parentUuid":"u-caveat","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<command-name>/compact</command-name>\n            <command-message>compact</command-message>\n            <command-args>keep the file list</command-args>"}]},"uuid":"u-command","timestamp":"2026-09-18T11:01:00.100Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	stdoutLine     = `{"parentUuid":"u-command","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<local-command-stdout>Compacted </local-command-stdout>"}]},"uuid":"u-stdout","timestamp":"2026-09-18T11:01:01.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	summaryLine    = `{"parentUuid":"u-stdout","isSidechain":false,"promptId":"p6","type":"user","message":{"role":"user","content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\nSummary:\n1. Primary Request and Intent:\n   The user asked one question."},"isCompactSummary":true,"uuid":"u-summary","timestamp":"2026-09-18T11:01:02.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	continueLine   = `{"parentUuid":"u-summary","isSidechain":false,"promptId":"p6","type":"user","message":{"role":"user","content":[{"type":"text","text":"Continue from where you left off."}]},"isMeta":true,"uuid":"u-continue","timestamp":"2026-09-18T11:01:02.100Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	bashInputLine  = `{"parentUuid":"u-continue","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<bash-input>ls -la</bash-input>"}]},"uuid":"u-bashin","timestamp":"2026-09-18T11:02:00.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	bashOutLine    = `{"parentUuid":"u-bashin","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<bash-stdout>total 8\ndrwxr-xr-x 2 me me 4096 caps</bash-stdout><bash-stderr></bash-stderr>"}]},"uuid":"u-bashout","timestamp":"2026-09-18T11:02:01.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	taskNoteLine   = `{"parentUuid":"u-bashout","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<task-notification>\n<task-id>b11c5vmr5</task-id>\n<summary>Monitor event: \"the log moved\"</summary>\n<event>2026/09/16 09:46:18 handled request</event>\n</task-notification>"}]},"uuid":"u-tasknote","timestamp":"2026-09-18T11:02:02.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	toolsLine      = `{"parentUuid":"u-tasknote","isSidechain":false,"message":{"model":"claude-fable-5-1","id":"msg_05","type":"message","role":"assistant","content":[{"type":"tool_use","id":"t-read","name":"Read","input":{"file_path":"/home/me/proj/a.go"}},{"type":"tool_use","id":"t-edit","name":"Edit","input":{"replace_all":false,"file_path":"/home/me/proj/b.go","old_string":"a","new_string":"b"}},{"type":"tool_use","id":"t-write","name":"Write","input":{"file_path":"/home/me/proj/c.go","content":"package main\n"}},{"type":"tool_use","id":"t-grep","name":"Grep","input":{"pattern":"func main","path":"/home/me/proj","output_mode":"content"}},{"type":"tool_use","id":"t-glob","name":"Glob","input":{"pattern":"**/*.go"}},{"type":"tool_use","id":"t-agent","name":"Agent","input":{"description":"Analyze compose actions","subagent_type":"Explore","prompt":"Read-only analysis in the Go project"}},{"type":"tool_use","id":"t-todo","name":"TodoWrite","input":{"todos":[{"content":"one","status":"pending","activeForm":"doing one"},{"content":"two","status":"pending","activeForm":"doing two"},{"content":"three","status":"pending","activeForm":"doing three"}]}},{"type":"tool_use","id":"t-monitor","name":"Monitor","input":{"cmd":"tail -f x.log","description":"watch"}}]},"type":"assistant","uuid":"a-tools","timestamp":"2026-09-18T11:03:00.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	fetchLine      = `{"parentUuid":"a-tools","isSidechain":false,"message":{"model":"claude-fable-5-1","id":"msg_06","type":"message","role":"assistant","content":[{"type":"tool_use","id":"t-fetch","name":"WebFetch","input":{"url":"https://example.org/manual.pdf","prompt":"Summarize the install steps"}}]},"type":"assistant","uuid":"a-fetch","timestamp":"2026-09-18T11:03:10.000Z","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	listResultLine = `{"parentUuid":"a-tools","isSidechain":false,"type":"user","message":{"role":"user","content":[{"tool_use_id":"t-read","type":"tool_result","content":[{"type":"text","text":"     1\tpackage main\n     2\t"}]}]},"uuid":"u-readdone","timestamp":"2026-09-18T11:03:01.000Z","toolUseResult":{"type":"text","file":{"filePath":"/home/me/proj/a.go","content":"package main\n"}},"cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
	bookkeepingLn  = `{"type":"last-prompt","lastPrompt":"a draft nobody sent","sessionId":"s1"}`
	hookLine       = `{"parentUuid":"a-reply","isSidechain":false,"type":"system","subtype":"stop_hook_summary","hookCount":1,"hookInfos":[{"command":"cat > inbox","durationMs":27}],"level":"suggestion","timestamp":"2026-09-18T10:51:17.000Z","uuid":"sys-hook","cwd":"/home/me/proj","sessionId":"s1","version":"2.1.280"}`
)

func build(lines ...string) []coder.Message {
	var b conversationBuilder
	for _, line := range lines {
		b.Feed([]byte(line))
	}
	return b.Messages()
}

func joined(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestATurnIsOneMessageWithItsToolCallsInPlace(t *testing.T) {
	all := build(askLine, bashLine, bashDone, thinkLine, replyLine)
	if len(all) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(all), all)
	}
	user, turn := all[0], all[1]
	if user.Role != coder.RoleUser || user.ID != "u-ask" || user.Text() != "list the files" || user.Time.IsZero() {
		t.Fatalf("user message: %+v", user)
	}
	if turn.Role != coder.RoleCoder || turn.ID != "a-bash" || len(turn.Parts) != 2 {
		t.Fatalf("coder turn: %+v", turn)
	}
	call := turn.Parts[0]
	if call.Kind != coder.PartTool || call.Tool != "Bash" || call.Text != "ls /tmp" {
		t.Fatalf("the tool call keeps its name and the first line of its command: %+v", call)
	}
	if turn.Parts[1].Kind != coder.PartText || !strings.Contains(turn.Parts[1].Text, "- caps") {
		t.Fatalf("the answer keeps its markdown: %+v", turn.Parts[1])
	}
	if !turn.Time.Equal(mustTime(t, "2026-09-18T10:50:59.382Z")) {
		t.Fatalf("a turn is dated by its first entry: %v", turn.Time)
	}
	for _, m := range all {
		for _, p := range m.Parts {
			if strings.Contains(p.Text, "hooks") && p.Kind == coder.PartTool {
				t.Fatalf("a tool result leaked into a part: %+v", p)
			}
			if strings.Contains(p.Text, "nothing more to do") {
				t.Fatalf("thinking leaked into a part: %+v", p)
			}
		}
	}
}

// A question the coder asked is one line of its turn, the question itself;
// the result that carries the answer is no message.
func TestAQuestionIsOneLine(t *testing.T) {
	all := build(colorLine, askUseLine, askDone, redLine)
	if len(all) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(all), all)
	}
	turn := all[1]
	if len(turn.Parts) != 2 || turn.Parts[0].Tool != "AskUserQuestion" || turn.Parts[0].Text != "Which color do you like?" || turn.Parts[1].Text != "Red." {
		t.Fatalf("the question turn: %+v", turn)
	}
}

// What the CLI writes in the user's role is not a message: the sidechain,
// the interrupt marker, the reminder, the bookkeeping entries and the hook
// summary leave nothing behind.
func TestNotesAndBookkeepingLeaveNoMessage(t *testing.T) {
	all := build(askLine, bashLine, abortLine, noteLine, sideLine, bookkeepingLn, hookLine)
	if len(all) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(all), all)
	}
}

// The user bubble carries what the person typed and nothing else, whatever
// the CLI put around it. claude marks its caveat isMeta, which drops it before
// any stripping, so bareCaveatLine carries the same text without the mark and
// pins the strip of the caveat block itself.
func TestAUserMessageIsOnlyWhatThePersonTyped(t *testing.T) {
	all := build(reminderLine, ideLine, imageLine, imageMetaLine, pastedLine, caveatLine, bareCaveatLine, commandLine1, stdoutLine, summaryLine, continueLine, bashInputLine, bashOutLine, taskNoteLine)
	want := []struct {
		role, kind, text string
	}{
		{coder.RoleUser, coder.KindWords, "Since your last answer the file moved."},
		{coder.RoleUser, coder.KindWords, "what does this do"},
		{coder.RoleUser, coder.KindWords, "why does look cut off"},
		{coder.RoleUser, coder.KindWords, "Good work, one thing stands between this and done.\n\nfix it"},
		{coder.RoleUser, coder.KindCommand, "/compact keep the file list"},
		{coder.RoleUser, coder.KindCommand, "! ls -la"},
	}
	if len(all) != len(want) {
		var got []string
		for _, m := range all {
			got = append(got, m.Role+"/"+m.Kind+": "+m.Text())
		}
		t.Fatalf("got %d messages, want %d:\n%s", len(all), len(want), strings.Join(got, "\n"))
	}
	for i, w := range want {
		m := all[i]
		if m.Role != w.role || m.Kind != w.kind || m.Text() != w.text {
			t.Fatalf("message %d: got %s/%s %q, want %s/%s %q", i, m.Role, m.Kind, m.Text(), w.role, w.kind, w.text)
		}
	}
	for _, m := range all {
		for _, tag := range []string{"system-reminder", "ide_selection", "pasted_content", "[Image", "Caveat", "command-name", "task-notification", "bash-stdout", "local-command"} {
			if strings.Contains(m.Text(), tag) {
				t.Fatalf("%q stands in a message: %q", tag, m.Text())
			}
		}
	}
}

// A reminder that is not closed is stripped to the end, and a text of
// nothing but reminders is no message.
func TestAnUnclosedReminderIsStrippedToTheEnd(t *testing.T) {
	if got := cleanUserText("do this\n<system-reminder>\nnever closed"); got != "do this" {
		t.Fatalf("cleanUserText: %q", got)
	}
	if got := cleanUserText("<system-reminder>a</system-reminder>\n\n<system-reminder>b</system-reminder>"); got != "" {
		t.Fatalf("a text of reminders alone: %q", got)
	}
	if got := cleanUserText("<system-reminder>\n<file>x</file>\n</system-reminder>\nkeep"); got != "keep" {
		t.Fatalf("a tag inside the reminder ended it early: %q", got)
	}
}

func TestTheToolLineSaysWhatACallIsAbout(t *testing.T) {
	all := build(askLine, toolsLine, fetchLine)
	turn := all[1]
	want := map[string]string{
		"Read":      "/home/me/proj/a.go",
		"Edit":      "/home/me/proj/b.go",
		"Write":     "/home/me/proj/c.go",
		"Grep":      "func main",
		"Glob":      "**/*.go",
		"Agent":     "Analyze compose actions",
		"TodoWrite": "3 items",
		"Monitor":   "",
		"WebFetch":  "https://example.org/manual.pdf",
	}
	if len(turn.Parts) != len(want) {
		t.Fatalf("got %d parts, want %d: %+v", len(turn.Parts), len(want), turn.Parts)
	}
	for _, p := range turn.Parts {
		if p.Kind != coder.PartTool {
			t.Fatalf("not a tool part: %+v", p)
		}
		if p.Text != want[p.Tool] {
			t.Fatalf("%s: got %q, want %q", p.Tool, p.Text, want[p.Tool])
		}
	}
	long := strings.Repeat("x", 300)
	if got := coder.ToolLine("Bash", []byte(`{"command":"`+long+`"}`)); len([]rune(got)) != coder.ToolLineRunes+1 {
		t.Fatalf("a long line is cut: %d runes", len([]rune(got)))
	}
	if got := coder.ToolLine("TodoWrite", []byte(`{"todos":[{"content":"x"}]}`)); got != "1 item" {
		t.Fatalf("one todo: %q", got)
	}
}

// A result never becomes a part, a failed one no more than a plain one, and
// the turn goes on across it.
func TestAResultLeavesNothing(t *testing.T) {
	all := build(askLine, toolsLine, listResultLine, fetchLine, fetchFailed)
	if len(all) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(all), all)
	}
	for _, p := range all[1].Parts {
		if p.Kind != coder.PartTool || strings.Contains(p.Text, "package main") || strings.Contains(p.Text, "unable to fetch") {
			t.Fatalf("a result became a part: %+v", p)
		}
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("time %q: %v", value, err)
	}
	return at
}

// The reading goes through the record file from the top on every call.
func TestTheSessionIsReadFromItsRecord(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "-home-me-proj", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Coder{sessions: &sessionRepository{stateRoot: root}}
	if err := os.WriteFile(path, []byte(joined(askLine, bashLine, bashDone, thinkLine, replyLine)), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := c.SessionConversation("s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(all) != 2 || all[1].ID != "a-bash" || len(all[1].Parts) != 2 {
		t.Fatalf("messages: %+v", all)
	}
}

// A session claude has not written a transcript for yet reads as
// fs.ErrNotExist, which the copy view shows as an empty conversation.
func TestASessionWithoutATranscriptIsNotExist(t *testing.T) {
	c := &Coder{sessions: &sessionRepository{stateRoot: t.TempDir()}}
	if _, err := c.SessionConversation("s1"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

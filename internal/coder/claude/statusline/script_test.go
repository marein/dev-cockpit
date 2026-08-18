package statusline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// payload is what claude writes to the script's stdin, cut down to the fields
// the values here read.
const payload = `{
  "model": {"display_name": "Opus 5 (1M context)"},
  "workspace": {"current_dir": "/root/projects/dev-cockpit"},
  "cwd": "/root/projects/dev-cockpit",
  "transcript_path": "%TRANSCRIPT%",
  "cost": {"total_cost_usd": 1.2449},
  "context_window": {"used_percentage": 42.5},
  "rate_limits": {"five_hour": {"used_percentage": 16.0}}
}`

// run writes the script for the entries and runs it against the payload, with
// a home of its own so the usage call finds no credentials and asks nothing.
func run(t *testing.T, entries []Entry, stdin string) string {
	t.Helper()
	requireTool(t, "bash")
	dir := t.TempDir()
	if err := Apply(dir, entries); err != nil {
		t.Fatalf("write the script: %v", err)
	}
	cmd := exec.Command("bash", ScriptPath(dir))
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run the script: %v (stderr %q)", err, stderr.String())
	}
	// Nothing in the script is allowed to say anything anywhere but on the
	// line: claude reads stdout, and a person reads whatever lands beside it.
	if stderr.Len() > 0 {
		t.Fatalf("the script wrote to stderr: %q", stderr.String())
	}
	return string(out)
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not on this machine", name)
	}
}

func TestScriptRendersTheDefaultLine(t *testing.T) {
	requireTool(t, "jq")
	out := run(t, Defaults(), strings.ReplaceAll(payload, "%TRANSCRIPT%", ""))
	// The weekly values need the usage API, which answers nothing here, so
	// those entries and the separators leading to them fall away and the line
	// neither ends in a separator nor carries two in a row.
	want := "\x1b[36mOpus 5\x1b[0m \x1b[2m·\x1b[0m \x1b[2mc\x1b[0m \x1b[32m42.5%\x1b[0m \x1b[2m·\x1b[0m \x1b[2m5\x1b[0m \x1b[32m16%\x1b[0m\n"
	if out != want {
		t.Fatalf("the default line is\n%q\nwant\n%q", out, want)
	}
}

func TestScriptColorsANumberByItsBounds(t *testing.T) {
	requireTool(t, "jq")
	entry := func(value string) []Entry {
		return Normalize([]Entry{{Kind: KindValue, Value: value, Thresholds: []Threshold{
			{At: 0, Color: "green"}, {At: 50, Color: "yellow"}, {At: 80, Color: "red"},
		}}})
	}
	cases := []struct {
		used string
		want string
	}{
		{used: "12", want: "\x1b[32m12%\x1b[0m\n"},
		{used: "50", want: "\x1b[33m50%\x1b[0m\n"},
		{used: "49.99", want: "\x1b[32m49.99%\x1b[0m\n"},
		{used: "80.01", want: "\x1b[31m80.01%\x1b[0m\n"},
	}
	for _, c := range cases {
		body := `{"context_window": {"used_percentage": ` + c.used + `}}`
		if out := run(t, entry("context"), body); out != c.want {
			t.Errorf("%s%% renders %q, want %q", c.used, out, c.want)
		}
	}
}

func TestScriptDropsAnEntryNobodyAnswered(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "cost", Thresholds: []Threshold{{At: 0, Color: "green"}}},
	})
	out := run(t, entries, `{"model": {"display_name": "Opus 5"}}`)
	want := "\x1b[36mOpus 5\x1b[0m\n"
	if out != want {
		t.Fatalf("a line whose second value is missing is %q, want %q", out, want)
	}
}

func TestScriptWritesOneLinePerBreak(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "default"},
		{Kind: KindBreak},
		{Kind: KindValue, Value: "dir", Color: "default"},
	})
	out := run(t, entries, `{"model": {"display_name": "Opus 5"}, "cwd": "/root/projects/dev-cockpit"}`)
	if out != "Opus 5\ndev-cockpit\n" {
		t.Fatalf("the two lines are %q", out)
	}
}

func TestScriptSurvivesWithoutJQ(t *testing.T) {
	requireTool(t, "bash")
	dir := t.TempDir()
	if err := Apply(dir, Defaults()); err != nil {
		t.Fatalf("write the script: %v", err)
	}
	cmd := exec.Command("bash", ScriptPath(dir))
	cmd.Stdin = strings.NewReader(strings.ReplaceAll(payload, "%TRANSCRIPT%", ""))
	// Nothing at all on the PATH: no jq, no git, no curl, no date, and no cat
	// either, which is why the payload is read with the shell's own read.
	cmd.Env = []string{"PATH=" + filepath.Join(dir, "nothing"), "HOME=" + t.TempDir()}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the script failed without jq: %v", err)
	}
	if string(out) != "\n" {
		t.Fatalf("without jq the script wrote %q, want the empty line alone", out)
	}
	if stderr.Len() > 0 {
		t.Fatalf("without the tools the script wrote %q to stderr", stderr.String())
	}
}

// The state directory is the script's own, and it can be gone: a restored
// backup, a directory somebody moved, a read only filesystem. A redirection
// that fails reports itself on the stderr in effect **when it runs**, so a
// trailing 2>/dev/null on the same command is too late and bash names the file
// out loud. Every write the script makes therefore carries the silence around
// it, and the line still stands.
func TestScriptSaysNothingWithoutItsStateDirectory(t *testing.T) {
	requireTool(t, "jq")
	dir := t.TempDir()
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindValue, Value: "week_top", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindValue, Value: "cost_turn", Thresholds: []Threshold{{At: 0, Color: "green"}}},
	})
	if err := Apply(dir, entries); err != nil {
		t.Fatalf("write the script: %v", err)
	}
	// The script keeps its own path to a state directory that is not there and
	// cannot be made either, which is what covers the write of the last cost
	// reading too: mkdir -p would create a plain missing directory, and the
	// redirection behind it would never be the thing that fails.
	blocked := filepath.Join(dir, "afile")
	if err := os.WriteFile(blocked, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatalf("write the blocking file: %v", err)
	}
	gone := filepath.Join(blocked, "state")
	script := filepath.Join(dir, "loose.sh")
	if err := os.WriteFile(script, []byte(Script(gone, entries)), 0o700); err != nil {
		t.Fatalf("write the loose script: %v", err)
	}
	cmd := exec.Command("bash", script)
	cmd.Stdin = strings.NewReader(`{"session_id": "abc", "model": {"display_name": "Opus 5"}, "cost": {"total_cost_usd": 1}}`)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the script failed without its state directory: %v (stderr %q)", err, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("a missing state directory put %q on stderr", stderr.String())
	}
	if want := "\x1b[36mOpus 5\x1b[0m\n"; string(out) != want {
		t.Fatalf("the line is %q, want %q", out, want)
	}
}

// A value the shell answers has to answer something when it is asked the way
// the script asks it, which is out of a child process with no terminal and
// nothing of an interactive shell around it.
func TestEveryShellValueAnswersSomething(t *testing.T) {
	requireTool(t, "bash")
	for _, value := range Values {
		if value.source != fromShell {
			continue
		}
		cmd := exec.Command("bash", "-c", "printf '%s' \"$("+value.shell+")\"")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		out, err := cmd.Output()
		if err != nil {
			t.Errorf("%s: %v", value.ID, err)
			continue
		}
		if strings.TrimSpace(string(out)) == "" {
			t.Errorf("%s runs %q and answers nothing, so its entry can never stand in a line", value.ID, value.shell)
		}
	}
}

func TestScriptTakesALabelAsItStands(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{{Kind: KindValue, Value: "model", Label: `it's $(touch pwned) "`, LabelColor: "dim", Color: "cyan"}})
	out := run(t, entries, `{"model": {"display_name": "Opus 5"}}`)
	want := "\x1b[2mit's $(touch pwned) \"\x1b[0m \x1b[36mOpus 5\x1b[0m\n"
	if out != want {
		t.Fatalf("the label renders %q, want %q", out, want)
	}
}

// These tokens are the payload's, the last request's four counts and the two
// sums over them, so this one reads no transcript.
func TestScriptReadsTheTokensOutOfThePayload(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "tokens_cache_read", Label: "r", LabelColor: "dim"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_tokens", Label: "t", LabelColor: "dim"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "cache_hit", Label: "hit", LabelColor: "dim"},
	})
	// The shape of a real payload, measured: the four counts of the last
	// request under current_usage, the two totals beside them.
	body := `{"context_window": {"total_input_tokens": 41531, "total_output_tokens": 217, "current_usage": {"input_tokens": 2, "output_tokens": 217, "cache_read_input_tokens": 22124, "cache_creation_input_tokens": 19405}}}`
	out := run(t, entries, body)
	// 41.5k in the window, the input side alone, and 22124 of those 41531
	// tokens came out of the cache.
	want := "\x1b[2mr\x1b[0m 22.1k \x1b[2m·\x1b[0m \x1b[2mt\x1b[0m 41.5k \x1b[2m·\x1b[0m \x1b[2mhit\x1b[0m 53.27%\n"
	if out != want {
		t.Fatalf("the token line is %q, want %q", out, want)
	}
}

// The window is the input side alone. Measured against claude 2.1.234: a
// payload carrying 32810 input and 1667 output tokens in a 200000 window says
// used_percentage 16, which is 32810 of 200000 and not the 17.2% the two added
// together would be, and total_input_tokens is exactly the three input counts
// of current_usage added up. The four context entries therefore have to agree
// with each other on one line.
func TestScriptCountsTheWindowTheWayClaudeCountsIt(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "context", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_tokens"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_left"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_size"},
	})
	body := `{"context_window": {"total_input_tokens": 32810, "total_output_tokens": 1667, "context_window_size": 200000, "used_percentage": 16,
	  "current_usage": {"input_tokens": 10, "output_tokens": 1667, "cache_creation_input_tokens": 8398, "cache_read_input_tokens": 24402}}}`
	out := run(t, entries, body)
	want := "\x1b[32m16%\x1b[0m \x1b[2m·\x1b[0m 32.8k \x1b[2m·\x1b[0m 167.2k \x1b[2m·\x1b[0m 200k\n"
	if out != want {
		t.Fatalf("the context line is %q, want %q", out, want)
	}
	// A window nobody has put anything in yet is a window whose free room is
	// unknown, not one that is empty: the size alone answers nothing.
	left := Normalize([]Entry{{Kind: KindValue, Value: "context_left"}})
	if bare := run(t, left, `{"context_window": {"context_window_size": 200000}}`); bare != "\n" {
		t.Fatalf("without a token count the room left is %q, want nothing", bare)
	}
}

// A length of time is one unit of time on the line, and its bound is compared
// against minutes.
func TestScriptRendersLengthsOfTime(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{{Kind: KindValue, Value: "duration", Thresholds: []Threshold{
		{At: 0, Color: "green"}, {At: 60, Color: "red"},
	}}})
	cases := []struct {
		ms   string
		want string
	}{
		{ms: "45000", want: "\x1b[32m45s\x1b[0m\n"},
		{ms: "900000", want: "\x1b[32m15m\x1b[0m\n"},
		{ms: "3600000", want: "\x1b[31m1h\x1b[0m\n"},
		{ms: "180000000", want: "\x1b[31m2d\x1b[0m\n"},
	}
	for _, c := range cases {
		out := run(t, entries, `{"cost": {"total_duration_ms": `+c.ms+`}}`)
		if out != c.want {
			t.Errorf("%sms renders %q, want %q", c.ms, out, c.want)
		}
	}
}

// The repository is asked through git itself, so this one builds a real one.
func TestScriptReadsTheRepository(t *testing.T) {
	requireTool(t, "jq")
	requireTool(t, "git")
	work := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", work, "-c", "user.name=e2e", "-c", "user.email=e2e@example.com"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "--initial-branch=work")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatalf("write a file: %v", err)
	}
	git("add", "a.txt")
	git("commit", "-m", "first")
	if err := os.WriteFile(filepath.Join(work, "b.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatalf("write a second file: %v", err)
	}
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "branch", Color: "magenta"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "git_changes", Label: "±", LabelColor: "dim"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "git_age", Label: "age", LabelColor: "dim"},
	})
	out := run(t, entries, `{"workspace": {"current_dir": "`+work+`"}}`)
	want := "\x1b[35mwork\x1b[0m \x1b[2m·\x1b[0m \x1b[2m±\x1b[0m 1 \x1b[2m·\x1b[0m \x1b[2mage\x1b[0m 0s\n"
	if out != want {
		t.Fatalf("the git line is %q, want %q", out, want)
	}
	// Outside a repository every one of them falls away, and with them the
	// separators that would lead nowhere.
	if bare := run(t, entries, `{"workspace": {"current_dir": "`+t.TempDir()+`"}}`); bare != "\n" {
		t.Fatalf("outside a repository the line is %q, want it empty", bare)
	}
}

// gitIn runs git in a repository built for a test, with an identity of its own
// so a machine without one still gets a commit.
func gitIn(t *testing.T, work string, args ...string) {
	t.Helper()
	full := append([]string{"-C", work, "-c", "user.name=e2e", "-c", "user.email=e2e@example.com"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func inDir(work string) string {
	return `{"workspace": {"current_dir": "` + work + `"}}`
}

// The branch is asked with the command that answers the branch, not with the
// one that answers what HEAD resolves to. Measured with git 2.43: rev-parse
// --abbrev-ref HEAD writes the word HEAD both in a repository without a first
// commit, where the branch exists and is simply empty, and on a detached head,
// where there is no branch at all, so the entry stood there naming a branch
// nobody is on.
func TestScriptNamesTheBranchOfARepositoryWithoutACommit(t *testing.T) {
	requireTool(t, "jq")
	requireTool(t, "git")
	work := t.TempDir()
	gitIn(t, work, "init", "--initial-branch=work")
	entries := Normalize([]Entry{{Kind: KindValue, Value: "branch", Color: "magenta"}})
	if out := run(t, entries, inDir(work)); out != "\x1b[35mwork\x1b[0m\n" {
		t.Fatalf("a repository without a commit is on %q, want work", out)
	}
	gitIn(t, work, "commit", "--allow-empty", "-m", "first")
	if out := run(t, entries, inDir(work)); out != "\x1b[35mwork\x1b[0m\n" {
		t.Fatalf("after the first commit the branch is %q, want work", out)
	}
	// A detached head is on no branch, and no branch is no value.
	gitIn(t, work, "checkout", "--detach", "HEAD")
	if out := run(t, entries, inDir(work)); out != "\n" {
		t.Fatalf("a detached head says %q, want nothing", out)
	}
}

// A folder somebody just wrote files into is that many changed files, not one:
// git collapses an untracked directory into a single line unless it is asked
// for every file.
func TestScriptCountsEveryNewFileOfANewFolder(t *testing.T) {
	requireTool(t, "jq")
	requireTool(t, "git")
	work := t.TempDir()
	gitIn(t, work, "init", "--initial-branch=work")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatalf("write a file: %v", err)
	}
	gitIn(t, work, "add", "a.txt")
	gitIn(t, work, "commit", "-m", "first")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatalf("change the file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(work, "sub"), 0o700); err != nil {
		t.Fatalf("make a folder: %v", err)
	}
	for _, name := range []string{"b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(work, "sub", name), []byte("new\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entries := Normalize([]Entry{{Kind: KindValue, Value: "git_changes"}})
	if out := run(t, entries, inDir(work)); out != "3\n" {
		t.Fatalf("one changed file and two new ones in a new folder count as %q, want 3", out)
	}
}

// mapfile is a bash 4 builtin and macOS ships 3.2 as /bin/bash, so the counts
// have to hold without it. BASH_ENV is read before a non-interactive script
// runs, which is what lets a bash 4 stand in for a bash that never had the
// builtin: what it disables is gone for the whole script.
func TestScriptCountsTheRepositoryWithoutMapfile(t *testing.T) {
	requireTool(t, "jq")
	requireTool(t, "git")
	work := t.TempDir()
	gitIn(t, work, "init", "--initial-branch=work")
	gitIn(t, work, "commit", "--allow-empty", "-m", "first")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatalf("write a file: %v", err)
	}
	gitIn(t, work, "add", "a.txt")
	gitIn(t, work, "stash")
	if err := os.WriteFile(filepath.Join(work, "b.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatalf("write a second file: %v", err)
	}
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "git_changes", Label: "±", LabelColor: "dim"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "git_stashes", Label: "s", LabelColor: "dim"},
	})
	dir := t.TempDir()
	if err := Apply(dir, entries); err != nil {
		t.Fatalf("write the script: %v", err)
	}
	profile := filepath.Join(t.TempDir(), "no-mapfile.sh")
	if err := os.WriteFile(profile, []byte("enable -n mapfile\n"), 0o600); err != nil {
		t.Fatalf("write the profile: %v", err)
	}
	cmd := exec.Command("bash", ScriptPath(dir))
	cmd.Stdin = strings.NewReader(inDir(work))
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "BASH_ENV="+profile)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run the script: %v (stderr %q)", err, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("without mapfile the script wrote to stderr: %q", stderr.String())
	}
	want := "\x1b[2m±\x1b[0m 1 \x1b[2m·\x1b[0m \x1b[2ms\x1b[0m 1\n"
	if string(out) != want {
		t.Fatalf("without mapfile the git line is %q, want %q", out, want)
	}
}

func TestScriptOnlyPaysForWhatTheLineShows(t *testing.T) {
	plain := Script("/state", Normalize([]Entry{{Kind: KindValue, Value: "model", Color: "cyan"}}))
	for _, unwanted := range []string{"api.anthropic.com", "credentials.json", "git -C", "date +"} {
		if strings.Contains(plain, unwanted) {
			t.Errorf("a line of the model alone still carries %q", unwanted)
		}
	}
	// The weekly limit and its reset are in the payload, so a line of those
	// two asks no network at all.
	weekly := Script("/state", Normalize([]Entry{
		{Kind: KindValue, Value: "week", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindValue, Value: "reset", Thresholds: []Threshold{{At: 0, Color: "blue"}}},
	}))
	if strings.Contains(weekly, "api.anthropic.com") {
		t.Error("the weekly limit out of the payload still calls the usage API")
	}
	full := Script("/state", Normalize([]Entry{
		{Kind: KindValue, Value: "branch"},
		{Kind: KindValue, Value: "week_top", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindValue, Value: "time"},
	}))
	for _, wanted := range []string{"api.anthropic.com", "git -C", "date +%H:%M", "/state/claude-statusline-usage.json"} {
		if !strings.Contains(full, wanted) {
			t.Errorf("a line that needs it does not carry %q", wanted)
		}
	}
	if strings.Contains(full, "Bearer $token") {
		t.Error("the token stands in curl's arguments")
	}
}

// The free text is the entry's own and reaches the line as it stands.
func TestScriptWritesFreeText(t *testing.T) {
	requireTool(t, "bash")
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: FreeTextValue, Text: `on "eax"`, Color: "cyan"},
	})
	if out := run(t, entries, "{}"); out != "\x1b[36mon \"eax\"\x1b[0m\n" {
		t.Fatalf("the free text renders %q", out)
	}
}

func TestApplyAndClearFollowTheSwitch(t *testing.T) {
	dir := t.TempDir()
	if err := Sync(dir, "", false); err != nil {
		t.Fatalf("sync without a setting: %v", err)
	}
	if _, err := os.Stat(ScriptPath(dir)); !os.IsNotExist(err) {
		t.Fatal("a cockpit that was never told wrote a status line script")
	}
	// A stored list with the switch off is a list nobody threw away, and it
	// still writes no script.
	if err := Sync(dir, Encode(Config{Enabled: false, Entries: Defaults()}), true); err != nil {
		t.Fatalf("sync with the switch off: %v", err)
	}
	if _, err := os.Stat(ScriptPath(dir)); !os.IsNotExist(err) {
		t.Fatal("a status line that is switched off wrote its script anyway")
	}
	if err := Sync(dir, Encode(Config{Enabled: true, Entries: Defaults()}), true); err != nil {
		t.Fatalf("sync with a setting: %v", err)
	}
	info, err := os.Stat(ScriptPath(dir))
	if err != nil {
		t.Fatalf("the script is not there: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("the script is not executable (%v)", info.Mode())
	}
	// The cached usage answer belongs to the script and goes with it.
	if err := os.WriteFile(cachePath(dir), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write the cache: %v", err)
	}
	if err := Sync(dir, "", false); err != nil {
		t.Fatalf("sync back to nothing: %v", err)
	}
	for _, path := range []string{ScriptPath(dir), cachePath(dir)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived the way out", filepath.Base(path))
		}
	}
}

// runIn keeps the state directory between calls, which is what the cost of the
// last turn is built on: the run before has to have left something behind.
func runIn(t *testing.T, dir string, entries []Entry, stdin string) string {
	t.Helper()
	requireTool(t, "bash")
	cmd := exec.Command("bash", ScriptPath(dir))
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run the script: %v (stderr %q)", err, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("the script wrote to stderr: %q", stderr.String())
	}
	return string(out)
}

// The burn rate is the one value that refuses to answer early: a coder that
// has run for seconds would report a number nobody can spend.
func TestScriptHoldsTheBurnRateBackUntilItMeansSomething(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{{Kind: KindValue, Value: "burn", Thresholds: []Threshold{{At: 0, Color: "green"}}}})
	early := `{"cost": {"total_cost_usd": 0.09, "total_duration_ms": 11656}}`
	if out := run(t, entries, early); out != "\n" {
		t.Fatalf("under a minute the burn rate says %q, want nothing", out)
	}
	late := `{"cost": {"total_cost_usd": 0.1266185, "total_duration_ms": 60904}}`
	if out := run(t, entries, late); out != "\x1b[32m$7.48/h\x1b[0m\n" {
		t.Fatalf("the burn rate is %q, want $7.48/h", out)
	}
}

// The cost of the last turn is a difference against what the run before wrote
// down, so it needs two readings and keeps the last rise until a new one comes.
func TestScriptRemembersWhatTheLastTurnCost(t *testing.T) {
	requireTool(t, "jq")
	dir := t.TempDir()
	entries := Normalize([]Entry{{Kind: KindValue, Value: "cost_turn", Thresholds: []Threshold{{At: 0, Color: "green"}}}})
	if err := Apply(dir, entries); err != nil {
		t.Fatalf("write the script: %v", err)
	}
	payload := func(cost string) string {
		return `{"session_id": "11111111-2222-3333-4444-555555555555", "cost": {"total_cost_usd": ` + cost + `}}`
	}
	if out := runIn(t, dir, entries, payload("0.090193")); out != "\n" {
		t.Fatalf("the first reading says %q, want nothing to compare against", out)
	}
	// The rise is 0.0364255, and it is rounded to the cent like every other
	// amount on the line rather than cut off at it.
	if out := runIn(t, dir, entries, payload("0.1266185")); out != "\x1b[32m$0.04\x1b[0m\n" {
		t.Fatalf("the rise says %q, want $0.04", out)
	}
	// A line drawn again without a turn in between keeps the last turn's cost
	// instead of wiping it to nothing.
	if out := runIn(t, dir, entries, payload("0.1266185")); out != "\x1b[32m$0.04\x1b[0m\n" {
		t.Fatalf("a repeat says %q, want the last rise kept", out)
	}
	// Another coder is another reading: its own file, no difference yet.
	other := `{"session_id": "22222222-2222-3333-4444-555555555555", "cost": {"total_cost_usd": 5}}`
	if out := runIn(t, dir, entries, other); out != "\n" {
		t.Fatalf("a second coder starts at %q, want nothing", out)
	}
}

// The session sums are the transcript's, and they are what the last request's
// counts cannot say.
func TestScriptAddsUpTheWholeTranscript(t *testing.T) {
	requireTool(t, "jq")
	dir := t.TempDir()
	transcript := filepath.Join(dir, "transcript.jsonl")
	lines := strings.Join([]string{
		`{"type": "user", "message": {"role": "user", "content": "hi"}}`,
		`"a line that is no object at all"`,
		`{"type": "assistant", "message": {"usage": {"input_tokens": 2000, "output_tokens": 8000, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 4000}}}`,
		`{"type": "assistant", "message": {"usage": {"input_tokens": 3000, "output_tokens": 7000, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 6000}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(transcript, []byte(lines), 0o600); err != nil {
		t.Fatalf("write the transcript: %v", err)
	}
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "session_input", Label: "i", LabelColor: "dim"},
		{Kind: KindValue, Value: "session_output", Label: "o", LabelColor: "dim"},
		{Kind: KindValue, Value: "session_cache_read", Label: "r", LabelColor: "dim"},
		{Kind: KindValue, Value: "session_cache_write", Label: "w", LabelColor: "dim"},
	})
	out := run(t, entries, `{"transcript_path": "`+transcript+`"}`)
	want := "\x1b[2mi\x1b[0m 5k \x1b[2mo\x1b[0m 15k \x1b[2mr\x1b[0m 10k \x1b[2mw\x1b[0m 1k\n"
	if out != want {
		t.Fatalf("the session sums are %q, want %q", out, want)
	}
	// Without a transcript to read they fall away instead of showing zeroes.
	if bare := run(t, entries, `{"transcript_path": "/nowhere/at/all.jsonl"}`); bare != "\n" {
		t.Fatalf("without a transcript the line is %q, want it empty", bare)
	}
}

// sessionSums is the four session entries, the ones read out of the transcript.
func sessionSums() []Entry {
	return Normalize([]Entry{
		{Kind: KindValue, Value: "session_input", Label: "i", LabelColor: "dim"},
		{Kind: KindValue, Value: "session_output", Label: "o", LabelColor: "dim"},
		{Kind: KindValue, Value: "session_cache_read", Label: "r", LabelColor: "dim"},
		{Kind: KindValue, Value: "session_cache_write", Label: "w", LabelColor: "dim"},
	})
}

func writeTranscript(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatalf("write the transcript: %v", err)
	}
	return path
}

// A turn is a request, not a line. claude writes one record per content block
// of an answer, the thinking, the text and every tool call, and every one of
// them repeats the **same** usage object of the whole request. Adding the
// records up therefore counts a turn as often as it had blocks, which on the
// real transcripts of this machine is about twice the tokens actually spent.
func TestScriptCountsATurnOnceHoweverManyBlocksItWrote(t *testing.T) {
	requireTool(t, "jq")
	usage := `{"input_tokens": 2000, "output_tokens": 8000, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 4000}`
	block := func(id, kind string) string {
		return `{"type": "assistant", "requestId": "` + id + `", "uuid": "` + id + kind + `", "message": {"content": [{"type": "` + kind + `"}], "usage": ` + usage + `}}`
	}
	// One request, four blocks; then a second request with one.
	lines := strings.Join([]string{
		block("req_1", "thinking"), block("req_1", "text"), block("req_1", "tool_use"), block("req_1", "tool_use"),
		block("req_2", "text"),
	}, "\n") + "\n"
	out := run(t, sessionSums(), `{"transcript_path": "`+writeTranscript(t, lines)+`"}`)
	// Two turns, not five: 4k in, 16k out, 8k read, 2k written.
	want := "\x1b[2mi\x1b[0m 4k \x1b[2mo\x1b[0m 16k \x1b[2mr\x1b[0m 8k \x1b[2mw\x1b[0m 2k\n"
	if out != want {
		t.Fatalf("the session sums are %q, want %q", out, want)
	}
	// A record without a request id cannot be told from another one, so it is
	// counted as it stands rather than folded into one shared empty key.
	bare := `{"type": "assistant", "message": {"usage": ` + usage + `}}`
	out = run(t, sessionSums(), `{"transcript_path": "`+writeTranscript(t, bare+"\n"+bare+"\n")+`"}`)
	if want := "\x1b[2mi\x1b[0m 4k \x1b[2mo\x1b[0m 16k \x1b[2mr\x1b[0m 8k \x1b[2mw\x1b[0m 2k\n"; out != want {
		t.Fatalf("records without a request id sum to %q, want %q", out, want)
	}
}

// A conversation nobody has spent anything on yet has a transcript with no
// usage record in it, which is every session until its first answer. Four
// zeroes nobody measured are not an answer, so the entries leave the line the
// way every unanswered value does.
func TestScriptSaysNothingWithoutATurnToAddUp(t *testing.T) {
	requireTool(t, "jq")
	for _, lines := range []string{"", `{"type": "user", "message": {"role": "user", "content": "hi"}}` + "\n"} {
		if out := run(t, sessionSums(), `{"transcript_path": "`+writeTranscript(t, lines)+`"}`); out != "\n" {
			t.Errorf("a conversation without a turn says %q, want nothing", out)
		}
	}
}

// The line is drawn while claude is appending to the very file it reads, so the
// last record is regularly half written. One unreadable record must not take
// the four sums with it.
func TestScriptKeepsTheSumsWhileTheTranscriptIsBeingWritten(t *testing.T) {
	requireTool(t, "jq")
	lines := `{"type": "assistant", "requestId": "req_1", "message": {"usage": {"input_tokens": 2000, "output_tokens": 8000, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 4000}}}` + "\n" +
		`{"type": "assistant", "requestId": "req_2", "message": {"usage": {"input_t`
	out := run(t, sessionSums(), `{"transcript_path": "`+writeTranscript(t, lines)+`"}`)
	want := "\x1b[2mi\x1b[0m 2k \x1b[2mo\x1b[0m 8k \x1b[2mr\x1b[0m 4k \x1b[2mw\x1b[0m 1k\n"
	if out != want {
		t.Fatalf("with a half written last record the sums are %q, want %q", out, want)
	}
}

// Every field travels to the shell as one line split on the unit separator, so
// a value carrying a line break or that separator would end the read early and
// hand the values behind it to the wrong names, and an escape would reach the
// terminal as a command instead of as text. They are cut out where the value is
// read, which is the only place that sees all of them.
func TestScriptKeepsTheLineWhenAPayloadValueCarriesAControlCharacter(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "version", Color: "default"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context", Thresholds: []Threshold{{At: 0, Color: "green"}}},
	})
	// The control characters ride in as the JSON escapes claude would send:
	// a line break and the unit separator inside the model's name, an escape
	// in front of the version. All three are gone and every value behind them
	// still arrives under its own name.
	body := `{"model": {"display_name": "Op\nus\u001f 5 (1M)"}, "version": "\u001b[31m2.1.233", "context_window": {"used_percentage": 42}}`
	out := run(t, entries, body)
	want := "\x1b[36mOpus 5\x1b[0m \x1b[2m·\x1b[0m [31m2.1.233 \x1b[2m·\x1b[0m \x1b[32m42%\x1b[0m\n"
	if out != want {
		t.Fatalf("the line is %q, want %q", out, want)
	}
}

// Two amounts on one line are written the same way: to the cent, always, so
// a dollar and a half is $1.50 and never $1.5 beside the last turn's $0.13.
func TestScriptWritesMoneyToTheCent(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{{Kind: KindValue, Value: "cost", Color: "default", Thresholds: nil}})
	cases := map[string]string{
		"1.5":    "$1.50",
		"2":      "$2.00",
		"1.2449": "$1.24",
		"12.345": "$12.35",
		"0.005":  "$0.01",
	}
	for amount, want := range cases {
		if out := run(t, entries, `{"cost": {"total_cost_usd": `+amount+`}}`); out != want+"\n" {
			t.Errorf("%s renders %q, want %q", amount, out, want)
		}
	}
	// The rate is the same amount with an hour behind it.
	rate := Normalize([]Entry{{Kind: KindValue, Value: "burn"}})
	if out := run(t, rate, `{"cost": {"total_cost_usd": 2.5, "total_duration_ms": 3600000}}`); out != "$2.50/h\n" {
		t.Errorf("the burn rate is %q, want $2.50/h", out)
	}
}

// A bound is compared against the number on the screen. Two amounts that print
// the same have to wear the same color, and the last turn's rise, which the
// shell works out in whole cents, is the one that decides which way that goes:
// $1.499 stands as $1.50 on the line, so a bound at 1.50 is reached.
func TestScriptComparesABoundAgainstWhatIsPrinted(t *testing.T) {
	requireTool(t, "jq")
	dir := t.TempDir()
	bounds := []Threshold{{At: 0, Color: "green"}, {At: 1.5, Color: "red"}}
	total := Normalize([]Entry{{Kind: KindValue, Value: "cost", Thresholds: bounds}})
	if out := run(t, total, `{"cost": {"total_cost_usd": 1.499}}`); out != "\x1b[31m$1.50\x1b[0m\n" {
		t.Errorf("a cost of 1.499 prints %q, want $1.50 in red", out)
	}
	if out := run(t, total, `{"cost": {"total_cost_usd": 1.494}}`); out != "\x1b[32m$1.49\x1b[0m\n" {
		t.Errorf("a cost of 1.494 prints %q, want $1.49 in green", out)
	}
	// The same amount as a rise between two readings, which is rounded to the
	// cent on its own way to the line.
	turn := Normalize([]Entry{{Kind: KindValue, Value: "cost_turn", Thresholds: bounds}})
	if err := Apply(dir, turn); err != nil {
		t.Fatalf("write the script: %v", err)
	}
	payload := func(cost string) string {
		return `{"session_id": "33333333-2222-3333-4444-555555555555", "cost": {"total_cost_usd": ` + cost + `}}`
	}
	runIn(t, dir, turn, payload("0"))
	if out := runIn(t, dir, turn, payload("1.499")); out != "\x1b[31m$1.50\x1b[0m\n" {
		t.Errorf("a rise of 1.499 prints %q, want $1.50 in red like the total", out)
	}
}

// The limits are a plan's numbers. An API key has none, and the payload then
// carries no rate_limits at all: the entries have to leave the line together
// with the separators that would otherwise lead nowhere.
func TestScriptDropsTheLimitsWithoutAPlan(t *testing.T) {
	requireTool(t, "jq")
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "session", Label: "5", LabelColor: "dim", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "week", Label: "w", LabelColor: "dim", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "reset", Label: "↻", LabelColor: "dim", Thresholds: []Threshold{{At: 0, Color: "blue"}}},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "dir", Color: "default"},
	})
	body := `{"model": {"display_name": "Opus 5"}, "cwd": "/root/projects/dev-cockpit"}`
	out := run(t, entries, body)
	want := "\x1b[36mOpus 5\x1b[0m \x1b[2m·\x1b[0m dev-cockpit\n"
	if out != want {
		t.Fatalf("without rate limits the line is %q, want %q", out, want)
	}
}

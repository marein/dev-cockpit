package assistant

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The provider failure the user can act on is the one worth naming: a CLI that
// was never logged in on this machine. Everything else stays generic.
func TestLooksLikeLogin(t *testing.T) {
	yes := []string{
		"Invalid API key · Please run /login",
		"You are not logged in. Run copilot and sign in.",
		"Error: authentication required",
		"HTTP 401 Unauthorized",
	}
	for _, line := range yes {
		if !LooksLikeLogin(line) {
			t.Errorf("expected a login hint for %q", line)
		}
	}
	no := []string{
		"panic: runtime error: index out of range",
		"error: max tokens exceeded",
		"tool use failed: permission denied for /etc/shadow",
		"",
	}
	for _, line := range no {
		if LooksLikeLogin(line) {
			t.Errorf("unexpected login hint for %q", line)
		}
	}
}

// startedEnv runs c through start in workdir and answers the environment its
// process found. The program is env itself and no shell in front of it: sh
// puts PWD right when the one it inherited does not name its directory, so a
// shell would hide exactly what this asks about.
func startedEnv(t *testing.T, c Command, workdir string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	p, err := start(c, workdir, out, filepath.Join(dir, "err"), filepath.Join(dir, "lock"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	p.Wait()
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the process wrote no environment: %v", err)
	}
	seen := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		key, value, _ := strings.Cut(line, "=")
		seen[key] = value
	}
	return seen
}

// A turn's process sees its workdir as PWD whether or not the command asks for
// variables of its own: os/exec writes that line only when it inherits the
// environment, and opencode reads its project root off it, so a copy carrying
// the server's own PWD ran the turn against the server's directory. The
// coder's variables still land on top of the inherited environment, and a
// command without any is left to what os/exec does by itself.
func TestATurnRunsWithPWDAtItsWorkdir(t *testing.T) {
	// The server carries the PWD of the shell it was started from.
	t.Setenv("PWD", "/")
	workdir := t.TempDir()

	seen := startedEnv(t, Command{Name: "env", Env: []string{"DC_FAKE_TURN=from the command"}}, workdir)
	if seen["PWD"] != workdir {
		t.Fatalf("want PWD=%s in a process with the command's variables, got %q", workdir, seen["PWD"])
	}
	if seen["DC_FAKE_TURN"] != "from the command" {
		t.Fatalf("want the command's variable in the process, got %q", seen["DC_FAKE_TURN"])
	}
	if seen["PATH"] == "" {
		t.Fatal("want the server's own environment kept under the command's variables")
	}

	seen = startedEnv(t, Command{Name: "env"}, workdir)
	if seen["PWD"] != workdir {
		t.Fatalf("want PWD=%s in a process without variables of its own, got %q", workdir, seen["PWD"])
	}
	if value, ok := seen["DC_FAKE_TURN"]; ok {
		t.Fatalf("want no variable of the command in a process without any, got %q", value)
	}
	if seen["PATH"] == "" {
		t.Fatal("want a command without variables to inherit the environment")
	}

	// A relative workdir lands absolute, the way os/exec writes it.
	t.Chdir(filepath.Dir(workdir))
	seen = startedEnv(t, Command{Name: "env", Env: []string{"DC_FAKE_TURN=from the command"}}, filepath.Base(workdir))
	if seen["PWD"] != workdir {
		t.Fatalf("want a relative workdir as PWD=%s, got %q", workdir, seen["PWD"])
	}
}

// A refusal is worded here and says where the model was set: the ring button
// for a chat turn and a check, the trigger for a reaction that ran on the
// trigger's own model, the ring again where the trigger followed the
// assistant's. What the user picked stands above what the CLI named, and a
// CLI whose own default is unknown is told so without a name.
// longModel is a model name the value rule takes whole, forty runes of letters
// and digits in one run, the shape the redaction of token runs would take out
// of a quoted line. The three surfaces that show a refusal are pinned on it,
// because a sentence whose purpose is to name the model must name it whole.
const longModel = "copilot-claude-sonnet-4-5-20250929-abcde"

func TestARefusalSaysWhereTheModelWasSet(t *testing.T) {
	refusal := &Refusal{Kind: RefusalUnknownModel, Model: "cli-said"}
	cases := []struct {
		rec  RunRecord
		want string
	}{
		{RunRecord{Kind: RunChat, Model: "picked"}, "The coder does not know the model picked. Pick another at the ring button of this assistant."},
		{RunRecord{Kind: RunCheck, Model: "picked"}, "The coder does not know the model picked. Pick another at the ring button of this assistant."},
		{RunRecord{Kind: RunReaction, Model: "picked", TriggerModel: true}, "The coder does not know the model picked. Pick another on the trigger."},
		{RunRecord{Kind: RunReaction, Model: "picked"}, "The coder does not know the model picked. Pick another at the ring button of this assistant."},
		{RunRecord{Kind: RunChat}, "The coder does not know the model cli-said. Pick another at the ring button of this assistant."},
	}
	for _, c := range cases {
		if got := refusal.placed(c.rec).Error(); got != c.want {
			t.Errorf("for %+v\n got %q\nwant %q", c.rec, got, c.want)
		}
	}
	if got := (&Refusal{Kind: RefusalUnknownModel}).placed(RunRecord{Kind: RunChat}).Error(); got != "The coder does not know the model it was started with. Pick another at the ring button of this assistant." {
		t.Errorf("want the sentence for a CLI whose own default is unknown, got %q", got)
	}
	if refusal.Model != "cli-said" || refusal.picked != "" {
		t.Fatal("placing a refusal must copy it, the parser's value is shared")
	}
	// The login kind is the sentinel every caller compared against, placed or not.
	login := ErrNotLoggedIn.placed(RunRecord{Kind: RunCheck})
	if !errors.Is(login, ErrNotLoggedIn) || login.Error() != ErrNotLoggedIn.Error() || errors.Is(refusal, ErrNotLoggedIn) {
		t.Fatalf("want a placed login refusal to still be ErrNotLoggedIn and nothing else, got %v", login)
	}
	// A name the value rule takes stands whole in the sentence the chat and a
	// reaction show, however long: it is the cockpit's sentence, not a quote.
	if len([]rune(longModel)) != 40 {
		t.Fatalf("the pinned name is %d runes, want 40", len([]rune(longModel)))
	}
	if got := sanitizeError(UnknownModel("").placed(RunRecord{Kind: RunChat, Model: longModel})); got != "The coder does not know the model "+longModel+". Pick another at the ring button of this assistant." {
		t.Fatalf("want the long name whole in the sentence, got %q", got)
	}
}

// What a CLI names as the model it does not know is read under the value rule
// before it reaches a sentence: trimmed, cut to MaxModelRunes, and dropped
// where the rule refuses it, so a line the CLI echoed never carries a token or
// a second sentence into the cockpit's own.
func TestUnknownModelCutsAndCleansWhatTheCLINamed(t *testing.T) {
	if got := UnknownModel(" haiku ").Model; got != "haiku" {
		t.Fatalf("want the name trimmed, got %q", got)
	}
	long := strings.Repeat("a1", MaxModelRunes)
	if got := UnknownModel(long).Model; got != strings.Repeat("a1", MaxModelRunes/2) {
		t.Fatalf("want the name cut to %d runes, got %d", MaxModelRunes, len([]rune(got)))
	}
	if got := UnknownModel(longModel).Model; got != longModel {
		t.Fatalf("want a name the rule takes kept whole, got %q", got)
	}
	for _, raw := range []string{"two words", "-foo", "model;rm", "x\ny"} {
		if got := UnknownModel(raw).Model; got != "" {
			t.Fatalf("want %q dropped, got %q", raw, got)
		}
	}
	refusal := UnknownModel("two words")
	if refusal.Kind != RefusalUnknownModel || !errors.Is(refusal, &Refusal{Kind: RefusalUnknownModel}) {
		t.Fatalf("want the unknown model kind whatever the name, got %+v", refusal)
	}
	if got := refusal.placed(RunRecord{Kind: RunChat}).Error(); !strings.Contains(got, "the model it was started with") {
		t.Fatalf("want a dropped name to read as the CLI's own default, got %q", got)
	}
}

// A failure nobody named quotes the coder once: the last non empty line of
// what it said, behind the cockpit's sentence, and nothing said leaves the
// frame alone.
func TestQuoteTakesTheLastNonEmptyLine(t *testing.T) {
	frame := errors.New("The coder stopped before it finished the answer.")
	err := Quote(frame, "warning: something\n\nError: the request failed\n   \n\n")
	if err.Error() != "The coder stopped before it finished the answer. The coder said: Error: the request failed" {
		t.Fatalf("want the last non empty line quoted, got %q", err)
	}
	if !errors.Is(err, frame) {
		t.Fatal("want the frame reachable behind the quote")
	}
	if got := sanitizeError(err); got != err.Error() {
		t.Fatalf("want the quote to survive sanitizeError whole, got %q", got)
	}
	for _, said := range []string{"", "  \n\n \n"} {
		if got := Quote(frame, said); got != frame {
			t.Fatalf("want the frame alone for %q, got %v", said, got)
		}
	}
	// A quote is never doubled and a refusal is never quoted.
	if got := Quote(err, "later line"); got != err {
		t.Fatalf("want the first quote kept, got %v", got)
	}
	if got := Quote(ErrNotLoggedIn, "Error: not logged in"); got != ErrNotLoggedIn {
		t.Fatalf("want a refusal left as it is, got %v", got)
	}
	if Quote(nil, "line") != nil {
		t.Fatal("no failure, nothing to quote")
	}
	long := strings.Repeat("x", 300)
	if got := Quote(frame, long).(*Said).Line; len([]rune(got)) != quoteRunes+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("want the line cut at %d runes, got %d", quoteRunes, len([]rune(got)))
	}
}

// What a quoted line must not carry: a key by its issuer's prefix or by its
// name, a bearer token, a run of nothing but token characters longer than a
// model name may be, the directories of a path. The file name and every
// ordinary word stay, a model name included, up to the value rule's own bound,
// so the two rules about one string cannot disagree. The shapes are built here
// and not written out, a test must not hold a line that looks like a
// credential either.
func TestQuoteRedactsKeysTokensAndPaths(t *testing.T) {
	frame := errors.New("The coder could not finish this answer.")
	byPrefix := "sk-" + strings.Repeat("a1", 8)
	byName := "api_key=" + strings.Repeat("q", 12)
	bearer := "Bearer " + strings.Repeat("t0", 12)
	run := strings.Repeat("Zx9", MaxModelRunes/3+1)
	name := strings.Repeat("Zx9", MaxModelRunes/3) + "Zx"
	if len([]rune(run)) != MaxModelRunes+1 || len([]rune(name)) != MaxModelRunes {
		t.Fatalf("the shapes are off, %d and %d runes", len([]rune(run)), len([]rune(name)))
	}
	cases := []struct{ said, want string }{
		{"401 for " + byPrefix + " at api", "401 for [redacted] at api"},
		{"request with " + byName + " refused", "request with api_key=[redacted] refused"},
		{"header " + bearer + " rejected", "header Bearer [redacted] rejected"},
		{"session " + run + " not found", "session [redacted] not found"},
		{"model " + name + " not found", "model " + name + " not found"},
		{"model " + longModel + " not found", "model " + longModel + " not found"},
		{"cannot open /home/someone/.config/app/config.json", "cannot open …/config.json"},
		{"see https://example.com/docs/errors for the model claude-sonnet-4-5-20250929", "see https://example.com/docs/errors for the model claude-sonnet-4-5-20250929"},
		{"model " + strings.Repeat("a", MaxModelRunes+5) + " is a word without digits", "model " + strings.Repeat("a", MaxModelRunes+5) + " is a word without digits"},
	}
	for _, c := range cases {
		got := Quote(frame, c.said).(*Said).Line
		if got != c.want {
			t.Errorf("for %q\n got %q\nwant %q", c.said, got, c.want)
		}
	}
	// A sentence the cockpit wrote is never redacted, only kept to one line
	// and cut: it carries nothing to redact, and the name in it is the point.
	if got := sanitizeError(errors.New("The model " + run + "\n is not known.")); got != "The model "+run+" is not known." {
		t.Errorf("want a curated sentence one line and untouched, got %q", got)
	}
	if got := sanitizeError(errors.New(strings.Repeat("y", 300))); len([]rune(got)) != quoteRunes+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("want a curated sentence cut at %d runes, got %d", quoteRunes, len([]rune(got)))
	}
}

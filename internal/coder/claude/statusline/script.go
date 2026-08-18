package statusline

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	scriptName = "claude-statusline.sh"
	cacheName  = "claude-statusline-usage.json"
	// costDirName holds one small file per coder, the last cost reading and
	// the last rise, which is what makes a difference to the run before
	// possible at all. It is derived state like the usage cache and goes with
	// the script.
	costDirName = "claude-statusline-cost"
)

// usageMaxAge is how long the answer of the usage API stands before the script
// asks again. The stamp file is touched before the call, so a machine without
// a network pays the timeout once every two minutes and not once per redraw.
const usageMaxAge = 120

// ScriptPath is the generated script. Its presence is what the runtime reads:
// a session gets a statusLine exactly when this file is there, which is what
// the switch on the settings page writes and takes away.
func ScriptPath(stateDir string) string { return filepath.Join(stateDir, scriptName) }

// cachePath is where the script keeps the last answer of the usage API, next
// to a stamp file of the same name plus ".stamp".
func cachePath(stateDir string) string { return filepath.Join(stateDir, cacheName) }

// costDir is where the last cost reading per coder is kept.
func costDir(stateDir string) string { return filepath.Join(stateDir, costDirName) }

// Apply writes the script for these entries. It is written beside the target
// and renamed onto it, so a session starting while a save runs never reads
// half a script.
func Apply(stateDir string, entries []Entry) error {
	path := ScriptPath(stateDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(Script(stateDir, entries)), 0o700); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Clear takes the status line away again: the script goes, and with it the
// cached usage answer it wrote. What was configured stays in the settings
// store, so switching back on brings the same line back. Removing what is not
// there is no error.
func Clear(stateDir string) error {
	var first error
	for _, path := range []string{ScriptPath(stateDir), cachePath(stateDir), cachePath(stateDir) + ".stamp"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && first == nil {
			first = err
		}
	}
	if err := os.RemoveAll(costDir(stateDir)); err != nil && first == nil {
		first = err
	}
	return first
}

// Sync brings the script in line with what the settings store holds: the
// switch decides whether there is one at all. A value that cannot be read is
// treated like nothing stored, because a script written out of a guess is
// worse than none.
func Sync(stateDir, raw string, set bool) error {
	if !set {
		return Clear(stateDir)
	}
	config, err := Decode(raw)
	if err != nil || !config.Enabled {
		return Clear(stateDir)
	}
	return Apply(stateDir, config.Entries)
}

// Script renders the bash the entries describe. Only what the line actually
// shows is computed: no jq call without a payload value, no git process
// without a repository entry, no shell command nobody reads, and no network
// call without the one entry the payload cannot answer.
func Script(stateDir string, entries []Entry) string {
	entries = Normalize(entries)
	used := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind == KindValue {
			used[entry.Value] = true
		}
	}
	usedValues := func(want source) []Value {
		out := []Value{}
		for _, value := range Values {
			if used[value.ID] && value.source == want {
				out = append(out, value)
			}
		}
		return out
	}
	payload := usedValues(fromPayload)
	usage := usedValues(fromUsage)
	git := usedValues(fromGit)
	shell := usedValues(fromShell)
	transcript := usedValues(fromTranscript)

	var b strings.Builder
	b.WriteString(scriptHead)
	b.WriteString(declarations(used))
	if fields := payloadFields(payload, len(git) > 0, len(transcript) > 0, used["cost_turn"]); len(fields) > 0 {
		b.WriteString("\n# The payload claude writes to this script's stdin.\n")
		b.WriteString(readBlock(fields, jqDefs+arrayJoin(fields), `<<<"$input"`))
	}
	if needsNow(used) {
		b.WriteString("\nnow=$(date +%s 2>/dev/null || echo 0)\n")
	}
	if len(git) > 0 {
		b.WriteString(gitBlock(git))
	}
	if len(shell) > 0 {
		b.WriteString(shellBlock(shell))
	}
	if len(usage) > 0 {
		b.WriteString(usageBlock(stateDir, usage))
	}
	if len(transcript) > 0 {
		b.WriteString(transcriptBlock(transcript))
	}
	if used["cost_turn"] {
		b.WriteString(costTurnBlock(stateDir))
	}
	b.WriteString(spanBlock(used))
	b.WriteString("\n")
	for _, entry := range entries {
		b.WriteString(entryLine(entry))
	}
	b.WriteString("\nout+=\"$line\"\nprintf '%s\\n' \"$out\"\n")
	return b.String()
}

// needsNow reports whether any value has to know what time it is: a length of
// time is measured against now, and the usage cache is aged against it.
func needsNow(used map[string]bool) bool {
	for _, value := range Values {
		if used[value.ID] && (value.resolve != resolveNone || value.source == fromUsage) {
			return true
		}
	}
	return false
}

// jqField is one value of a jq program: the shell variable that reads it and
// the expression that answers it. The order is the order of the read, so both
// sides are built from one list.
type jqField struct {
	name string
	expr string
}

// fieldsFor is what a value reads out of its document. A number is read twice,
// once as the text on the line and once times a hundred for the bounds, which
// is what lets bash compare a fraction as an integer. A length of time is the
// exception: it arrives as seconds or as a timestamp and the shell makes both
// halves of it afterwards, see spanBlock.
func fieldsFor(value Value) []jqField {
	if value.format == asDelta {
		return nil
	}
	if value.format == asSpan {
		return []jqField{{name: "v_" + value.ID, expr: "(" + value.raw + " | txt)"}}
	}
	fields := []jqField{{name: "v_" + value.ID, expr: "(" + value.raw + " | " + value.format.jq() + ")"}}
	if value.Numeric {
		fields = append(fields, jqField{name: "n_" + value.ID, expr: "(" + value.raw + " | scaled)"})
	}
	return fields
}

func (f format) jq() string {
	switch f {
	case asPercent:
		return "pct"
	case asMoney:
		return "money"
	case asCount:
		return "cnt"
	case asRate:
		return "rate"
	default:
		return "txt"
	}
}

// declarations empties every variable the line reads, so a block that never
// ran leaves an entry without a value rather than one from the environment.
func declarations(used map[string]bool) string {
	var b strings.Builder
	for _, value := range Values {
		if !used[value.ID] || value.source == fromEntry {
			continue
		}
		b.WriteString("v_" + value.ID + "=\"\"\n")
		if value.Numeric {
			b.WriteString("n_" + value.ID + "=\"\"\n")
		}
	}
	return b.String()
}

// payloadFields are the values read out of the JSON on stdin, plus the one the
// script needs for itself: the directory git is asked about.
func payloadFields(values []Value, wantCwd, wantTranscript, wantCost bool) []jqField {
	var fields []jqField
	for _, value := range values {
		fields = append(fields, fieldsFor(value)...)
	}
	if wantCwd {
		fields = append(fields, jqField{name: "cwd", expr: `((.workspace.current_dir // .cwd // "") | txt)`})
	}
	if wantTranscript {
		fields = append(fields, jqField{name: "transcript", expr: `((.transcript_path // "") | txt)`})
	}
	if wantCost {
		// The cost as whole millionths of a dollar, so the shell can take one
		// reading from another without ever doing arithmetic on a fraction,
		// plus the coder this reading belongs to.
		fields = append(fields, jqField{name: "cost_micro", expr: `(if (.cost.total_cost_usd | type) == "number" then (.cost.total_cost_usd * 1000000 | floor) else "" end | txt)`})
		fields = append(fields, jqField{name: "cost_key", expr: `((.session_id // "") | txt)`})
	}
	return fields
}

// gitBlock asks git once per value, all of it behind one check for git itself
// and every call quiet: a folder that is no repository answers nothing and the
// entries fall out of the line.
//
// countLines is what turns an answer of git's into a number, and it is written
// out rather than taken from bash's own mapfile: mapfile arrived in bash 4 and
// macOS still ships 3.2 as /bin/bash, where the missing builtin would put a
// "command not found" on stderr and leave the count at nothing, which reads as
// a clean tree on a folder full of changes.
func gitBlock(values []Value) string {
	var b strings.Builder
	b.WriteString(`
# count_lines <var> <text>, an answer of git's as the number of lines it holds.
count_lines() {
  local n=0 line
  if [ -n "$2" ]; then
    while IFS= read -r line; do n=$((n + 1)); done <<< "$2"
  fi
  printf -v "$1" '%s' "$n"
}
`)
	b.WriteString("\n# The repository the folder belongs to. No git, no entries.\n")
	b.WriteString("if command -v git >/dev/null 2>&1; then\n")
	b.WriteString("  git_dir=\"${cwd:-.}\"\n")
	for _, value := range values {
		switch value.ID {
		case "git_changes":
			// Every untracked file counts for itself: git collapses a new
			// folder into a single line by default, so a folder somebody just
			// wrote twelve files into would stand as one changed file.
			b.WriteString(`  if git_out=$(GIT_OPTIONAL_LOCKS=0 git -C "$git_dir" status --porcelain --untracked-files=all 2>/dev/null); then
    count_lines v_git_changes "$git_out"
    scale v_git_changes n_git_changes
  fi
`)
		case "git_stashes":
			b.WriteString(`  if git_out=$(GIT_OPTIONAL_LOCKS=0 git -C "$git_dir" stash list 2>/dev/null); then
    count_lines v_git_stashes "$git_out"
    scale v_git_stashes n_git_stashes
  fi
`)
		case "git_ahead_behind":
			b.WriteString(`  if git_out=$(GIT_OPTIONAL_LOCKS=0 git -C "$git_dir" rev-list --count --left-right '@{upstream}...HEAD' 2>/dev/null); then
    git_behind=${git_out%%[[:space:]]*}
    git_ahead=${git_out##*[[:space:]]}
    [ "${git_ahead:-0}" -gt 0 ] 2>/dev/null && v_git_ahead_behind="↑$git_ahead"
    [ "${git_behind:-0}" -gt 0 ] 2>/dev/null && v_git_ahead_behind="${v_git_ahead_behind:+$v_git_ahead_behind }↓$git_behind"
  fi
`)
		default:
			b.WriteString("  v_" + value.ID + "=$(GIT_OPTIONAL_LOCKS=0 git -C \"$git_dir\" " + value.shell + " 2>/dev/null)\n")
		}
	}
	b.WriteString("fi\n")
	return b.String()
}

// shellBlock is what the machine answers about itself. Every line is quiet on
// its own, so a command this host does not have takes its entry with it and
// nothing else.
func shellBlock(values []Value) string {
	var b strings.Builder
	b.WriteString("\n# What this machine says about itself.\n")
	for _, value := range values {
		b.WriteString("v_" + value.ID + "=$(" + value.shell + ")\n")
		if value.Numeric && value.format != asSpan {
			b.WriteString("scale v_" + value.ID + " n_" + value.ID + "\n")
		}
	}
	return b.String()
}

// usageBlock asks the usage API for what the payload does not carry, and
// caches the answer. The call is only in the script when an entry needs it.
// The token is read straight into curl's own configuration on stdin, so it
// stands in no process list and reaches no output of this script.
func usageBlock(stateDir string, values []Value) string {
	var fields []jqField
	for _, value := range values {
		fields = append(fields, fieldsFor(value)...)
	}
	cache := shellQuote(cachePath(stateDir))
	block := `
# What the payload does not carry, from the usage API, cached.
cache=` + cache + `
stamp=$cache.stamp
last=$(stat -c %Y "$stamp" 2>/dev/null || echo 0)
if [ "$((now - last))" -gt ` + strconv.Itoa(usageMaxAge) + ` ] && command -v jq >/dev/null 2>&1 && command -v curl >/dev/null 2>&1; then
  # The redirection is inside the group: a failing one reports itself on the
  # stderr in effect when it runs, which is the caller's unless the group
  # already carries the silence.
  { : > "$stamp"; } 2>/dev/null
  token=$(jq -r '.claudeAiOauth.accessToken // empty' "${HOME:-/root}/.claude/.credentials.json" 2>/dev/null)
  if [ -n "$token" ]; then
    printf 'header = "Authorization: Bearer %s"\n' "$token" |
      curl -s -K - --max-time 5 -H 'anthropic-beta: oauth-2025-04-20' \
        -o "$cache.tmp" https://api.anthropic.com/api/oauth/usage 2>/dev/null &&
      jq -e '.limits' "$cache.tmp" >/dev/null 2>&1 && mv "$cache.tmp" "$cache" 2>/dev/null
    rm -f "$cache.tmp" 2>/dev/null
  fi
  unset token
fi
`
	program := jqDefs + `def limit(f): ([.limits[]? | select(f)] | first // {});
limit(.kind == "weekly_scoped") as $top
| ` + arrayJoin(fields)
	return block + readBlock(fields, program, `"$cache"`)
}

// spanBlock is where a length of time becomes readable: a timestamp turns into
// the seconds to or from it first, then the seconds become one unit of time
// plus the bound value, which is minutes.
func spanBlock(used map[string]bool) string {
	var b strings.Builder
	for _, value := range Values {
		if !used[value.ID] {
			continue
		}
		switch value.resolve {
		case resolveUntil:
			b.WriteString("secs_until v_" + value.ID + "\n")
		case resolveSince:
			b.WriteString("secs_since v_" + value.ID + "\n")
		}
		if value.format == asSpan {
			b.WriteString("span v_" + value.ID + " n_" + value.ID + "\n")
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n# Lengths of time, readable plus the minutes the bounds compare against.\n" + b.String()
}

// transcriptBlock adds up what every turn of this conversation spent, in one
// pass over the transcript beside the payload. It is the only thing the
// payload cannot answer: its counts are the last request's alone.
//
// A turn is a request, not a line. claude writes one record per content block
// of an answer, thinking, text and every tool call, and every one of them
// carries the **same** usage object of the whole request, so adding the records
// up counts a turn as often as it had blocks: measured against the real
// transcripts on this machine that is roughly twice the tokens actually spent.
// The request id is what tells one turn from the next, and a record that has
// none at all is counted as it stands rather than folded into a shared empty
// key. Two more things ride on the same pass: a file whose last line is half
// written, which is every file claude is appending to right now, ends the read
// instead of failing the whole program, and a conversation without a single
// usage record answers nothing rather than four zeroes nobody measured.
func transcriptBlock(values []Value) string {
	var fields []jqField
	for _, value := range values {
		fields = append(fields, fieldsFor(value)...)
	}
	program := jqDefs + `reduce (try inputs catch empty) as $entry ({i: 0, o: 0, r: 0, w: 0, n: 0, seen: {}};
  (try $entry.message.usage catch null) as $u
  | ((try $entry.requestId catch null) // (try $entry.uuid catch null) // "") as $id
  | if ($u | type) == "object" and ($id == "" or (.seen[$id] | not))
    then (if $id == "" then . else .seen[$id] = true end)
      | .n += 1
      | .i += ($u.input_tokens // 0)
      | .o += ($u.output_tokens // 0)
      | .r += ($u.cache_read_input_tokens // 0)
      | .w += ($u.cache_creation_input_tokens // 0)
    else .
    end)
| (if .n == 0 then {i: null, o: null, r: null, w: null} else . end)
| ` + arrayJoin(fields)
	return "\n# What this whole conversation spent, read out of its transcript.\n" +
		"if [ -n \"$transcript\" ] && [ -r \"$transcript\" ]; then\n" +
		indent(readBlock(fields, program, `-n "$transcript"`)) +
		"fi\n"
}

// costTurnBlock is the one value that needs to remember something: the cost of
// the last turn is what the total went up by since this script last ran, so
// the last reading is kept per coder. The rise is kept with it, because the
// line is drawn more often than a turn ends and a rise of nothing would
// otherwise wipe the last turn's cost off the screen a second later. Whole
// millionths of a dollar throughout, so the shell never divides a fraction.
func costTurnBlock(stateDir string) string {
	return `
# The cost of the last turn, against what the run before wrote down.
case $cost_key in
  '' | *[!0-9a-zA-Z-]*) cost_key="" ;;
esac
if [ -n "$cost_key" ] && [ -n "$cost_micro" ]; then
  cost_dir=` + shellQuote(costDir(stateDir)) + `
  mkdir -p "$cost_dir" 2>/dev/null
  cost_file=$cost_dir/$cost_key
  cost_seen=""
  cost_rise=""
  if [ -r "$cost_file" ]; then read -r cost_seen cost_rise < "$cost_file"; fi
  case $cost_seen in
    '' | *[!0-9]*) cost_seen="" ;;
  esac
  case $cost_rise in
    '' | *[!0-9]*) cost_rise="" ;;
  esac
  if [ -n "$cost_seen" ] && [ "$cost_micro" -gt "$cost_seen" ]; then
    cost_rise=$((cost_micro - cost_seen))
  fi
  { printf '%s %s
' "$cost_micro" "${cost_rise:-}" > "$cost_file"; } 2>/dev/null
  if [ -n "$cost_rise" ]; then
    # Rounded to the cent, the way the money values in the payload are, so two
    # amounts on one line are never a rounding apart. The cents are also
    # exactly what a bound compares against, a dollar times a hundred.
    cost_cents=$(((cost_rise + 5000) / 10000))
    printf -v v_cost_turn '$%d.%02d' "$((cost_cents / 100))" "$((cost_cents % 100))"
    n_cost_turn=$cost_cents
  fi
fi
`
}

// readBlock is how a jq program lands in shell variables: one call, the fields
// separated by the unit separator, and a jq that is not installed leaves every
// one of them empty.
func readBlock(fields []jqField, program, input string) string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.name)
	}
	return "if command -v jq >/dev/null 2>&1; then\n" +
		"  IFS=$us read -r " + strings.Join(names, " ") + " < <(jq -r '" + program + "' " + input + " 2>/dev/null)\n" +
		"fi\n"
}

// arrayJoin is the tail of every program here: the fields as one array, joined
// by the separator the read splits on.
func arrayJoin(fields []jqField) string {
	exprs := make([]string, 0, len(fields))
	for _, field := range fields {
		exprs = append(exprs, field.expr)
	}
	return "[" + strings.Join(exprs, ",\n ") + "] | join(\"\\u001f\")"
}

// entryLine is what one entry contributes to the line.
func entryLine(entry Entry) string {
	switch entry.Kind {
	case KindBreak:
		return "brk\n"
	case KindSeparator:
		return "sep \"$(paint '2' " + shellQuote(entry.Text) + ")\"\n"
	case KindValue:
		value, ok := ValueByID(entry.Value)
		if !ok {
			return ""
		}
		color := "''"
		if value.Numeric {
			if bounds := boundArgs(entry.Thresholds); bounds != "" {
				color = `"$(pick "$n_` + value.ID + `" ` + bounds + `)"`
			}
		} else {
			color = shellQuote(ANSI(entry.Color))
		}
		// The free text is the entry's own, so it stands in the line itself
		// rather than in a variable some source filled.
		text := "\"$v_" + value.ID + "\""
		if value.source == fromEntry {
			text = shellQuote(entry.Text)
		}
		return "val \"$(piece " + shellQuote(ANSI(entry.LabelColor)) + " " + shellQuote(entry.Label) +
			" " + color + " " + text + ")\"\n"
	}
	return ""
}

// boundArgs writes the bounds the way pick reads them, the bound times a
// hundred so bash can compare it as an integer.
func boundArgs(thresholds []Threshold) string {
	args := make([]string, 0, len(thresholds))
	for _, t := range thresholds {
		args = append(args, shellQuote(strconv.FormatInt(int64(math.Round(t.At*100)), 10)+":"+ANSI(t.Color)))
	}
	return strings.Join(args, " ")
}

// indent puts a block one step in, so a read that sits inside a guard reads
// like one.
func indent(block string) string {
	lines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "  " + line
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// shellQuote wraps a value so a shell takes it exactly as it stands. Single
// quotes take everything literally, which leaves one character to handle, the
// quote that ends them.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// jqDefs are the renderings every program here shares. A value that is not
// there answers the empty string rather than an error, which is what makes an
// entry fall away instead of taking the line with it.
//
// Three of them carry a rule of their own. txt drops every C0 control character
// and DEL: the fields travel to the shell as one line split on the unit
// separator, so a value carrying a line break or that separator would end the
// read early and hand the values behind it to the wrong names, and an escape
// would reach the terminal as a command rather than as text. money is
// always two decimals, because jq's tostring drops a trailing zero and $1.50
// would otherwise stand as $1.5 next to the last turn's $0.13. And scaled
// rounds where the renderings round, so what a bound is compared against is
// the number that is on the screen: cutting instead would leave $1.499 printed
// as $1.50 and still wearing the color below a bound at 1.50, next to the last
// turn's rise, which is worked out in whole cents and does reach it.
const jqDefs = `def txt: if . == null then "" else (tostring | explode | map(select(. > 31 and . != 127)) | implode) end;
def two: (. * 100 | round) as $c | ($c / 100 | floor) as $d | ($c - $d * 100) as $r | ($d | tostring) + "." + (if $r < 10 then "0" else "" end) + ($r | tostring);
def pct: if . == null then "" else (. * 100 | round / 100 | tostring) + "%" end;
def money: if . == null then "" else "$" + two end;
def cnt: if . == null then "" elif . >= 1000000 then (. / 100000 | round / 10 | tostring) + "M" elif . >= 1000 then (. / 100 | round / 10 | tostring) + "k" else (. | floor | tostring) end;
def scaled: if . == null then "" else (. * 100 | round | tostring) end;
def rate: if . == null then "" else "$" + two + "/h" end;
def secs: if type == "number" then (. / 1000 | floor) else null end;
`

const scriptHead = `#!/bin/bash
# Generated by dev-cockpit, Settings -> Coder -> Status line. Every save writes
# this file again, so an edit here is gone with the next save. The cockpit
# hands it to claude with the session's --settings; the claude settings files
# in your home directory are never written. Switching the status line off on
# that page removes this file again.
#
# Nothing in here fails the whole line: a missing tool or a value nobody
# answered drops its own entry, and stdout carries the status line alone.

# The payload is read with the shell's own read and not with cat: a machine
# whose PATH does not hold cat would otherwise put a "command not found" on
# stderr, and stdout is not the only stream that has to stay clean. read -d ''
# takes everything up to the end of the input and reports that it never saw the
# delimiter, which is the ordinary case and no error.
IFS= read -r -d '' input
us=$'\x1f'

# paint <sgr> <text>
paint() {
  [ -z "$2" ] && return 0
  if [ -z "$1" ]; then printf '%s' "$2"; else printf '\033[%sm%s\033[0m' "$1" "$2"; fi
}

# piece <label sgr> <label> <value sgr> <value>, nothing at all without a value
piece() {
  [ -z "$4" ] && return 0
  if [ -n "$2" ]; then paint "$1" "$2"; printf ' '; fi
  paint "$3" "$4"
}

# pick <value times 100> <bound:sgr>..., the highest bound that is reached wins
pick() {
  local value=$1 code="" bound
  shift
  for bound in "$@"; do
    if [ "$value" -ge "${bound%%:*}" ] 2>/dev/null; then code=${bound#*:}; fi
  done
  printf '%s' "$code"
}

# scale <value var> <bound var>, a plain counter becomes what a bound compares
scale() {
  local raw=${!1} out=""
  case $raw in
    '' | *[!0-9]*) ;;
    *) out=$((raw * 100)) ;;
  esac
  printf -v "$2" '%s' "$out"
}

# secs_until <var>, a moment becomes the seconds left to it. claude writes the
# reset times as plain seconds since the epoch, which date refuses to read as a
# date, so those are taken as they are; anything else is handed to date.
secs_until() {
  local stamp=${!1} target="" out=""
  case $stamp in
    '') ;;
    *[!0-9]*) target=$(date -d "$stamp" +%s 2>/dev/null || echo 0) ;;
    *) target=$stamp ;;
  esac
  if [ -n "$target" ] && [ "$target" -gt 0 ] 2>/dev/null; then
    out=$((target - now))
    [ "$out" -lt 0 ] && out=0
  fi
  printf -v "$1" '%s' "$out"
}

# secs_since <var>, a moment becomes the seconds gone by since
secs_since() {
  local stamp=${!1} out=""
  if [ -n "$stamp" ] && [ "$stamp" -gt 0 ] 2>/dev/null; then
    out=$((now - stamp))
    [ "$out" -lt 0 ] && out=0
  fi
  printf -v "$1" '%s' "$out"
}

# span <value var> <bound var>, seconds become one unit of time, bounds minutes
span() {
  local secs=${!1} text=""
  secs=${secs%%.*}
  case $secs in
    '' | *[!0-9]*) printf -v "$1" '%s' ""; printf -v "$2" '%s' ""; return 0 ;;
  esac
  if [ "$secs" -ge 86400 ]; then text="$((secs / 86400))d"
  elif [ "$secs" -ge 3600 ]; then text="$((secs / 3600))h"
  elif [ "$secs" -ge 60 ]; then text="$((secs / 60))m"
  else text="${secs}s"
  fi
  printf -v "$1" '%s' "$text"
  printf -v "$2" '%s' "$((secs * 100 / 60))"
}

out=""
line=""
pending=""

# val <text>, an entry without a value drops itself and leaves the separator
# in front of it waiting, so no line ever starts or ends with one.
val() {
  [ -z "$1" ] && return 0
  if [ -n "$line" ]; then
    [ -n "$pending" ] && line+=" $pending"
    line+=" "
  fi
  pending=""
  line+="$1"
}

# sep <text>, written only once something follows it
sep() {
  [ -n "$line" ] && pending=$1
  return 0
}

brk() {
  out+="$line"$'\n'
  line=""
  pending=""
}

`

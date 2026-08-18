package statusline

import (
	"math"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"
	"time"
)

// source says where a value comes from, which is what decides what a redraw
// pays for it: the payload on stdin is free and is therefore first choice
// wherever it carries the number at all, git, a command of the user's and the
// transcript cost a process or a read, and the usage API is a network call,
// which is why only what the payload does not know is left on it.
type source int

const (
	fromPayload source = iota
	fromUsage
	fromGitStatus
	fromGitStash
	fromGitLog
	fromTranscript
	fromCostMemory
	fromSystem
	fromEntry
	fromCommand
)

// Value is one thing the line can show. Sample and Number are what the preview
// stands in with, so the bounds can be seen working before a save. TextLabel
// names the field an entry's own text is typed into, for the two values that
// take one, and is empty for every other.
type Value struct {
	ID        string
	Group     string
	Label     string
	Hint      string
	Numeric   bool
	Sample    string
	Number    float64
	TextLabel string

	source source
	read   func(f *facts, entry Entry) (reading, bool)
}

// Groups is the order the select offers the groups in.
var Groups = []string{"Coder", "Context", "Tokens", "Cost", "Limits", "Git", "Place", "System", "Free"}

// Values is what the settings page offers, grouped and in this order. Every
// one of them is something a source really answers: the payload claude writes
// to the renderer's stdin wherever it carries it, git for the repository, the
// machine for itself, and the usage API for the one number the payload does
// not know.
var Values = []Value{
	{
		ID: "model", Group: "Coder", Label: "Model", Hint: "The model this coder runs on, without the note in brackets.",
		Sample: "Opus 5",
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) {
			name, _ := str(field(f.payload, "model", "display_name"))
			name, _, _ = strings.Cut(name, " (")
			return textReading(name)
		},
	},
	{
		ID: "model_id", Group: "Coder", Label: "Model id", Hint: "The model's full identifier.",
		Sample: "claude-opus-5",
		source: fromPayload, read: payloadText("model", "id"),
	},
	{
		ID: "output_style", Group: "Coder", Label: "Output style", Hint: "The output style this coder runs with.",
		Sample: "default",
		source: fromPayload, read: payloadText("output_style", "name"),
	},
	{
		ID: "version", Group: "Coder", Label: "Claude version", Hint: "The version of the claude CLI.",
		Sample: "2.1.233",
		source: fromPayload, read: payloadText("version"),
	},
	{
		ID: "session_id", Group: "Coder", Label: "Coder id", Hint: "The first eight characters of the identifier claude knows this coder by.",
		Sample: "450c6a9d",
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) {
			id, _ := str(field(f.payload, "session_id"))
			if runes := []rune(id); len(runes) > 8 {
				id = string(runes[:8])
			}
			return textReading(id)
		},
	},
	{
		ID: "duration", Group: "Coder", Label: "Running time", Hint: "How long this coder has been running, wall clock. Bounds are in minutes.",
		Numeric: true, Sample: "1h", Number: 65,
		source: fromPayload, read: payloadMillis("cost", "total_duration_ms"),
	},
	{
		ID: "api_duration", Group: "Coder", Label: "API time", Hint: "How long this coder actually waited for the model, which is a fraction of the time it ran. Bounds are in minutes.",
		Numeric: true, Sample: "45s", Number: 0.75,
		source: fromPayload, read: payloadMillis("cost", "total_api_duration_ms"),
	},
	{
		ID: "effort", Group: "Coder", Label: "Effort", Hint: "The reasoning effort the model runs with. Away on a model that has none.",
		Sample: "high",
		source: fromPayload, read: payloadText("effort", "level"),
	},
	{
		// fast_mode and exceeds_200k_tokens below are in the payload but not
		// in claude's own description of it, so they may go without notice;
		// the entry then simply leaves the line.
		ID: "fast", Group: "Coder", Label: "Fast mode", Hint: "Stands there while fast mode is on, the faster and pricier way to run the model, and is away otherwise.",
		Sample: "fast",
		source: fromPayload, read: payloadMark("fast", "fast_mode"),
	},
	{
		ID: "thinking", Group: "Coder", Label: "Thinking", Hint: "Whether extended thinking is on, in words: thinking or no thinking.",
		Sample: "thinking",
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) {
			on, ok := field(f.payload, "thinking", "enabled").(bool)
			if !ok {
				return reading{}, false
			}
			if on {
				return textReading("thinking")
			}
			return textReading("no thinking")
		},
	},

	{
		ID: "context", Group: "Context", Label: "Context used", Hint: "How much of the context window this coder holds.",
		Numeric: true, Sample: "42%", Number: 42,
		source: fromPayload, read: payloadNumber(percent, "context_window", "used_percentage"),
	},
	{
		// claude sends total_input_tokens 0 and used_percentage null before its
		// first measurement, in a fresh session and after /clear, so the count
		// stands only beside a percentage, the way the context entry does.
		ID: "context_tokens", Group: "Context", Label: "Context tokens", Hint: "The tokens in the window right now, fresh input and both cache halves, which is what the percentage above counts.",
		Numeric: true, Sample: "84.2k", Number: 84200,
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) {
			if _, ok := number(field(f.payload, "context_window", "used_percentage")); !ok {
				return reading{}, false
			}
			return readNumber(count, field(f.payload, "context_window", "total_input_tokens"))
		},
	},
	{
		// What is in the window is the input side alone, measured: claude's own
		// used_percentage counts input plus both cache halves and leaves the last
		// answer's output tokens out, and total_input_tokens is exactly that sum,
		// so this is the count the percentage beside it is a percentage of. All
		// three have to be numbers, or what is left is the whole window and the
		// entry would claim an empty one nobody measured.
		ID: "context_left", Group: "Context", Label: "Context left", Hint: "What the window still takes.",
		Numeric: true, Sample: "115.8k", Number: 115800,
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) {
			size, ok1 := number(field(f.payload, "context_window", "context_window_size"))
			used, ok2 := number(field(f.payload, "context_window", "total_input_tokens"))
			_, ok3 := number(field(f.payload, "context_window", "used_percentage"))
			if !ok1 || !ok2 || !ok3 {
				return reading{}, false
			}
			return count(math.Max(size-used, 0))
		},
	},
	{
		ID: "context_size", Group: "Context", Label: "Context size", Hint: "How big the window is.",
		Numeric: true, Sample: "200k", Number: 200000,
		source: fromPayload, read: payloadNumber(count, "context_window", "context_window_size"),
	},
	{
		ID: "over_200k", Group: "Context", Label: "Over 200k", Hint: "Stands there while the conversation holds more than 200k tokens, where the models that charge for a long context take their long context price, and is away below.",
		Sample: ">200k",
		source: fromPayload, read: payloadMark(">200k", "exceeds_200k_tokens"),
	},

	// The four counts and the hit rate are the last request's, which is
	// what the payload carries; the four sums below them are the whole
	// conversation's and are the only thing here that reads the transcript.
	{
		ID: "tokens_input", Group: "Tokens", Label: "Input", Hint: "Input tokens of the last request, cache not counted.",
		Numeric: true, Sample: "2", Number: 2,
		source: fromPayload, read: payloadNumber(count, "context_window", "current_usage", "input_tokens"),
	},
	{
		ID: "tokens_output", Group: "Tokens", Label: "Output", Hint: "Output tokens of the last request.",
		Numeric: true, Sample: "217", Number: 217,
		source: fromPayload, read: payloadNumber(count, "context_window", "current_usage", "output_tokens"),
	},
	{
		ID: "tokens_cache_read", Group: "Tokens", Label: "Cache read", Hint: "Tokens the last request read out of the cache.",
		Numeric: true, Sample: "22.1k", Number: 22100,
		source: fromPayload, read: payloadNumber(count, "context_window", "current_usage", "cache_read_input_tokens"),
	},
	{
		ID: "tokens_cache_write", Group: "Tokens", Label: "Cache creation", Hint: "Tokens the last request wrote into the cache.",
		Numeric: true, Sample: "19.4k", Number: 19400,
		source: fromPayload, read: payloadNumber(count, "context_window", "current_usage", "cache_creation_input_tokens"),
	},
	{
		ID: "cache_hit", Group: "Tokens", Label: "Cache hit rate", Hint: "The share of the last request's input that came out of the cache.",
		Numeric: true, Sample: "53.3%", Number: 53.3,
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) {
			usage, ok := field(f.payload, "context_window", "current_usage").(map[string]any)
			if !ok {
				return reading{}, false
			}
			read := orZero(usage["cache_read_input_tokens"])
			input := orZero(usage["input_tokens"]) + read + orZero(usage["cache_creation_input_tokens"])
			if input <= 0 {
				return reading{}, false
			}
			return percent(read / input * 100)
		},
	},
	{
		ID: "cache_left", Group: "Tokens", Label: "Cache warm for", Hint: "How long the prompt cache stays warm: until then the next request reads the conversation at the cache price, after it the whole conversation is written to the cache again. 0s is a cold cache. Bounds are in minutes. Away before the first answer and while the last one cached nothing.",
		Numeric: true, Sample: "4m", Number: 4,
		source: fromPayload, read: payloadUntil("prompt_cache", "expires_at"),
	},

	{
		ID: "session_input", Group: "Tokens", Label: "Session input", Hint: "Input tokens over every turn of this conversation, cache not counted. Read from the transcript.",
		Numeric: true, Sample: "12.4k", Number: 12400,
		source: fromTranscript, read: sessionSum(func(s tokenSums) float64 { return s.input }),
	},
	{
		ID: "session_output", Group: "Tokens", Label: "Session output", Hint: "Output tokens over every turn of this conversation. Read from the transcript.",
		Numeric: true, Sample: "38.2k", Number: 38200,
		source: fromTranscript, read: sessionSum(func(s tokenSums) float64 { return s.output }),
	},
	{
		ID: "session_cache_read", Group: "Tokens", Label: "Session cache read", Hint: "Cache reads added up over every turn, which is what a whole conversation really reads. Read from the transcript.",
		Numeric: true, Sample: "1.1M", Number: 1100000,
		source: fromTranscript, read: sessionSum(func(s tokenSums) float64 { return s.read }),
	},
	{
		ID: "session_cache_write", Group: "Tokens", Label: "Session cache creation", Hint: "Cache writes added up over every turn. Read from the transcript.",
		Numeric: true, Sample: "420k", Number: 420000,
		source: fromTranscript, read: sessionSum(func(s tokenSums) float64 { return s.write }),
	},

	{
		ID: "cost", Group: "Cost", Label: "Cost", Hint: "What this coder has spent since it started, not what the whole conversation cost.",
		Numeric: true, Sample: "$1.24", Number: 1.24,
		source: fromPayload, read: payloadNumber(money, "cost", "total_cost_usd"),
	},
	{
		ID: "burn", Group: "Cost", Label: "Burn rate", Hint: "What this coder spends per hour, its cost over the time it ran. Away in the first minute, where the number would be invented.",
		Numeric: true, Sample: "$7.49/h", Number: 7.49,
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) {
			ms, ok1 := number(field(f.payload, "cost", "total_duration_ms"))
			usd, ok2 := number(field(f.payload, "cost", "total_cost_usd"))
			if !ok1 || !ok2 || ms < 60000 {
				return reading{}, false
			}
			return rate(usd / (ms / 3600000))
		},
	},
	{
		ID: "cost_turn", Group: "Cost", Label: "Cost, last request", Hint: "What the cost went up by when it last rose. claude draws the line after every request, so inside a turn with tools this is the last request, not the whole turn. Away until there is a second reading to compare.",
		Numeric: true, Sample: "$0.13", Number: 0.13,
		source: fromCostMemory, read: func(f *facts, _ Entry) (reading, bool) {
			if f.costTurn == nil {
				return reading{}, false
			}
			return *f.costTurn, true
		},
	},

	{
		ID: "lines_added", Group: "Cost", Label: "Lines added", Hint: "Lines this coder added since it started.",
		Numeric: true, Sample: "156", Number: 156,
		source: fromPayload, read: payloadNumber(count, "cost", "total_lines_added"),
	},
	{
		ID: "lines_removed", Group: "Cost", Label: "Lines removed", Hint: "Lines this coder removed since it started.",
		Numeric: true, Sample: "23", Number: 23,
		source: fromPayload, read: payloadNumber(count, "cost", "total_lines_removed"),
	},

	{
		ID: "session", Group: "Limits", Label: "Five hour limit", Hint: "How much of the rolling five hour limit is spent.",
		Numeric: true, Sample: "16%", Number: 16,
		source: fromPayload, read: payloadNumber(percent, "rate_limits", "five_hour", "used_percentage"),
	},
	{
		ID: "session_reset", Group: "Limits", Label: "Five hour reset", Hint: "How long the five hour limit still runs. Bounds are in minutes.",
		Numeric: true, Sample: "2h", Number: 120,
		source: fromPayload, read: payloadUntil("rate_limits", "five_hour", "resets_at"),
	},
	{
		ID: "week", Group: "Limits", Label: "Weekly limit", Hint: "The weekly limit over all models.",
		Numeric: true, Sample: "69%", Number: 69,
		source: fromPayload, read: payloadNumber(percent, "rate_limits", "seven_day", "used_percentage"),
	},
	{
		ID: "week_top", Group: "Limits", Label: "Weekly, one model", Hint: "The weekly limit that belongs to one model rather than to all of them. Name the model the way claude's /usage does, Fable for example, or leave it empty for the first one the account carries. The one value the payload does not carry, so this entry alone asks the usage API.",
		Numeric: true, Sample: "82%", Number: 82, TextLabel: "Model",
		source: fromUsage, read: func(f *facts, entry Entry) (reading, bool) {
			used, ok := weeklyScoped(f.usage, f.env.Now.Unix(), entry.Text)
			if !ok {
				return reading{}, false
			}
			return percent(used)
		},
	},
	{
		ID: "reset", Group: "Limits", Label: "Weekly reset", Hint: "How long the weekly limits still run. Bounds are in minutes.",
		Numeric: true, Sample: "2d", Number: 2880,
		source: fromPayload, read: payloadUntil("rate_limits", "seven_day", "resets_at"),
	},

	// Every one of them falls away without git or outside a repository.
	{
		// The branch status names, which exists on a repository without a
		// first commit too; a detached head is on no branch at all.
		ID: "branch", Group: "Git", Label: "Branch", Hint: "The branch the folder is on. Away on a detached head, where there is none.",
		Sample: "master",
		source: fromGitStatus, read: func(f *facts, _ Entry) (reading, bool) {
			if !f.repo.hasSummary || f.repo.summary.Branch.Detached {
				return reading{}, false
			}
			return textReading(f.repo.summary.Branch.Name)
		},
	},
	{
		ID: "git_changes", Group: "Git", Label: "Changed files", Hint: "How many files are changed, every new file counted for itself, a clean tree says zero.",
		Numeric: true, Sample: "3", Number: 3,
		source: fromGitStatus, read: func(f *facts, _ Entry) (reading, bool) {
			if !f.repo.hasSummary {
				return reading{}, false
			}
			return count(float64(f.repo.summary.Changed))
		},
	},
	{
		ID: "git_ahead_behind", Group: "Git", Label: "Ahead and behind", Hint: "How far the branch is from its upstream. Nothing to say when they agree.",
		Sample: "↑2 ↓1",
		source: fromGitStatus, read: func(f *facts, _ Entry) (reading, bool) {
			branch := f.repo.summary.Branch
			if !f.repo.hasSummary || !branch.Counted {
				return reading{}, false
			}
			var parts []string
			if branch.Ahead > 0 {
				parts = append(parts, "↑"+itoa(branch.Ahead))
			}
			if branch.Behind > 0 {
				parts = append(parts, "↓"+itoa(branch.Behind))
			}
			return textReading(strings.Join(parts, " "))
		},
	},
	{
		ID: "git_stashes", Group: "Git", Label: "Stashes", Hint: "How many stashes the repository holds.",
		Numeric: true, Sample: "1", Number: 1,
		source: fromGitStash, read: func(f *facts, _ Entry) (reading, bool) {
			if !f.repo.hasStashes {
				return reading{}, false
			}
			return count(float64(f.repo.stashes))
		},
	},
	{
		ID: "git_age", Group: "Git", Label: "Last commit age", Hint: "How long ago the last commit was made. Bounds are in minutes.",
		Numeric: true, Sample: "20m", Number: 20,
		source: fromGitLog, read: func(f *facts, _ Entry) (reading, bool) {
			if !f.repo.hasLast {
				return reading{}, false
			}
			return since(f.repo.last.Unix(), f.env.Now.Unix())
		},
	},
	{
		ID: "pr", Group: "Git", Label: "Pull request", Hint: "The open pull request of the branch, the one claude's own footer shows, with its review state where it has one. A GitLab merge request reads !N. Away without one.",
		Sample: "#123 approved",
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) {
			n, ok := number(field(f.payload, "pr", "number"))
			if !ok {
				return reading{}, false
			}
			text := "#" + decimal(n)
			if kind, _ := str(field(f.payload, "pr", "kind")); kind == "mr" {
				text = "!" + decimal(n)
			}
			if state, _ := str(field(f.payload, "pr", "review_state")); state != "" {
				text += " " + strings.ReplaceAll(state, "_", " ")
			}
			return textReading(text)
		},
	},

	{
		ID: "dir", Group: "Place", Label: "Folder", Hint: "The name of the folder the coder works in.",
		Sample: "dev-cockpit",
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) {
			dir := f.dir()
			if dir == "" || strings.HasSuffix(dir, "/") {
				return reading{}, false
			}
			return textReading(path.Base(dir))
		},
	},
	{
		ID: "dir_full", Group: "Place", Label: "Folder path", Hint: "The whole path of that folder.",
		Sample: "/root/projects/dev-cockpit",
		source: fromPayload, read: func(f *facts, _ Entry) (reading, bool) { return textReading(f.dir()) },
	},
	{
		ID: "time", Group: "System", Label: "Time", Hint: "The time on this machine.",
		Sample: "14:32",
		source: fromSystem, read: func(f *facts, _ Entry) (reading, bool) { return textReading(f.env.Now.Format("15:04")) },
	},
	{
		ID: "date", Group: "System", Label: "Date", Hint: "The date on this machine.",
		Sample: "2026-08-16",
		source: fromSystem, read: func(f *facts, _ Entry) (reading, bool) { return textReading(f.env.Now.Format("2006-01-02")) },
	},
	{
		ID: "host", Group: "System", Label: "Host", Hint: "The name of this machine.",
		Sample: "eax",
		source: fromSystem, read: func(*facts, Entry) (reading, bool) {
			host, _ := os.Hostname()
			return textReading(host)
		},
	},
	{
		ID: "user", Group: "System", Label: "User", Hint: "The account the coder runs under.",
		Sample: "root",
		source: fromSystem, read: func(*facts, Entry) (reading, bool) {
			if name := os.Getenv("USER"); name != "" {
				return textReading(name)
			}
			if account, err := user.Current(); err == nil {
				return textReading(account.Username)
			}
			return reading{}, false
		},
	},
	// There is deliberately no terminal width here, and the reason is not that
	// there is none to read: claude runs the renderer with its streams on pipes,
	// so there is no terminal to ask, but it puts the width and the height of
	// the one it draws into the environment itself, COLUMNS and LINES, and has
	// done since 2.1.153 (measured against 2.1.234 in a 137 column pane: both
	// arrive). Nothing on this line reads a width, every entry renders the same
	// at any size, so the value would be a number to look at and nothing else.

	{
		ID: FreeTextValue, Group: "Free", Label: "Text", Hint: "Whatever you type, as it stands.",
		Sample: "text", TextLabel: "Text",
		source: fromEntry, read: func(_ *facts, entry Entry) (reading, bool) { return textReading(entry.Text) },
	},
	{
		ID: CommandValue, Group: "Free", Label: "Command", Hint: "The first line a command of yours prints, run on every redraw in the coder's folder with claude's status JSON on stdin. It runs without a shell, so a pipe or a variable needs a script of its own. Away when it fails or takes longer than " + strconv.Itoa(int(commandTimeout/time.Second)) + " seconds.",
		Sample: "output", TextLabel: "Command",
		source: fromCommand, read: func(f *facts, entry Entry) (reading, bool) { return textReading(f.outputs[entry.Text]) },
	},
}

// ValueByID picks a value out of the list.
func ValueByID(id string) (Value, bool) {
	for _, v := range Values {
		if v.ID == id {
			return v, true
		}
	}
	return Value{}, false
}

// ValuesInGroup answers the values of one group, in their order.
func ValuesInGroup(group string) []Value {
	out := []Value{}
	for _, value := range Values {
		if value.Group == group {
			out = append(out, value)
		}
	}
	return out
}

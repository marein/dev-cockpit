package statusline

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/git"
)

// Env is what one redraw runs against besides the payload: the moment it is
// drawn, the home whose claude login the usage call reads, the directory the
// line keeps what it remembers between two redraws in, and where the usage
// API answers. An empty cache directory remembers nothing, which leaves the
// usage and the last request's cost out of the line.
type Env struct {
	Now      time.Time
	Home     string
	CacheDir string
	UsageURL string
}

// facts is what the values of one redraw are read from, each part gathered
// only when an entry of the line asks for it.
type facts struct {
	env      Env
	payload  any
	stdin    []byte
	usage    any
	repo     repoFacts
	sums     tokenSums
	costTurn *reading
	outputs  map[string]string
}

type repoFacts struct {
	summary    git.Summary
	hasSummary bool
	stashes    int
	hasStashes bool
	last       time.Time
	hasLast    bool
}

// dir is the folder the coder works in, the one git and a command are asked
// in.
func (f *facts) dir() string {
	for _, v := range []any{field(f.payload, "workspace", "current_dir"), field(f.payload, "cwd")} {
		if dir, ok := str(v); ok {
			return dir
		}
	}
	return ""
}

// Render draws the line the entries describe for one payload. Nothing fails
// the whole line: a value nobody answers leaves it together with the separator
// in front of it, and a payload that is no JSON at all still leaves standing
// what needs none of it.
func Render(ctx context.Context, entries []Entry, stdin []byte, env Env) string {
	entries = Normalize(entries)
	f := gather(ctx, entries, stdin, env)
	var line lineBuilder
	for _, entry := range entries {
		switch entry.Kind {
		case KindBreak:
			line.lineBreak()
		case KindSeparator:
			line.separator(paint(ANSI("dim"), entry.Text))
		case KindValue:
			value, _ := ValueByID(entry.Value)
			if r, ok := value.read(f, entry); ok {
				line.value(piece(entry, value, r))
			}
		}
	}
	return line.String()
}

// gather reads every source the line uses, and only those: no git process
// without a repository entry, no network call without the one entry the
// payload cannot answer. The sources do not depend on each other, so they run
// side by side and the line waits for the slowest of them, not for their sum.
func gather(ctx context.Context, entries []Entry, stdin []byte, env Env) *facts {
	f := &facts{env: env, stdin: stdin, outputs: map[string]string{}}
	_ = json.Unmarshal(stdin, &f.payload)
	used := map[source]bool{}
	commands := map[string]bool{}
	for _, entry := range entries {
		if entry.Kind != KindValue {
			continue
		}
		value, _ := ValueByID(entry.Value)
		used[value.source] = true
		if value.source == fromCommand && entry.Text != "" {
			commands[entry.Text] = true
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	async := func(work func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			work()
		}()
	}
	if used[fromUsage] {
		async(func() { f.usage = loadUsage(ctx, env) })
	}
	if used[fromGitStatus] || used[fromGitStash] || used[fromGitLog] {
		async(func() { f.repo = readRepo(ctx, f.dir(), used) })
	}
	if used[fromTranscript] {
		async(func() {
			transcript, _ := str(field(f.payload, "transcript_path"))
			f.sums = readSums(transcript)
		})
	}
	if used[fromCostMemory] {
		async(func() { f.costTurn = rememberCost(f.payload, env.CacheDir) })
	}
	for line := range commands {
		async(func() {
			out := runCommand(ctx, line, stdin, f.dir())
			mu.Lock()
			f.outputs[line] = out
			mu.Unlock()
		})
	}
	wg.Wait()
	return f
}

// readRepo asks git what the entries want to know, one call per kind of
// answer.
func readRepo(ctx context.Context, dir string, used map[source]bool) repoFacts {
	if dir == "" {
		dir = "."
	}
	repo := git.New(dir)
	var facts repoFacts
	if used[fromGitStatus] {
		facts.summary, facts.hasSummary = repo.Summary(ctx)
	}
	if used[fromGitStash] {
		facts.stashes, facts.hasStashes = repo.Stashes(ctx)
	}
	if used[fromGitLog] {
		facts.last, facts.hasLast = repo.LastCommit(ctx)
	}
	return facts
}

// piece is one value entry on the line: its label in the label's color, then
// the value in its own, which for a number is the color of the highest bound
// it reaches.
func piece(entry Entry, value Value, r reading) string {
	var b strings.Builder
	if entry.Label != "" {
		b.WriteString(paint(ANSI(entry.LabelColor), entry.Label))
		b.WriteString(" ")
	}
	color := ANSI(entry.Color)
	if value.Numeric {
		color = pick(r.scaled, entry.Thresholds)
	}
	b.WriteString(paint(color, r.text))
	return b.String()
}

// pick answers the color of the highest bound the value reaches. The bounds
// arrive sorted, so the last one that matches is the highest.
func pick(scaled int64, bounds []Threshold) string {
	code := ""
	for _, bound := range bounds {
		if scaled >= int64(math.Round(bound.At*100)) {
			code = ANSI(bound.Color)
		}
	}
	return code
}

func paint(sgr, text string) string {
	if text == "" || sgr == "" {
		return text
	}
	return "\x1b[" + sgr + "m" + text + "\x1b[0m"
}

// lineBuilder puts the line together. A separator waits until something
// follows it, so no line ever starts or ends with one and two never stand in a
// row around a value that is not there.
type lineBuilder struct {
	done    strings.Builder
	line    strings.Builder
	pending string
}

func (b *lineBuilder) value(text string) {
	if text == "" {
		return
	}
	if b.line.Len() > 0 {
		if b.pending != "" {
			b.line.WriteString(" " + b.pending)
		}
		b.line.WriteString(" ")
	}
	b.pending = ""
	b.line.WriteString(text)
}

func (b *lineBuilder) separator(text string) {
	if b.line.Len() > 0 {
		b.pending = text
	}
}

func (b *lineBuilder) lineBreak() {
	b.done.WriteString(b.line.String() + "\n")
	b.line.Reset()
	b.pending = ""
}

func (b *lineBuilder) String() string {
	return b.done.String() + b.line.String() + "\n"
}

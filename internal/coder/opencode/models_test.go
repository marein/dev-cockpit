package opencode

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// verboseModelList is `opencode models --verbose` as 1.18.32 prints it, the
// first block whole and the rest cut to the fields the parser reads plus
// what tests the seams: a model with an input bound below its context, one
// with a context alone, one whose limit names neither, a name without a
// block, a block under a line that is no name, and a block that is no JSON.
const verboseModelList = `opencode/big-pickle
{
  "id": "big-pickle",
  "providerID": "opencode",
  "name": "Big Pickle",
  "family": "big-pickle",
  "api": {
    "id": "big-pickle",
    "url": "https://opencode.ai/zen/v1",
    "npm": "@ai-sdk/openai-compatible"
  },
  "status": "active",
  "headers": {},
  "options": {},
  "cost": {
    "input": 0,
    "output": 0,
    "cache": {
      "read": 0,
      "write": 0
    }
  },
  "limit": {
    "context": 200000,
    "input": 160000,
    "output": 32000
  },
  "capabilities": {
    "temperature": true,
    "reasoning": true,
    "toolcall": true,
    "interleaved": {
      "field": "reasoning_content"
    }
  },
  "release_date": "2025-10-17",
  "variants": {}
}
github-copilot/claude-haiku-4.5
{
  "id": "claude-haiku-4.5",
  "providerID": "github-copilot",
  "limit": {
    "context": 144000,
    "input": 128000,
    "output": 32000
  },
  "variants": {
    "max": {
      "thinking": {
        "budgetTokens": 31999
      }
    },
    "high": {},
    "Not A Level": {}
  }
}
opencode/mimo-v2.6-flash-free
{
  "id": "mimo-v2.6-flash-free",
  "providerID": "opencode",
  "limit": {
    "context": 200000,
    "output": 32000
  }
}
opencode/no-bound
{
  "id": "no-bound",
  "providerID": "opencode",
  "limit": {
    "output": 32768
  }
}
opencode/no-block
WARN some warning text
{
  "id": "orphan",
  "providerID": "opencode",
  "limit": {
    "context": 999999
  }
}
  github-copilot/gpt-5.4-mini  
{
  "id": "gpt-5.4-mini",
  not json at all
}
not a model at all
`

func TestTheModelListReadsTheNamesTheirWindowsAndTheirVariants(t *testing.T) {
	entries := parseModelList(verboseModelList)
	want := []modelEntry{
		{Name: "opencode/big-pickle", Window: 160_000},
		{Name: "github-copilot/claude-haiku-4.5", Window: 128_000, Variants: []string{"high", "max"}},
		{Name: "opencode/mimo-v2.6-flash-free", Window: 200_000},
		{Name: "opencode/no-bound"},
		{Name: "opencode/no-block"},
		{Name: "github-copilot/gpt-5.4-mini"},
	}
	if len(entries) != len(want) {
		t.Fatalf("want %d models, got %v", len(want), entries)
	}
	for i := range want {
		if !reflect.DeepEqual(entries[i], want[i]) {
			t.Fatalf("entry %d: want %+v, got %+v", i, want[i], entries[i])
		}
	}
	if parseModelList("") != nil {
		t.Fatal("want no entries out of no output")
	}
}

// The window is the prompt bound: `limit.input` where the metadata names one,
// which is what opencode's own compaction check measures a turn against and
// what copilot's CLI calls max_prompt_tokens, else `limit.context`, else
// unknown. A zero input reads as none, the way opencode reads it.
func TestTheWindowIsThePromptBound(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  int
	}{
		{"input below the context", `{"limit": {"context": 200000, "input": 160000, "output": 32000}}`, 160_000},
		{"context alone", `{"limit": {"context": 262144, "output": 32768}}`, 262_144},
		{"neither", `{"limit": {"output": 32768}}`, 0},
		{"no limit at all", `{"id": "x"}`, 0},
		{"zero input", `{"limit": {"context": 200000, "input": 0}}`, 200_000},
		{"not json", `{"limit": `, 0},
	}
	for _, c := range cases {
		var entry modelEntry
		if entry.read(c.block); entry.Window != c.want {
			t.Fatalf("%s: want %d, got %d", c.name, c.want, entry.Window)
		}
	}
}

// The plain list, one provider/model per line as 1.18.30 printed it, reads
// the same way with every bound unknown, so a CLI that prints no metadata
// still fills the selects. Everything that is not a name is dropped: a blank
// line, a warning printed on the way, a name no turn could carry.
func TestTheModelListReadsOneNamePerLine(t *testing.T) {
	entries := parseModelList("opencode/big-pickle\n\ngithub-copilot/claude-haiku-4.5\nWARN some warning text\n  github-copilot/gpt-5.4-mini  \nnot a model at all\n")
	var names []string
	for _, entry := range entries {
		if entry.Window != 0 {
			t.Fatalf("want no bound without metadata, got %+v", entry)
		}
		names = append(names, entry.Name)
	}
	if strings.Join(names, ",") != "opencode/big-pickle,github-copilot/claude-haiku-4.5,github-copilot/gpt-5.4-mini" {
		t.Fatalf("want the provider/model lines alone, got %v", names)
	}
}

// The window is read off the same cache the names come from: nothing before
// the first fetch came back, which also starts it, then the bound the
// metadata named, zero for a name the list does not hold and for a model
// whose metadata named none.
func TestTheWindowIsReadOffTheSameList(t *testing.T) {
	list := newModelList(func(context.Context) ([]modelEntry, error) {
		return []modelEntry{{Name: "opencode/big-pickle", Window: 160_000}, {Name: "opencode/no-bound"}}, nil
	})
	start := time.Now()
	if got := list.window("opencode/big-pickle", start); got != 0 {
		t.Fatalf("want nothing while the first fetch runs, got %d", got)
	}
	waitSettled(t, list)
	if got := list.window("opencode/big-pickle", start.Add(time.Minute)); got != 160_000 {
		t.Fatalf("want the bound the metadata named, got %d", got)
	}
	for _, name := range []string{"opencode/no-bound", "opencode/no-such", "big-pickle", ""} {
		if got := list.window(name, start.Add(time.Minute)); got != 0 {
			t.Fatalf("want no window for %q, got %d", name, got)
		}
	}
	c := &Coder{modelCache: list}
	if got := c.modelWindow("opencode/big-pickle"); got != 160_000 {
		t.Fatalf("want the coder to answer out of its cache, got %d", got)
	}
	if got := (&Coder{}).modelWindow("opencode/big-pickle"); got != 0 {
		t.Fatalf("want nothing from a coder without a cache, got %d", got)
	}
}

// The list is fetched in the background and never on the request path: the
// first read answers empty and starts the fetch, a read inside the ten
// minutes answers the cache without a process, a read past them answers the
// cache and starts the refresh, and a refresh that failed keeps what stood.
func TestTheModelListRefreshesInTheBackgroundAndKeepsWhatStood(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	fail := false
	list := newModelList(func(context.Context) ([]modelEntry, error) {
		mu.Lock()
		defer mu.Unlock()
		runs++
		if fail {
			return nil, errors.New("opencode is broken")
		}
		return []modelEntry{{Name: "opencode/big-pickle", Window: 160_000}}, nil
	})
	settled := func() {
		t.Helper()
		waitSettled(t, list)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return runs
	}

	start := time.Now()
	if got := entryNames(list.entries(start)); len(got) != 0 {
		t.Fatalf("want the first read to answer nothing while the fetch runs, got %v", got)
	}
	settled()
	if count() != 1 {
		t.Fatalf("want the first read to start one fetch, got %d", count())
	}
	if got := entryNames(list.entries(start.Add(time.Minute))); strings.Join(got, ",") != "opencode/big-pickle" || count() != 1 {
		t.Fatalf("want the cache answered without a process inside the ten minutes, got %v after %d runs", got, count())
	}

	mu.Lock()
	fail = true
	mu.Unlock()
	if got := entryNames(list.entries(list.at.Add(modelListTTL))); strings.Join(got, ",") != "opencode/big-pickle" {
		t.Fatalf("want the stale cache answered while the refresh runs, got %v", got)
	}
	settled()
	if count() != 2 {
		t.Fatalf("want the read past the ten minutes to start a refresh, got %d runs", count())
	}
	if got := entryNames(list.entries(list.at.Add(time.Minute))); strings.Join(got, ",") != "opencode/big-pickle" || count() != 2 {
		t.Fatalf("want a failed refresh to keep what stood and wait, got %v after %d runs", got, count())
	}
}

// waitSettled waits for the refresh a read started to come back, which every
// refresh does: a hung one is ended by its deadline.
func waitSettled(t *testing.T, list *modelList) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list.mu.Lock()
		done := !list.refreshing
		list.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the refresh never came back")
}

// A refresh that hangs is ended by its deadline and read as a failed one: the
// refreshing flag is cleared, what stood is kept, and the read past the TTL
// starts the next try instead of serving the stale list for the life of the
// process. The fetch itself is what the deadline reaches, through its context.
func TestAHungModelListRefreshIsEndedByItsDeadline(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	ended := 0
	list := newModelList(func(ctx context.Context) ([]modelEntry, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		ended++
		mu.Unlock()
		return nil, ctx.Err()
	})
	list.timeout = 50 * time.Millisecond
	list.cached = []modelEntry{{Name: "opencode/big-pickle"}}
	count := func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return runs, ended
	}

	start := time.Now()
	if got := entryNames(list.entries(start)); strings.Join(got, ",") != "opencode/big-pickle" {
		t.Fatalf("want what stood answered while the refresh runs, got %v", got)
	}
	waitSettled(t, list)
	if runs, ended := count(); runs != 1 || ended != 1 {
		t.Fatalf("want the one fetch started and ended by its context, got %d started and %d ended", runs, ended)
	}
	if got := entryNames(list.entries(list.at.Add(time.Minute))); strings.Join(got, ",") != "opencode/big-pickle" {
		t.Fatalf("want a failed refresh to keep what stood, got %v", got)
	}
	if runs, _ := count(); runs != 1 {
		t.Fatalf("want a read inside the TTL to start nothing, got %d runs", runs)
	}
	list.entries(list.at.Add(modelListTTL))
	waitSettled(t, list)
	if runs, ended := count(); runs != 2 || ended != 2 {
		t.Fatalf("want the read past the TTL to try again, got %d started and %d ended", runs, ended)
	}
}

// The real fetch runs `opencode models` under the context: a process that
// never ends is ended at the deadline and answers as a failure, never as a
// list, and the refresh that ran it comes back.
func TestListModelsEndsTheProcessAtTheDeadline(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep on this host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	names, err := listModelsWith(ctx, "sleep", "30")
	if err == nil || names != nil {
		t.Fatalf("want a failure and no list from a hung process, got %v and %v", names, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the deadline did not end the process, the call took %s", time.Since(start))
	}
	if !strings.Contains(err.Error(), "did not answer in time") {
		t.Fatalf("want the deadline named, got %v", err)
	}
}

// The warm up at the serve start is the coder's list capability alone: a coder
// with no conversation probe at all starts its first fetch when asked, so the
// New coder dialog of a host whose opencode cannot hold a conversation opens
// filled the same way.
func TestWarmingTheModelsHangsOnNoOtherCapability(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	c := &Coder{modelCache: newModelList(func(context.Context) ([]modelEntry, error) {
		mu.Lock()
		defer mu.Unlock()
		runs++
		return []modelEntry{{Name: "opencode/big-pickle"}}, nil
	})}
	c.models = coder.NewModelRepository(nil, "opencode", opencodeModelsNote, c.cliModels)
	coder.WarmModels(c)
	waitSettled(t, c.modelCache)
	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Fatalf("want the warm up to start the first fetch, got %d runs", runs)
	}
}

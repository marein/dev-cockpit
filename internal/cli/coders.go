package cli

import (
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
	coderclaude "github.com/marein/dev-cockpit/internal/coder/claude"
	codercodex "github.com/marein/dev-cockpit/internal/coder/codex"
	codercopilot "github.com/marein/dev-cockpit/internal/coder/copilot"
	coderopencode "github.com/marein/dev-cockpit/internal/coder/opencode"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/ollama"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/tmux"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// removeManagedSkills takes the cockpit's own skills off the disk on the way
// out, the counterpart of the start's EnsureManagedSkills. The skill points a
// coder at the local API socket of a running instance, so one left behind
// after the stop would send every coder down a path that cannot answer. It
// runs before the language servers are closed, because it is a few file
// removals while that close is bounded but not instant, and a second signal
// may end the process during it.
//
// It is deliberately not the only thing keeping the disk clean: a SIGKILL and
// the self-update's exec both walk past this, and both are covered by the
// start rewriting the skill anyway.
//
// The state directory says which skill is this instance's. A coder home is
// shared by every cockpit on the machine, and a throwaway stopping next to the
// real instance may not take the running instance's skill with it.
func removeManagedSkills(coders []coder.Coder, instance coder.CockpitInstance) {
	for _, c := range coders {
		if err := coder.RemoveManagedSkills(c.SkillRepository(), instance); err != nil {
			log.Printf("coder %s: the cockpit git skill could not be removed: %v", c.ID(), err)
		}
	}
}

// cockpitServesFrom answers whether some cockpit is still listening on the
// local API socket of a state directory. It is what keeps a second instance
// from taking the managed skill away from the one that is running, and it is a
// single connect with no retry on purpose: the answer is wanted at start, and
// localapi.Dial waits out a budget for a cockpit that may still be coming up,
// which is the opposite question.
func cockpitServesFrom(stateDir string) bool {
	conn, err := net.DialTimeout("unix", localapi.SocketPath(stateDir), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// knownCoders builds every coder the cockpit knows, installed or not, wired
// with what serve holds. A reading of what a coder can do takes them bare.
func knownCoders(stateDir string, store *settings.Store, launcher *ollama.Client) []coder.Coder {
	return []coder.Coder{
		codercopilot.New(store),
		coderclaude.New(stateDir, notify.InboxDir(stateDir, "claude"), store, launcher),
		coderopencode.New(notify.InboxDir(stateDir, "opencode"), store),
		codercodex.New(store),
	}
}

// agentUsage is the --agent line of coder-new, naming the coders that start
// a session as one of their agents by what the new coder form reads too.
func agentUsage(coders []coder.Coder) string {
	var ids []string
	for _, c := range coders {
		if coder.NoAgentStartNote(c) == "" {
			ids = append(ids, c.ID())
		}
	}
	sort.Strings(ids)
	usage := "agent the session starts with"
	switch n := len(ids); {
	case n > 1:
		return usage + ", for " + strings.Join(ids[:n-1], ", ") + " and " + ids[n-1]
	case n == 1:
		return usage + ", for " + ids[0]
	}
	return usage
}

// selectProviders resolves which coders this instance serves: every registered
// coder whose CLI is installed.
func selectProviders(registry *coder.Registry) ([]coder.Coder, error) {
	if missing := clirun.MissingTools(tmux.RequiredTools); len(missing) > 0 {
		return nil, fmt.Errorf("missing CLI tools: %v", missing)
	}
	var selected []coder.Coder
	for _, p := range registry.All() {
		if missing := clirun.MissingTools(p.RequiredTools()); len(missing) > 0 {
			log.Printf("coder %s disabled, missing CLI tools: %v", p.ID(), missing)
			continue
		}
		selected = append(selected, p)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no coder CLI found (looked for: %s)", strings.Join(registry.IDs(), ", "))
	}
	return selected, nil
}

// assistantCoders adapts the installed coders to what the assistant needs,
// keeping internal/assistant free of any dependency on internal/coder.
// assistantCoders hands the assistant the coders that can answer a turn, each
// with the reading of its stored model defaults, the purpose fallback behind
// an assistant's own pick, read fresh on every turn out of the settings store.
type assistantCoders struct {
	coders []coder.Coder
	store  *settings.Store
}

func (c assistantCoders) Available() []assistant.CoderInfo {
	out := make([]assistant.CoderInfo, 0, len(c.coders))
	for _, co := range c.coders {
		runner := coder.AssistantRunnerFor(co)
		if runner == nil {
			continue
		}
		id := co.ID()
		out = append(out, assistant.CoderInfo{ID: id, Label: render.CoderLabel(id), Runner: runner, Defaults: func() assistant.ModelDefaults {
			return assistant.ModelDefaultsFor(c.store, id)
		}})
	}
	return out
}

// isStrayCheckSession decides what the startup sweep deletes. The name alone is
// user text, somebody may call a coder "cockpit check: whatever", and deleting
// it would take that coder's transcript with it. A check always runs in the
// workspace of the assistant whose job it is, so both have to hold.
func isStrayCheckSession(session coder.Session, ownWorkdir func(string) bool) bool {
	return assistant.IsCheckSession(session.Name) && ownWorkdir(session.CWD)
}

// coderSessions lets a check ask the coder of a job what that session last did.
// Which coder answers comes from the job, and how it answers is the coder's
// business: one that keeps a transcript reads it, one that keeps none falls
// back to the terminal picture. A job whose coder id is not among the installed
// ones (an old job, a coder that was removed) is offered to each of them, and
// the one that owns the session answers.
type coderSessions struct{ coders []*coder.Manager }

// toAssistantActivity maps the coder reading onto the assistant's own twin
// type, field by field on purpose: the two packages stay uncoupled, and the
// coder side may grow fields (the tool call phase) the job checks never read.
func toAssistantActivity(a coder.Activity) assistant.Activity {
	return assistant.Activity{Text: a.Text, Finished: a.Finished, Screen: a.Screen}
}

func (c coderSessions) Activity(coderID, terminal string) (assistant.Activity, error) {
	if coderID != "" {
		for _, m := range c.coders {
			if m.ID() == coderID {
				activity, err := m.Activity(terminal, 0, coder.ActivityBudget)
				return toAssistantActivity(activity), err
			}
		}
	}
	// No coder of that name is installed here, or the job never carried one:
	// ask them all, the one that owns the session answers. A job outlives the
	// set of installed coders, and answering "nobody answers" would give the
	// check a verdict about an error message instead of about the session.
	var lastErr error
	for _, m := range c.coders {
		activity, err := m.Activity(terminal, 0, coder.ActivityBudget)
		if err == nil {
			return toAssistantActivity(activity), nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("No coder answers for %q.", terminal)
	}
	return assistant.Activity{}, lastErr
}

// Running reports whether the job's terminal is still a live session. A job
// asks before it spends anything, because a terminal that is gone is not a
// question for a coder, it is a fact the cockpit already holds.
func (c coderSessions) Running(coderID, terminal string) bool {
	for _, m := range c.coders {
		if coderID != "" && m.ID() != coderID {
			continue
		}
		if _, err := m.ResolveRunning(terminal); err == nil {
			return true
		}
	}
	return false
}

// sweepCheckSessions drops the provider sessions of checks that a killed
// process left behind. A check reserves its session for as long as it runs, and
// the reservation lives in that process, so a session it never got to drop
// becomes a resumable ghost coder at the next start. Nothing running answers to
// these names, a live check is always reserved and never listed. ownWorkdir
// says whether a directory is an assistant's workspace, the place every check
// runs in.
func sweepCheckSessions(coders []*coder.Manager, ownWorkdir func(string) bool) {
	for _, m := range coders {
		// A check that outlived the restart reserved its session again a moment
		// ago, and a cached snapshot from before that would offer it up here.
		m.Invalidate()
		for _, session := range m.Snapshot().Resumable {
			if !isStrayCheckSession(session, ownWorkdir) {
				continue
			}
			if _, err := m.DeleteResumable(session.SessionID); err != nil {
				log.Printf("assistant: a check session of %s stayed behind: %v", m.ID(), err)
				continue
			}
			log.Printf("assistant: dropped the check session %s a restart left behind", session.SessionID)
		}
		m.Invalidate()
	}
}

package cli

import (
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/cost"
	costclaude "github.com/marein/dev-cockpit/internal/cost/claude"
	"github.com/marein/dev-cockpit/internal/cost/price"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/spf13/cobra"
)

// costPollInterval is how often the cost books read what the CLIs appended,
// short enough that a new call shows within seconds, long enough to cost
// nothing.
const costPollInterval = 10 * time.Second

// costSources are the coder CLIs whose spend is read. Every source reads its
// CLI's own records under the home directory, whether or not the CLI is
// installed right now: a record left behind was still spent.
func costSources(prices *price.Book, places func() []string, keepMonths func() int) []cost.Source {
	home, err := filesystem.HomeDir()
	if err != nil {
		return nil
	}
	return []cost.Source{costclaude.NewSource(filepath.Join(home, ".claude", "projects"), prices.Table, places, keepMonths)}
}

// costPlaces are the directories a session may have run in: every project,
// every assistant's workspace, and the same two with a wildcard for the ones
// that are gone, so a record without a directory of its own still finds the
// project it was spent on.
func costPlaces(conversations *assistant.Service, workspace *assistant.Workspace, projects *project.Repository) func() []string {
	return func() []string {
		places := projects.SelectablePaths()
		for _, a := range conversations.List() {
			places = append(places, workspace.Dir(a.ID))
		}
		if root, err := projects.EnsureRoot(); err == nil {
			places = append(places, filepath.Join(root, "*"))
		}
		return append(places, workspace.Dir("*"))
	}
}

// watchSessionDeletes books what a session spent last right before its
// records go, on every coder that announces a delete.
func watchSessionDeletes(coders []coder.Coder, costs *cost.Service) {
	for _, c := range coders {
		watcher, ok := c.(coder.SessionDeleteWatcher)
		if !ok {
			continue
		}
		coderID := c.ID()
		watcher.BeforeSessionDelete(func(sessionID string) {
			if err := costs.BookSession(coderID, sessionID); err != nil {
				log.Printf("cost: %v", err)
			}
		})
	}
}

// costPlacer reads a session's directory for the cost books: an assistant's
// workspace, gone or not, else the project it lies in. Both answers read the
// path alone.
func costPlacer(workspace *assistant.Workspace, projects *project.Repository) cost.Placer {
	head, tail, _ := strings.Cut(workspace.Dir("*"), "*")
	return func(cwd string) (string, string) {
		if workspace.IsWorkdir(cwd) {
			dir, _ := filepath.Abs(strings.TrimSpace(cwd))
			return strings.TrimSuffix(strings.TrimPrefix(dir, head), tail), ""
		}
		return "", projects.ProjectNameFor(cwd)
	}
}

// costOwners lists for the cost books what the cockpit runs right now:
// every coder session, running or stored, and every assistant's name.
func costOwners(coders []*coder.Manager, conversations *assistant.Service) func() cost.Owners {
	return func() cost.Owners {
		owners := cost.Owners{Assistants: map[string]string{}}
		for _, m := range coders {
			snap := m.Snapshot()
			for _, r := range snap.Running {
				owners.Coders = append(owners.Coders, cost.CoderSession{Coder: m.ID(), Session: r.Identifier, Name: r.Name, CWD: r.CWD})
			}
			for _, st := range snap.Resumable {
				owners.Coders = append(owners.Coders, cost.CoderSession{Coder: m.ID(), Session: st.SessionID, Name: st.Name, CWD: st.CWD})
			}
		}
		for _, a := range conversations.List() {
			owners.Assistants[a.ID] = a.Title
		}
		return owners
	}
}

// costsPath answers the cost summary to a local caller as JSON.
const costsPath = "/costs"

func newCostShowCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "cost-show",
		Short: "Show what the coders spent today, this week and this month",
		Long: "Show the coders' spend at API list price, as their CLIs report it: today, " +
			"this week, this month and the projects that spent most this month, " +
			"the assistants as one group split by assistant, each with its turns, checks and triggers. " +
			"Only claude reports spend so far. " +
			"Reads only, changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCostShow(cmd.OutOrStdout(), *opts)
		},
	}
}

func runCostShow(out io.Writer, opts inspectOptions) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.GetJSON(costsPath+"?range="+cost.RangeMonth, inputTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, costLines(answer))
	return err
}

// maxCostProjects is how many projects the summary names.
const maxCostProjects = 5

func costLines(answer map[string]any) string {
	var b strings.Builder
	b.WriteString("Spend at API list price.\n")
	fmt.Fprintf(&b, "Today %s, this week %s, this month %s.\n",
		usd(answer["today"]), usd(answer["week"]), usd(answer["month"]))
	projects, _ := answer["projects"].([]any)
	if len(projects) == 0 {
		b.WriteString("Nothing booked this month.\n")
		return b.String()
	}
	b.WriteString("Top projects this month:\n")
	for i, p := range projects {
		if i == maxCostProjects {
			fmt.Fprintf(&b, "  and %d more\n", len(projects)-maxCostProjects)
			break
		}
		entry, _ := p.(map[string]any)
		if assistants, ok := entry["assistants"].([]any); ok {
			fmt.Fprintf(&b, "  Assistants %s\n", usd(entry["usd"]))
			for _, a := range assistants {
				named, _ := a.(map[string]any)
				name := word(named["name"])
				if name == "" {
					name = "(unnamed)"
				}
				fmt.Fprintf(&b, "    %s %s\n", name, usd(named["usd"]))
			}
			continue
		}
		name := word(entry["project"])
		if name == "" {
			name = "(no project)"
		}
		fmt.Fprintf(&b, "  %s %s\n", name, usd(entry["usd"]))
	}
	if n := count(answer["unpriced"]); n > 0 {
		fmt.Fprintf(&b, "%d sessions ran a model without a list price, their tokens are counted, no spend.\n", n)
	}
	return b.String()
}

func usd(value any) string {
	n, _ := value.(float64)
	return fmt.Sprintf("$%.2f", n)
}

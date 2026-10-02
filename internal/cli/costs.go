package cli

import (
	"fmt"
	"io"
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
// short enough for a burn rate per minute, long enough to cost nothing.
const costPollInterval = 10 * time.Second

// costSources are the coder CLIs whose spend is read. Every source reads its
// CLI's own records under the home directory, whether or not the CLI is
// installed right now: a record left behind was still spent.
func costSources(prices *price.Book, places func() []string) []cost.Source {
	home, err := filesystem.HomeDir()
	if err != nil {
		return nil
	}
	return []cost.Source{costclaude.NewSource(filepath.Join(home, ".claude", "projects"), prices.Table, places)}
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

// watchSessionDeletes hands every coder that can announce a session delete
// the cost books' capture: what the session wrote last is read before its
// record goes and booked after.
func watchSessionDeletes(coders []coder.Coder, costs *cost.Service) {
	for _, c := range coders {
		watcher, ok := c.(coder.SessionDeleteWatcher)
		if !ok {
			continue
		}
		coderID := c.ID()
		watcher.BeforeSessionDelete(func(sessionID string) { costs.SessionDeleting(coderID, sessionID) })
	}
}

// costPlacer reads a session's directory for the cost books: an assistant's
// workspace, gone or not, else the project it lies in. Both answers read the
// path alone.
func costPlacer(workspace *assistant.Workspace, projects *project.Repository) cost.Placer {
	return func(cwd string) (string, string) {
		if id, ok := workspace.InstanceOf(cwd); ok {
			return id, ""
		}
		return "", projects.ProjectNameFor(cwd)
	}
}

// seedCostOwners tells the cost books every assistant there is when the
// server starts. From then on each name comes with its event, and every coder
// comes from the turn watch.
func seedCostOwners(conversations *assistant.Service, costs *cost.Service) {
	for _, a := range conversations.List() {
		costs.AssistantNamed(a.ID, a.Title)
	}
}

// costsPath answers the cost summary to a local caller as JSON.
const costsPath = "/costs"

func newCostShowCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "cost-show",
		Short: "Show what the coders spent today, this week and this month",
		Long: "Show the coders' spend as the API list price equivalent their CLIs report: today, " +
			"this week, this month, the last hour and the projects that spent most this month, " +
			"the assistants as one group split by assistant, each with its turns, checks and triggers. " +
			"A subscription user does not pay these amounts. Only claude reports spend so far. " +
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
	b.WriteString("Spend at API list price, an equivalent: a subscription does not pay it.\n")
	fmt.Fprintf(&b, "Today %s, this week %s, this month %s, last hour %s.\n",
		usd(answer["today"]), usd(answer["week"]), usd(answer["month"]), usd(answer["burn"]))
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

package cli

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/sshagent"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// newSSHAgent builds the cockpit's ssh agent over the user's ~/.ssh and hands
// every shell and coder started from now on its socket. SSH_AUTH_SOCK is read
// once, as the process found it: set, the host's agent wins and every session
// inherits it as before. The sockets of the sessions a previous process
// started are listened on again before the first session starts here.
func newSSHAgent(stateDir string, store *settings.Store, tmuxClient *tmux.Client, coders []*coder.Manager, shells *shell.Shells, projects *project.Repository) *sshagent.Service {
	sshDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		sshDir = filepath.Join(home, ".ssh")
	} else {
		log.Printf("ssh agent: no home directory, no keys: %v", err)
	}
	agent := sshagent.New(stateDir, sshDir, os.Getenv("SSH_AUTH_SOCK"), store.Get)
	agent.SetResolver(agentCallers(tmuxClient, coders, projects))
	for _, m := range coders {
		m.SetAgent(func() (map[string]string, string) { return agent.OpenSession(sshagent.KindCoder) })
	}
	shells.SetAgent(func() (map[string]string, string) { return agent.OpenSession(sshagent.KindShell) })
	if err := agent.Start(agentTags(tmuxClient)); err != nil {
		log.Printf("ssh agent: %v", err)
	}
	return agent
}

// agentRound is how often the agent forgets the keys whose time is up and
// follows the sessions that came and went.
const agentRound = 5 * time.Second

// agentTags lists the agent tags of the live sessions.
func agentTags(tmuxClient *tmux.Client) func() ([]string, error) {
	return func() ([]string, error) {
		panes, err := tmuxClient.ListPanes()
		if err != nil {
			return nil, err
		}
		tags := make([]string, 0, len(panes))
		for _, p := range panes {
			if p.SSHAgent != "" {
				tags = append(tags, p.SSHAgent)
			}
		}
		return tags, nil
	}
}

// agentCallers names the session behind a tag the way its tab does, read when
// it asks, so a rename since the start shows.
func agentCallers(tmuxClient *tmux.Client, coders []*coder.Manager, projects *project.Repository) func(tag string) (sshagent.Caller, bool) {
	return func(tag string) (sshagent.Caller, bool) {
		panes, err := tmuxClient.ListPanes()
		if err != nil {
			return sshagent.Caller{}, false
		}
		for _, p := range panes {
			if p.SSHAgent != tag {
				continue
			}
			if name := strings.TrimSpace(p.ShellName); name != "" {
				return sshagent.Caller{Name: name, Project: projects.ProjectNameFor(p.Workdir)}, true
			}
			for _, m := range coders {
				for _, r := range m.Snapshot().Running {
					if r.Identifier == p.Name {
						return sshagent.Caller{Name: r.Name, Project: projects.ProjectNameFor(r.CWD)}, true
					}
				}
			}
			return sshagent.Caller{Name: p.CoderName, Project: projects.ProjectNameFor(p.CoderDir)}, true
		}
		return sshagent.Caller{}, false
	}
}

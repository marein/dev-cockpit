package cli

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/sshagent"
)

// newSSHAgent builds the cockpit's ssh agents over the user's ~/.ssh and hands
// every shell, coder and assistant turn started from now on the socket of its
// kind. SSH_AUTH_SOCK is read once, as the process found it: set, the host's
// agent wins and every session and turn inherits it as before, and without
// OpenSSH's ssh-agent and ssh-add nothing changes either. The sockets listen
// before the first session starts here, at the paths the sessions of a
// previous process still carry.
func newSSHAgent(stateDir string, store *settings.Store, coders []*coder.Manager, shells *shell.Shells, assistants *assistant.Service) *sshagent.Service {
	sshDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		sshDir = filepath.Join(home, ".ssh")
	} else {
		log.Printf("ssh agent: no home directory, no keys: %v", err)
	}
	inherited := os.Getenv("SSH_AUTH_SOCK")
	agent := sshagent.New(stateDir, sshDir, inherited, store.Get)
	if inherited != "" && agent.External() == "" {
		// A cockpit started from one of its own terminals inherits its own
		// socket, and every git call that names none would sign through it.
		_ = os.Unsetenv("SSH_AUTH_SOCK")
	}
	agent.Start()
	if sock := agent.Socket(sshagent.KindCoder); sock != "" {
		for _, m := range coders {
			m.SetAgent(map[string]string{"SSH_AUTH_SOCK": sock})
		}
	}
	if sock := agent.Socket(sshagent.KindShell); sock != "" {
		shells.SetAgent(map[string]string{"SSH_AUTH_SOCK": sock})
	}
	if sock := agent.Socket(sshagent.KindAssistant); sock != "" {
		assistants.SetAgent([]string{"SSH_AUTH_SOCK=" + sock})
	}
	return agent
}

// agentRound is how often the agents let go of the keys whose time is up.
const agentRound = 5 * time.Second

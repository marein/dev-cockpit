package terminal

import "github.com/marein/dev-cockpit/internal/tmux"

// AgentOption is the tmux user option that carries the tag of a session's
// socket of the cockpit's ssh agent. It outlives a rename of the session and
// a restart of the cockpit, which is how a new process finds the socket the
// session's environment still names.
const AgentOption = "@dc_ssh_agent"

// SessionAgent opens the agent socket of a session about to start, and
// answers the environment to start it with and the tag to mark it with.
type SessionAgent func() (env map[string]string, tag string)

// WithAgent answers a copy of env with the agent's environment added, and
// the tag to mark the session with. A nil agent adds nothing.
func WithAgent(env map[string]string, open SessionAgent) (map[string]string, string) {
	out := make(map[string]string, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	if open == nil {
		return out, ""
	}
	extra, tag := open()
	for k, v := range extra {
		out[k] = v
	}
	return out, tag
}

// TagAgent marks a started session with its agent socket's tag.
func TagAgent(t *tmux.Client, session, tag string) error {
	if tag == "" {
		return nil
	}
	return t.SetOption(session, AgentOption, tag)
}

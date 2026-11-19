package terminal

import "maps"

// WithAgent answers a copy of env with the environment of the cockpit's ssh
// agent added, which points a session at its kind's socket. An empty agent
// adds nothing.
func WithAgent(env, agent map[string]string) map[string]string {
	out := make(map[string]string, len(env)+len(agent))
	maps.Copy(out, env)
	maps.Copy(out, agent)
	return out
}

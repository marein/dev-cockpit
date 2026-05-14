package provider

import "strings"

// SessionRuntime builds provider-specific start and resume commands.
type SessionRuntime interface {
	UsesProvidedSessionID() bool
	StartCommand(sessionID, sessionName, workdir, agentID string, remoteControl, automaticApproval bool) string
	ResumeCommand(sessionID, workdir string, remoteControl, automaticApproval bool) string
	Env() map[string]string
}

const ProviderEnvVar = "DEV_COCKPIT_PROVIDER"

func OwnsProcess(env map[string]string, providerID string) bool {
	return env[ProviderEnvVar] == providerID
}

func RunningName(cmdline []string) string {
	return flagValue(cmdline, "--name")
}

func flagValue(cmdline []string, name string) string {
	prefix := name + "="
	for i, arg := range cmdline {
		switch {
		case arg == name && i+1 < len(cmdline):
			return strings.TrimSpace(cmdline[i+1])
		case strings.HasPrefix(arg, prefix):
			return strings.TrimSpace(strings.TrimPrefix(arg, prefix))
		}
	}
	return ""
}

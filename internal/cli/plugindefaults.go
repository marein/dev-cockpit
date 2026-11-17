package cli

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/pluginhost"
)

// newAssistant builds the assistant, writes the plugins' memory files into its
// memory directory and rebuilds the instruction files of every assistant.
func newAssistant(stateDir string, coders assistant.Coders, cockpit assistant.Cockpit, serves []*pluginhost.Serve) (*assistant.Service, *assistant.Workspace, error) {
	conversations, workspace, err := assistant.New(stateDir, coders, cockpit)
	if err != nil {
		return nil, nil, err
	}
	applyPluginMemory(stateDir, workspace, serves)
	return conversations, workspace, nil
}

// applyPluginMemory writes the plugins' memory files and rebuilds every
// assistant's instruction files over them.
func applyPluginMemory(stateDir string, workspace *assistant.Workspace, serves []*pluginhost.Serve) {
	_, _, memory := assistant.Paths(stateDir)
	pluginhost.ApplyAssistantMemory(serves, memory, assistant.IsMemoryFile, assistant.IsMemoryTooLong)
	if err := workspace.Sync(); err != nil {
		log.Printf("assistant: the instruction files were not rebuilt after the plugin memory: %v", err)
	}
}

// pluginCheckEveryEnv sets a dev build's plugin update check interval, a Go
// duration of at least a second, so a test instance on a stub feed sees an
// automatic update without waiting hours.
const pluginCheckEveryEnv = "DEV_COCKPIT_PLUGIN_CHECK_EVERY"

// devPluginAPI is the stub a dev build sends every plugin release request
// to, "" for a release build.
func devPluginAPI() string {
	if version != "dev" {
		return ""
	}
	return strings.TrimRight(os.Getenv(pluginhost.APIURLEnv), "/")
}

// devPluginCheckEvery is a dev build's plugin update check interval, 0 for
// the default.
func devPluginCheckEvery() time.Duration {
	if version != "dev" {
		return 0
	}
	every, err := time.ParseDuration(os.Getenv(pluginCheckEveryEnv))
	if err != nil || every < time.Second {
		return 0
	}
	return every
}

package cli

import (
	"log"

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
	_, _, memory := assistant.Paths(stateDir)
	pluginhost.ApplyAssistantMemory(serves, memory, assistant.IsMemoryFile, assistant.IsMemoryTooLong)
	if err := workspace.Sync(); err != nil {
		log.Printf("assistant: the instruction files were not rebuilt after the plugin memory: %v", err)
	}
	return conversations, workspace, nil
}

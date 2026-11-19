package pluginhost

import (
	"context"
	"fmt"
	"sync"

	"github.com/marein/dev-cockpit/plugin"
)

// Assistants is the cockpit's assistant surface as every plugin reaches it.
// The assistants start after the plugins configured at serve start, so the
// surface is bound once they stand and answers ErrAssistantsNotReady before.
type Assistants struct {
	mu sync.RWMutex
	to plugin.Assistants
}

// Bind hands every plugin the surface to delegate to.
func (a *Assistants) Bind(to plugin.Assistants) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.to = to
}

func (a *Assistants) bound() (plugin.Assistants, error) {
	if a == nil {
		return nil, plugin.ErrAssistantsNotReady
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.to == nil {
		return nil, plugin.ErrAssistantsNotReady
	}
	return a.to, nil
}

func (a *Assistants) Create(ctx context.Context, in plugin.AssistantSpec) (plugin.Assistant, error) {
	to, err := a.bound()
	if err != nil {
		return plugin.Assistant{}, err
	}
	return to.Create(ctx, in)
}

func (a *Assistants) List(ctx context.Context, in plugin.AssistantQuery) (plugin.AssistantList, error) {
	to, err := a.bound()
	if err != nil {
		return plugin.AssistantList{}, err
	}
	return to.List(ctx, in)
}

func (a *Assistants) LastMessage(ctx context.Context, in plugin.AssistantMessageQuery) (plugin.AssistantMessage, error) {
	to, err := a.bound()
	if err != nil {
		return plugin.AssistantMessage{}, err
	}
	return to.LastMessage(ctx, in)
}

func (a *Assistants) Coders(ctx context.Context, in plugin.AssistantCoderQuery) (plugin.AssistantCoderList, error) {
	to, err := a.bound()
	if err != nil {
		return plugin.AssistantCoderList{}, err
	}
	return to.Coders(ctx, in)
}

// serveAssistants is the surface one Serve answers: it refuses once the
// Serve's context ended, so an optional plugin that is off reaches nothing.
type serveAssistants struct {
	serve *Serve
}

func (a serveAssistants) live() error {
	if err := a.serve.ctx.Err(); err != nil {
		return fmt.Errorf("plugin %s is off: %w", a.serve.id, err)
	}
	return nil
}

func (a serveAssistants) Create(ctx context.Context, in plugin.AssistantSpec) (plugin.Assistant, error) {
	if err := a.live(); err != nil {
		return plugin.Assistant{}, err
	}
	return a.serve.assistants.Create(ctx, in)
}

func (a serveAssistants) List(ctx context.Context, in plugin.AssistantQuery) (plugin.AssistantList, error) {
	if err := a.live(); err != nil {
		return plugin.AssistantList{}, err
	}
	return a.serve.assistants.List(ctx, in)
}

func (a serveAssistants) LastMessage(ctx context.Context, in plugin.AssistantMessageQuery) (plugin.AssistantMessage, error) {
	if err := a.live(); err != nil {
		return plugin.AssistantMessage{}, err
	}
	return a.serve.assistants.LastMessage(ctx, in)
}

func (a serveAssistants) Coders(ctx context.Context, in plugin.AssistantCoderQuery) (plugin.AssistantCoderList, error) {
	if err := a.live(); err != nil {
		return plugin.AssistantCoderList{}, err
	}
	return a.serve.assistants.Coders(ctx, in)
}

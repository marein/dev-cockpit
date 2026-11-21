package web

import (
	"context"
	"errors"
	"strings"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/web/render"
	"github.com/marein/dev-cockpit/plugin"
)

// PluginAssistants is the assistant surface every plugin delegates to, the
// paths `assistant-new` and the assistants page run.
func (s *Server) PluginAssistants() plugin.Assistants { return pluginAssistants{s: s} }

type pluginAssistants struct {
	s *Server
}

func (a pluginAssistants) Create(ctx context.Context, in plugin.AssistantSpec) (plugin.Assistant, error) {
	if err := ctx.Err(); err != nil {
		return plugin.Assistant{}, err
	}
	created, _, err := a.s.startAssistant(assistant.Handover{CoderID: strings.TrimSpace(in.CoderID), Name: in.Name, Message: in.Message})
	if created.ID == "" {
		return plugin.Assistant{}, err
	}
	if stored, getErr := a.s.assistants.Get(created.ID); getErr == nil {
		created = stored
	}
	return pluginAssistant(created.Summary), err
}

func (a pluginAssistants) List(ctx context.Context, in plugin.AssistantQuery) (plugin.AssistantList, error) {
	if err := ctx.Err(); err != nil {
		return plugin.AssistantList{}, err
	}
	summaries := a.s.assistants.Search(in.Contains)
	list := plugin.AssistantList{Assistants: make([]plugin.Assistant, 0, len(summaries))}
	for _, summary := range summaries {
		list.Assistants = append(list.Assistants, pluginAssistant(summary))
	}
	return list, nil
}

func (a pluginAssistants) LastMessage(ctx context.Context, in plugin.AssistantMessageQuery) (plugin.AssistantMessage, error) {
	if err := ctx.Err(); err != nil {
		return plugin.AssistantMessage{}, err
	}
	instance, err := a.s.assistants.Get(in.AssistantID)
	if errors.Is(err, assistant.ErrNotFound) {
		return plugin.AssistantMessage{}, plugin.ErrAssistantNotFound
	}
	if err != nil {
		return plugin.AssistantMessage{}, err
	}
	if len(instance.Messages) == 0 {
		return plugin.AssistantMessage{}, nil
	}
	m := instance.Messages[len(instance.Messages)-1]
	answer := plugin.AssistantMessage{ID: m.ID, Role: string(m.Role), Text: m.Content, Time: m.CreatedAt, State: string(m.State), Error: m.Error, Model: m.Model}
	if m.Note != nil {
		answer.Headline = m.Note.Headline
	}
	if line := compactHeadline(m); line != "" {
		answer.Headline = line
	}
	return answer, nil
}

func (a pluginAssistants) Coders(ctx context.Context, in plugin.AssistantCoderQuery) (plugin.AssistantCoderList, error) {
	if err := ctx.Err(); err != nil {
		return plugin.AssistantCoderList{}, err
	}
	coders := a.s.assistants.Coders()
	fallback := a.s.assistantDefaultCoderID(coders)
	list := plugin.AssistantCoderList{Coders: make([]plugin.AssistantCoder, 0, len(coders))}
	for _, co := range coders {
		list.Coders = append(list.Coders, plugin.AssistantCoder{ID: co.ID, Name: render.CoderLabel(co.ID), Default: co.ID == fallback})
	}
	return list, nil
}

func pluginAssistant(s assistant.Summary) plugin.Assistant {
	return plugin.Assistant{
		ID:            s.ID,
		Name:          s.Title,
		Named:         s.Named,
		CoderID:       s.CoderID,
		Model:         s.Model,
		CheckModel:    s.CheckModel,
		TriggerModel:  s.TriggerModel,
		CreatedAt:     s.CreatedAt,
		LastMessageAt: s.LastMessageAt,
		MessageCount:  s.MessageCount,
		Preview:       s.Preview,
		Running:       s.Running,
		Unfinished:    s.Unfinished,
		URL:           "/assistants/" + s.ID,
	}
}

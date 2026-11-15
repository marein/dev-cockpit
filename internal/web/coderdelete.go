package web

import (
	"errors"

	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

// coderDeleteWarning is what a coder delete does, see deleteCoder.
const coderDeleteWarning = "A running coder is stopped and its stored session deleted, so it cannot be resumed. The triggers on it are dropped."

// coderDeleteKind is the coder delete an assistant asks for: a target names
// the coder and its project, and its delete looks the session up again and
// says what went.
func (s *Server) coderDeleteKind() deleteKind {
	return deleteKind{ID: approvalCoderDelete, Plural: "coders", Warning: coderDeleteWarning, Resolve: func(id string) (deleteTarget, error) {
		name, cwd, err := s.coderSession(id)
		if err != nil {
			return deleteTarget{}, err
		}
		short := coder.ShortID(id)
		details := []askpass.Detail{{Label: "Coder", Value: name}, {Label: "ID", Value: short, As: askpass.AsMono}}
		project := s.projects.ProjectNameFor(cwd)
		if project != "" {
			details = append(details, askpass.Detail{Label: "Project", Value: project})
		}
		return deleteTarget{
			Key:     id,
			Name:    "coder " + name,
			Details: details,
			Item:    askpass.Detail{Icon: "terminal-2", Value: name, ID: short, Place: project},
			Project: project,
			Delete: func(bool) (string, []string, error) {
				if _, _, err := s.coderSession(id); err != nil {
					return "", nil, errors.New("the coder no longer exists")
				}
				deleted, err := s.deleteCoder(id)
				if err != nil {
					return "", nil, err
				}
				return assistant.CoderDeleted(coder.DisplayName(deleted.Name, id), deleted.Dropped), nil, nil
			},
		}, nil
	}}
}

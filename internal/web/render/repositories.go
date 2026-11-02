package render

import (
	"strings"

	"github.com/marein/dev-cockpit/internal/forge"
)

// RepositoryForge is a forge host the create form offers to clone from: the
// select's value, the host's id and its name.
type RepositoryForge struct {
	Choice string
	ID     string
	Label  string
}

// NewRepositoryForges are the create form's entries of the hosts a search
// asks. A built in host is named by its forge, an added one by its web
// address without the scheme, the way Settings › Forges lists them.
func NewRepositoryForges(hosts []forge.Host) []RepositoryForge {
	out := make([]RepositoryForge, 0, len(hosts))
	for _, h := range hosts {
		label := h.Kind.Label
		if !h.Builtin() {
			label = hostName(h)
		}
		out = append(out, RepositoryForge{Choice: CreateForge + h.ID, ID: h.ID, Label: label})
	}
	return out
}

// RepositoryChoice is one repository the create form's search offers: the
// mark of its forge, its path, the address picking it fills in and what the
// forge says of it.
type RepositoryChoice struct {
	Icon string `json:"icon"`
	Path string `json:"path"`
	URL  string `json:"url"`
	Hint string `json:"hint,omitempty"`
}

// RepositoryChoices is one answer of the create form's search: the
// repositories, or the sentence why the host could not be searched.
type RepositoryChoices struct {
	Repositories []RepositoryChoice `json:"repositories"`
	Failure      string             `json:"failure,omitempty"`
}

// NewRepositoryChoices words what a search found.
func NewRepositoryChoices(found []forge.Found) RepositoryChoices {
	out := RepositoryChoices{Repositories: make([]RepositoryChoice, 0, len(found))}
	for _, r := range found {
		out.Repositories = append(out.Repositories, RepositoryChoice{Icon: r.Host.Kind.Icon, Path: r.Path, URL: r.CloneURL, Hint: repositoryHint(r)})
	}
	return out
}

// RepositorySearchFailed is the answer of a search its host failed, its
// sentence as the one quiet line.
func RepositorySearchFailed(err error) RepositoryChoices {
	return RepositoryChoices{Repositories: []RepositoryChoice{}, Failure: err.Error()}
}

func repositoryHint(r forge.Found) string {
	var hints []string
	if r.Visibility != "" {
		hints = append(hints, r.Visibility)
	}
	if r.Fork {
		hints = append(hints, "fork")
	}
	if r.Archived {
		hints = append(hints, "archived")
	}
	return strings.Join(hints, " · ")
}

func hostName(h forge.Host) string {
	if _, rest, ok := strings.Cut(h.WebURL, "://"); ok {
		return rest
	}
	return h.WebURL
}

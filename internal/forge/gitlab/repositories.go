package gitlab

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/forge"
)

// GitLab matches a word within a field from minMatched characters on and
// drops a shorter one from a term with a longer word, a term of shorter
// words alone has to equal a whole field. ownPage is how many projects a
// search of the account's own asks for when shorter words narrow GitLab's
// answer here.
const (
	minMatched = 3
	ownPage    = 100
)

// project is a project as GET /projects answers it, the fields a search
// reads. GitLab names the project a fork came from only for a fork.
type project struct {
	Path       string          `json:"path_with_namespace"`
	SSHURL     string          `json:"ssh_url_to_repo"`
	HTTPURL    string          `json:"http_url_to_repo"`
	Visibility string          `json:"visibility"`
	Archived   bool            `json:"archived"`
	ForkedFrom json.RawMessage `json:"forked_from_project"`
}

func (p project) repository() forge.Repository {
	fork := len(p.ForkedFrom) > 0 && string(p.ForkedFrom) != "null"
	return forge.Repository{Path: p.Path, SSHURL: p.SSHURL, HTTPURL: p.HTTPURL, Visibility: p.Visibility, Fork: fork, Archived: p.Archived}
}

// OwnRepositories asks GitLab for the projects the token's user is a member
// of, directly or through a group, by their last activity. GitLab matches
// all words of a search only within one field, the path, the name or the
// description, so it is asked to match the full path with every group,
// which within the memberships is cheap. It matches only the words of
// minMatched characters and more there, the shorter ones narrow its answer
// by the path here. A term of short words alone it is asked to match
// against the project's own path and name, which a short name equals and a
// full path never does.
func (Provider) OwnRepositories(ctx context.Context, c *forge.Client, h forge.Host, token, term string, limit int) ([]forge.Repository, error) {
	query := url.Values{"membership": {"true"}, "order_by": {"last_activity_at"}, "sort": {"desc"}, "per_page": {strconv.Itoa(limit)}}
	var long, short []string
	for _, word := range forge.SearchWords(term) {
		if utf8.RuneCountInString(word) < minMatched {
			short = append(short, word)
		} else {
			long = append(long, word)
		}
	}
	switch {
	case len(long) > 0:
		query.Set("search", strings.Join(long, " "))
		query.Set("search_namespaces", "true")
		if len(short) > 0 {
			query.Set("per_page", strconv.Itoa(ownPage))
		}
	case len(short) > 0:
		query.Set("search", strings.Join(short, " "))
		short = nil
	}
	return projects(ctx, api(c, h, token), query, short, limit)
}

// PublicRepositories asks GitLab for the public projects whose name or path
// carries the name words of term, the most starred first. GitLab is not
// asked to match namespaces here: gitlab.com fails such a search of every
// public project after fifteen seconds.
func (Provider) PublicRepositories(ctx context.Context, c *forge.Client, h forge.Host, token, term string, limit int) ([]forge.Repository, error) {
	names, _ := forge.NameWords(term)
	if len(names) == 0 {
		return nil, nil
	}
	var owned []string
	for _, word := range forge.SearchWords(term) {
		if strings.Contains(word, "/") {
			owned = append(owned, word)
		}
	}
	query := url.Values{"visibility": {"public"}, "search": {strings.Join(names, " ")}, "order_by": {"star_count"}, "sort": {"desc"}, "per_page": {strconv.Itoa(limit)}}
	return projects(ctx, api(c, h, token), query, owned, limit)
}

// projects reads one page of GET /projects and keeps the first limit whose
// path carries every one of words, those GitLab was not asked to match. The
// others GitLab matched itself, also in a name or a description the path
// does not carry.
func projects(ctx context.Context, a forge.API, query url.Values, words []string, limit int) ([]forge.Repository, error) {
	var list []project
	if _, err := a.Get(ctx, "/projects?"+query.Encode(), &list); err != nil {
		return nil, refusal(a, err)
	}
	var out []forge.Repository
	for _, p := range list {
		if len(out) == limit {
			break
		}
		if forge.Carries(p.Path, words) {
			out = append(out, p.repository())
		}
	}
	return out, nil
}

package render

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/share"
)

// A list of Changes git's output cap cut ends saying so, with the editor's
// compare on the pair where the reader may go there, files and commits alike.
func TestAChangesListGitCutEndsSayingSo(t *testing.T) {
	files := []AssistantChangedFile{{Path: "a.txt", Status: "M", Added: 2}, {Path: "b.txt", Status: "A", Added: 1}}
	commits := []AssistantCommit{{Short: "1111111", Subject: "one"}, {Short: "2222222", Subject: "two"}}
	changes := AssistantProjectChanges{
		Project:    "app",
		Since:      AssistantProjectSince{Read: true, From: "main", Commits: 3, Changed: 2, Added: 3},
		Files:      NewAssistantFileList("a1", "app", ChangesFiles, files, 1, true, true, "/projects/app/editor?pair=suggested&view=compare"),
		Commits:    NewAssistantRows(commits, 3, 1, true, true),
		LogRead:    true,
		CompareURL: "/projects/app/editor?pair=suggested&view=compare",
	}
	html := renderPart(t, "assistant_project_changes.gohtml", changes)
	if strings.Count(html, "data-assistant-changes-cut>") != 2 || strings.Count(html, `<a class="btn btn-sm" href="/projects/app/editor?pair=suggested&amp;view=compare" data-file-link data-assistant-changes-compare>`) != 2 {
		t.Fatalf("the cut lists do not end saying so with the editor's compare:\n%s", html)
	}
	if !strings.Contains(html, `data-assistant-changed-more="files"><span data-fold-shut>Show 1 more file</span>`) || !strings.Contains(html, "data-assistant-changes-older><span data-fold-shut>Show 2 older commits</span>") {
		t.Fatalf("the rows past the first do not count the whole list:\n%s", html)
	}
	changes.CompareURL, changes.Files.URL = "", ""
	if html := renderPart(t, "assistant_project_changes.gohtml", changes); strings.Count(html, "data-assistant-changes-cut>") != 2 || strings.Contains(html, "data-assistant-changes-compare") {
		t.Fatalf("a guest off the link is led into the editor:\n%s", html)
	}
}

// A card's review line counts what is open, and where the card has no changes
// line into its project and nothing is open, the resolved threads.
func TestAReviewLineCountsResolvedThreadsWhereNothingElseLeadsIn(t *testing.T) {
	for _, c := range []struct {
		card AssistantProjectCard
		want string
	}{
		{AssistantProjectCard{Threads: 2, Resolved: 1, Unpublished: 1}, "2 open threads · 1 unpublished comment"},
		{AssistantProjectCard{Resolved: 2}, "2 resolved threads"},
		{AssistantProjectCard{Resolved: 2, Git: AssistantProjectGit{Repo: true, Read: true}}, ""},
		{AssistantProjectCard{Unpublished: 1, Resolved: 1}, "1 unpublished comment"},
	} {
		if got := c.card.ReviewLine(); got != c.want {
			t.Fatalf("%+v reads %q, want %q", c.card, got, c.want)
		}
	}
}

// A card's job row leads where the Coders tab's does: a stopped coder's row
// posts the Coders tab's resume with the page's token, a running one opens
// the coder. A guest's rows resume nothing, a stopped coder off the link leads
// nowhere. Every fold row says what a press does next, on both sides of its
// state.
func TestACardsJobRowsResumeForTheOwnerAlone(t *testing.T) {
	jobs := []AssistantJobView{
		{Terminal: "t1", Name: "one", URL: "/coders/t1", Stopped: true},
		{Terminal: "t2", Name: "two", URL: "/coders/t2", Open: true},
		{Terminal: "t3", Name: "three", URL: "/coders/t3", State: "done"},
	}
	data := AssistantProjectsData{Projects: []AssistantProjectCard{{Name: "app", Jobs: jobs, Older: 1}}, Older: 2, Unfolded: true, CSRFToken: "tok"}
	html := renderPart(t, "assistant_projects_list.gohtml", data)
	resume := `<form method="post" action="/coders/t1/resume" class="d-flex" data-assistant-project-job="t1">
  <input type="hidden" name="csrf_token" value="tok">`
	for _, want := range []string{
		resume,
		`title="Resume coder" data-assistant-job-resume>`,
		`href="/coders/t2" data-assistant-project-job="t2" data-assistant-project-job-link>`,
		`aria-expanded="false" aria-controls="assistant-project-`,
		`data-assistant-project-older><span data-fold-shut>Show 1 older coder</span><span data-fold-open>Show fewer coders</span></button>`,
		`aria-expanded="true" data-assistant-older><span data-fold-shut>Show 2 older projects</span><span data-fold-open>Show fewer projects</span></button>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("the owner's tab lacks %s:\n%s", want, html)
		}
	}
	if strings.Count(html, "data-assistant-job-resume") != 1 || strings.Contains(html, `href="/coders/t1"`) {
		t.Fatalf("a row other than the stopped coder's resumes, or the stopped one opens:\n%s", html)
	}
	coders := renderPart(t, "assistant_jobs_list.gohtml", AssistantData{Page: Page{CSRFToken: "tok"}, Jobs: jobs[:1]})
	if !strings.Contains(coders, `<form method="post" action="/coders/t1/resume" class="d-flex flex-fill">
  <input type="hidden" name="csrf_token" value="tok">`) {
		t.Fatalf("the Coders tab does not post the same resume:\n%s", coders)
	}

	data.Access, data.CSRFToken = guestOf(share.KindAssistant), ""
	data.Projects[0].Jobs[0].URL = ""
	guest := renderPart(t, "assistant_projects_list.gohtml", data)
	if strings.Contains(guest, "<form") || strings.Contains(guest, "data-assistant-job-resume") || !strings.Contains(guest, `<div class="list-group-item border-0 d-flex align-items-center gap-2 px-3 py-1 small" data-assistant-project-job="t1">`) {
		t.Fatalf("a guest's stopped coder resumes or leads somewhere:\n%s", guest)
	}

	data.Unfolded = false
	if folded := renderPart(t, "assistant_projects_list.gohtml", data); !strings.Contains(folded, `aria-expanded="false" data-assistant-older>`) {
		t.Fatalf("the folded older projects do not say so:\n%s", folded)
	}
	reviews := AssistantProjectReviews{Project: "app", Resolved: []AssistantThread{{ID: "a"}, {ID: "b"}}}
	if got := reviews.ResolvedLabel(); got != (FoldLabel{Shut: "Show 2 resolved threads", Open: "Hide resolved threads"}) {
		t.Fatalf("the resolved fold says %+v", got)
	}
}

// Two assistants side by side as editor tabs may show the same project, and
// Bootstrap finds a fold or a tab by its id in the whole document: every id
// of the aside carries the assistant, and stays the same for it.
func TestAsideIDsCarryTheAssistant(t *testing.T) {
	ids := func(owner string) []string {
		card := AssistantProjectCard{Owner: owner, Name: "app"}
		list := NewAssistantFileList(owner, "app", ChangesFiles, nil, 1, false, false, "")
		return []string{
			card.FoldID(),
			AssistantProjectView{Card: card}.PaneID(),
			AssistantProjectReviews{Owner: owner, Project: "app"}.ResolvedFold(),
			AssistantProjectChanges{Owner: owner, Project: "app"}.CommitsFold(),
			list.Fold(),
			AssistantChangedFile{Owner: owner, List: ChangesFiles, Path: "a.txt"}.Fold(),
		}
	}
	a, again, b := ids("a1"), ids("a1"), ids("a2")
	seen := map[string]bool{}
	for i := range a {
		if a[i] != again[i] || a[i] == b[i] || seen[a[i]] || seen[b[i]] {
			t.Fatalf("id %d is not the assistant's own: %s, %s, %s", i, a[i], again[i], b[i])
		}
		seen[a[i]], seen[b[i]] = true, true
	}
}

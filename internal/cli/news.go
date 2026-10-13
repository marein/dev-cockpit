package cli

import (
	"net/url"
	"strings"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/backup"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/shell"
)

// notifyResolver enriches notifications with the name, project, and target
// page at ingest time, using the cached coder snapshots and shell list so a
// burst of events never rescans coder state.
func notifyResolver(coders []*coder.Manager, shells *shell.Shells, conversations *assistant.Service, projects *project.Repository, backups *backup.Service, dockerService *docker.Service, askBroker *askpass.Broker) notify.Resolver {
	return func(targetID string) notify.TargetInfo {
		info := notify.TargetInfo{}
		if notify.IsGitPromptTarget(targetID) {
			project := notify.GitPromptTargetProject(targetID)
			info.Name = "Git"
			info.Project = project
			// The dialog is app-wide, any signed-in page shows it, so the
			// entry leads home instead of to a page that may be gone.
			info.URL = "/projects"
			// The entry is written while the question stands, so the project's
			// running action is the one it is about; a question that vanished
			// in between keeps the generic word.
			actionName := "git"
			if action := askBroker.Find(project); action != nil {
				actionName = action.Name()
			}
			info.Title, info.Detail = gitPromptNews(actionName)
			return info
		}
		if notify.IsApprovalTarget(targetID) {
			// The entry is written while the question stands, so the
			// approval's standing question is the one it is about: who asks
			// and the one line it is read by. A question that vanished in
			// between keeps the generic words. The dialog is app-wide, so the
			// entry leads home like a git question's.
			key := askpass.ApprovalKey(notify.ApprovalTargetID(targetID))
			info.Name = "Approval"
			info.URL = "/projects"
			who, what := "", ""
			for _, q := range askBroker.Questions() {
				if q.Key == key {
					who, what = q.Assistant, q.Action
					break
				}
			}
			info.Title, info.Detail = approvalNews(who, what)
			return info
		}
		if notify.IsSSHKeyTarget(targetID) {
			// Written while the question stands, so its caller is the one it
			// is about. The dialog is app-wide, so the entry leads home.
			key := askpass.SSHKeyKey(notify.SSHKeyTargetID(targetID))
			info.Name = "SSH key"
			info.URL = "/projects"
			who := ""
			for _, q := range askBroker.Questions() {
				if q.Key == key {
					who = q.Action
					break
				}
			}
			info.Title, info.Detail = sshKeyNews(who)
			return info
		}
		if notify.IsDockerTarget(targetID) {
			project := notify.DockerTargetProject(targetID)
			info.Name = "Compose"
			info.Project = project
			info.URL = "/projects"
			if project != "" {
				info.URL = "/projects#project-" + project
			}
			// The notification fires right after a run of that project
			// finished, so its newest finished run is the one it is about.
			// Asked per project, because another project may have finished a
			// run in the same moment.
			if run, ok := dockerService.LastComposeRun(project); ok {
				info.Title, info.Detail = composeNews(run)
				// A failure is no follow-up: right after a success the fresh
				// unread entry says everything went through, and the dedupe
				// window would swallow the one word the user is owed.
				info.Urgent = run.Failure != ""
				// The output of the run is what somebody wants after such a
				// notification, not the row it ran for.
				if run.ID != "" {
					info.URL = "/projects/" + url.PathEscape(project) + "/docker/runs/" + run.ID
				}
			}
			return info
		}
		// The assistants come first: their provider sessions are hidden from
		// the coder lists, and they are the only surface with a fixed home, so
		// the lookup is cheap and cannot be shadowed by a coder of the same id.
		for _, entry := range conversations.List() {
			if entry.ID != targetID {
				continue
			}
			// One bell for all of them. The name is what this assistant is
			// called, cut to a label, and it opens the line below the title.
			// The link is that assistant's page, and the fragment names the
			// answer the page scrolls to. An assistant with nothing in it goes
			// through the same wording with an empty message, so there is one
			// sentence for the branch and not two.
			info.Name = assistantNewsName(entry)
			info.URL = "/assistants/" + entry.ID
			m, ok := conversations.LastAnswer(entry.ID)
			if ok {
				info.URL += "#message-" + m.ID
			}
			info.Title, info.Detail = assistantNews(info.Name, m)
			return info
		}
		if targetID == notify.BackupTarget {
			info.Name = "Backup"
			info.URL = "/settings/backup"
			// The notification fires right after a job finished, so the
			// newest finished entry is the one it is about.
			if b, ok := backups.LastFinished(); ok {
				info.Title, info.Detail = backupNews(b.Name, b.Done())
			}
			return info
		}
		for _, m := range coders {
			snap := m.Snapshot()
			for _, r := range snap.Running {
				if r.Identifier == targetID {
					info.Name = r.Name
					info.Project = projects.ProjectNameFor(r.CWD)
					info.URL = "/coders/" + r.Identifier
					info.Title, info.Detail = coderNews(info.Name)
					return info
				}
			}
			for _, stored := range snap.Resumable {
				if stored.SessionID == targetID {
					info.Name = stored.Name
					info.Project = projects.ProjectNameFor(stored.CWD)
					info.URL = "/coders/" + stored.SessionID
					info.Title, info.Detail = coderNews(info.Name)
					return info
				}
			}
		}
		for _, sh := range shells.List() {
			if sh.Identifier == targetID {
				info.Name = sh.Name
				info.Project = projects.ProjectNameFor(sh.CWD)
				info.URL = "/shells/" + sh.Identifier
				info.Title, info.Detail = shellNews(info.Name)
				return info
			}
		}
		// A target nothing knows any more, a session deleted between the
		// signal and this lookup: there is no kind, so there is no sentence
		// either, and every surface falls back to "Something new in ...".
		return info
	}
}

// Every notification this cockpit writes reads the same way: the title says
// what happened, the line below it says what it happened to and, where
// something was written, what that was. The wording of all of them stands
// here, next to each other, because that is the only way it stays one
// wording. internal/notify writes the entries and classifies nothing, so
// nothing of this belongs there.

// coderNews is what a coder's signal says: that there is news, and the coder
// stands below it. Whether the coder finished its turn, asks a question or
// waits for a permission is what this cockpit deliberately does not classify,
// see internal/notify.
func coderNews(name string) (title, detail string) {
	return "Coder has news.", newsDetail(name, "")
}

// shellNews is what a shell's signal says. A shell reports when a foreground
// command ended, so that is what the title says, with the shell below it.
func shellNews(name string) (title, detail string) {
	return "Command finished.", newsDetail(name, "")
}

// backupNews is what a finished backup job says, the archive below how it
// went.
func backupNews(name string, ok bool) (title, detail string) {
	title = "Backup failed."
	if ok {
		title = "Backup ready."
	}
	return title, newsDetail(name, "")
}

// gitPromptNews is what a standing askpass question says: git is waiting for
// an answer, and the action it is waiting in stands below that. The entry is
// marked read again the moment the question no longer stands, however it went,
// so the title may speak in the present.
func gitPromptNews(action string) (title, detail string) {
	return "Git asks a question.", newsDetail(action, "")
}

// approvalNews is what a standing approval says: an assistant asks, and below
// it which one and what it waits to do, the question's own action line
// ("Compose down in shop", "Delete project shop"), which the asker writes
// because only it knows what kind of action waits. The assistant's name is
// already a label where it is written (assistantNewsName).
func approvalNews(who, what string) (title, detail string) {
	if who == "" {
		who = assistant.Name
	}
	if what == "" {
		what = "an action"
	}
	return "Assistant asks approval.", newsDetail(who, what)
}

// sshKeyNews is what a terminal's standing question of the ssh agent says: a
// key is wanted, and below it the terminal that wants it ("Shell: build").
func sshKeyNews(who string) (title, detail string) {
	if who == "" {
		who = "A terminal"
	}
	return "SSH key wanted.", newsDetail(who, "")
}

// composeNews is what a finished docker compose run says: how it went, and
// the command that ran below it.
func composeNews(run docker.RunView) (title, detail string) {
	title = "Compose finished."
	if run.Failure != "" {
		title = "Compose failed."
	}
	name := run.Action
	if name == "" {
		name = "compose"
	}
	return title, newsDetail(name, "")
}

// postScriptNews is what a failed worktree post script says, the worktree
// project below it with the reason and the last lines of the output, which is
// where a failure says why.
func postScriptNews(name string, run project.PostScriptRun) (title, detail string) {
	lines := strings.Split(strings.TrimSpace(run.Output), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	excerpt, _ := markdown.Excerpt(run.Err+": "+strings.Join(lines, " "), assistant.PreviewRunes)
	return "Post script failed.", newsDetail(name, excerpt)
}

// assistantNewsName is the name a notification is rung under: what this
// assistant is called, and the surface's own word for one that has not been
// named yet. That name is the conversation's title, which is written from the
// first message somebody typed and is a whole paragraph as often as not, so it
// goes through the same cut a coder's session title does. A paragraph is
// unusable in every surface that shows it: the head of a notification's lower
// line, and the "Something new in ..." an unresolved entry falls back to.
func assistantNewsName(entry assistant.Summary) string {
	if title := strings.TrimSpace(entry.Title); title != "" && title != assistant.DefaultTitle {
		return coder.ShortTitle(title)
	}
	return assistant.Name
}

// assistantNews is what a notification about one assistant says, and it says
// it in two lines that divide the work. The title is the kind alone, one of
// twelve fixed sentences: the words tell a job from a compose run from an
// approval from a trigger from an answer, and the endings of each apart. The
// line below it
// names which one it was and then carries an excerpt of what was written, see
// newsDetail: an entry that only said that something happened would send the
// user into the thread to find out what.
//
// The identifier is the most precise thing there is: the job a report closed,
// the headline a reaction fired under, which is the trigger's name where the
// user gave it one. Where nothing narrower exists, an answer somebody asked
// for and a report from before the note carried a name, the assistant it came
// from stands there. That is the same rule and not an exception, and it lands
// the name in exactly the case where several assistants could be confused.
//
// A report's excerpt does not repeat the verdict its title already says: the
// report is stored without it, parseVerdict cuts the word off the answer
// before it is ever written down.
//
// The job's name comes from the message's own note, never from a lookup: this
// resolver runs before the job store exists, and a terminal that is steered
// again would hand back the successor's job.
func assistantNews(who string, m assistant.Message) (title, detail string) {
	ident := ""
	if m.Note != nil && m.Note.Source == assistant.NoteCheck {
		// A job ends in one of three states and the title says which one, in
		// the words of the JobState it was closed with.
		switch m.Note.Verdict {
		case string(assistant.VerdictDone):
			title, ident = "Job done.", m.Note.Name
		case string(assistant.VerdictBlocked):
			title, ident = "Job blocked.", m.Note.Name
		case string(assistant.VerdictExpired):
			title, ident = "Job expired.", m.Note.Name
		}
	}
	if m.Note != nil && m.Note.Source == assistant.NoteCompose {
		// A compose run of the assistant ends in one of two ways, in the
		// words of the note's own verdict, and the command names it.
		switch m.Note.Verdict {
		case assistant.ComposeKindDone:
			title = "Compose done."
		case assistant.ComposeKindFailed:
			title = "Compose failed."
		}
		ident = m.Note.Name
	}
	if m.Note != nil && m.Note.Source == assistant.NoteApproval {
		// An action the user was asked about ends in one of three ways, and
		// the action's own line names it.
		switch m.Note.Verdict {
		case approval.Done:
			title = "Action approved."
		case approval.Failed:
			title = "Approved action failed."
		case approval.Declined:
			title = "Action declined."
		}
		ident = m.Note.Name
	}
	unfinished := m.State == assistant.StateFailed || m.State == assistant.StateInterrupted
	// Nobody asked for this answer, so the title says a trigger fired rather
	// than that the assistant answered, and it says it for a turn that broke
	// off too: what is on the screen has to be recognizable as a reaction
	// either way.
	if title == "" && m.Origin != nil && m.Origin.Source == assistant.NoteEvent {
		title, ident = "Trigger fired.", m.Origin.Headline
		if unfinished {
			// The word the thread and the code use for a turn that stopped
			// before it was done.
			title = "Trigger broke off."
		}
	}
	if title == "" {
		title = "Answer ready."
		if unfinished {
			// Whatever was written before it broke off is still the best line
			// about what the turn was doing.
			title = "Answer broke off."
		}
	}
	// Nothing narrower than the assistant itself: an answer somebody asked
	// for, and a report from before the note carried a name.
	if strings.TrimSpace(ident) == "" {
		ident = who
	}
	return title, newsDetail(ident, answerExcerpt(m.Content))
}

// newsTitleRunes is the room a title has. Every title above is a fixed
// sentence, nothing is composed out of user text and nothing is ever cut, so
// this is no budget the code spends: it is the bound the wording is written
// to and the one the test pins. It is the narrowest of the three surfaces a
// title surfaces in, a phone's push, where iOS gives the title one line and
// writes the app's own name on the second, and a lock screen of this
// cockpit's own pushes ran out at exactly 32 runes with the system's mark
// among them. The bell's list and a toast hold 40 to 46, so a sentence that
// fits the push stands whole in all three. The wider two decided this line
// while it still ended in an identifier that could be shortened; a sentence
// cannot be shortened, so the narrowest surface decides it now.
const newsTitleRunes = 32

// newsDetail writes the lower line: the identifier of what this happened to,
// then what was written where there is a text of it. Every kind builds it,
// which is what makes one line read like the next.
//
// The identifier opens this line instead of closing the title because a phone
// gives the title one line and the body three or four: a name is read whole
// down here and cut up there, and what a push cuts off the end of this line
// is the tail of an excerpt, by construction the cheapest part of it. The
// kinds that write no text stop after the identifier, so their line is the
// name alone.
//
// Nothing is cut here. Every identifier is already a label where it is
// written: a coder's title through coder.ShortTitle, an assistant's the same
// way through assistantNewsName, a trigger's name bounded where it is typed.
func newsDetail(ident, excerpt string) string {
	ident = oneLine(ident)
	switch {
	case ident == "":
		return excerpt
	case excerpt == "":
		return ident
	}
	return ident + ": " + excerpt
}

// oneLine folds every line break and indent into one space: a title and an
// excerpt land on a single line wherever they surface.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// answerExcerpt turns an answer into the line a notification shows. It is the
// same preview the thread folds a note and a pushed answer under
// (assistant.PreviewRunes), so what brought a reader here and what stands in
// front of them when they arrive are the same words.
func answerExcerpt(content string) string {
	line, _ := markdown.Excerpt(content, assistant.PreviewRunes)
	return line
}

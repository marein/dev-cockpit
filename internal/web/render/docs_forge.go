package render

import (
	"fmt"
	"html/template"
)

func docsForgeTopic() DocsTopic {
	pace := forgePace()
	return DocsTopic{
		Key:      "forges",
		Title:    "Forges",
		Icon:     "ti-git-pull-request",
		Lead:     "Change requests, pipelines and review threads of GitLab and GitHub, on the project row, in the editor and for the assistants.",
		LinkURL:  "/settings/forges",
		LinkText: "Open forges",
		Items: []DocsItem{
			{
				Title: "Forge hosts",
				Desc:  `Settings &rarr; Forges lists the hosts an origin is matched against, github.com and gitlab.com built in. <em>Add host</em> adds a self hosted GitLab or GitHub: its kind, its web address with its path, like <code>https://example.com/gitlab</code>, and its alias hosts, other names an origin may use, like an SSH host or a <code>Host</code> of <code>~/.ssh/config</code>. A name belongs to one host. A server whose certificate an internal CA signed takes the CA's PEM certificates as <em>CA bundle</em>. An added host's <em>Edit</em> changes these, a built in host has none.`,
			},
			{
				Title: "Tokens and the connection test",
				Desc:  `A host's personal access token has a view of its own, opened right after <em>Add host</em> and by the row's <em>add token</em>, or <em>change token</em> once it has one. <em>Save and test</em> keeps the token and runs <em>Test</em>, <em>Later</em> goes back to the list. A host with a token takes a new one through <em>Replace and test</em>, <em>Remove</em> takes it away. <em>New token</em> opens the server's page that creates one named dev-cockpit: on GitLab with the scope <code>api</code>, on GitHub a classic one with <code>repo</code>, <code>workflow</code> and <code>read:org</code>. Reading needs <code>read_api</code> on GitLab, writing <code>api</code>; writing on GitHub needs <code>repo</code> on a classic token, Actions and Pull requests read and write on a fine grained one. The token is never shown again, a long one only by its last four characters. <em>Test</em> shows whom the token belongs to, with the server's version, or why it failed. The tokens stay in a file only you can read and travel in the backup as <em>Forge tokens</em>.`,
			},
			{
				Title: "A project's forge",
				Desc:  `<em>Forge</em> in a repository's git menu shows where the project is bound and links the repository. <em>From the origin</em> binds it by its origin, <em>Set by hand</em> to a host and repository path, <em>Off</em> to no forge. A worktree binds like its main project. The bindings travel in the backup as <em>Forges</em>, an import drops the binding of a project this host does not have.`,
			},
			{
				Title: "Change request and pipeline",
				Desc:  `For the branch HEAD is on the cockpit reads its open change request, else the one merged or closed at its pushed head, and the pipeline of the pushed head with its jobs, on GitHub the check runs and commit statuses combined. The pushed head is what origin's tracking branch names as last fetched or pushed, never the local HEAD.`,
			},
			{
				Title: "Status on the project row",
				Desc:  template.HTML(fmt.Sprintf(`Beside the branch a bound row shows the change request by its number, open <i class="ti ti-git-pull-request align-text-bottom" aria-hidden="true"></i>, draft <i class="ti ti-git-pull-request-draft align-text-bottom" aria-hidden="true"></i>, merged <i class="ti ti-git-merge align-text-bottom" aria-hidden="true"></i> or closed <i class="ti ti-git-pull-request-closed align-text-bottom" aria-hidden="true"></i>, and the pipeline by its status: green passed, red failed, blue running or waiting, a running one with a spinner. Each links to its page on the forge. A warning <i class="ti ti-alert-triangle align-text-bottom" aria-hidden="true"></i> says why the last read failed. Each project is checked on its own: every %s while a pipeline runs or %s after a push, every %s when idle. Review threads are read along with these checks, at most every %s. After a failed check it waits longer, up to %s.`, pace.Fast, pace.Moved, pace.Slow, pace.Review, pace.Slow)),
			},
			{
				Title: "Status in the editor",
				Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "E"}}},
				Desc:  `The forge sheet opens from the forge's button in the statusbar and from <em>Forge</em> in the editor menu. It shows the row's badge, the change request's title and why the last read failed. <em>Jobs</em> lists the pipeline's jobs with stage and status. <em>Log of</em> a failed job, or a tap on a job, shows the last 100 lines of its log, read again every 5 seconds while the job runs. A job without a log opens its page on the forge instead, if it has one. GitHub hands out a log only once the job ended. A guest of a shared link sees none of it.`,
			},
			{
				Title: "For the assistants",
				Desc:  `An assistant reads the same with <code>forge-show</code>, with <code>--log</code> the end of the failed jobs' logs as plain text and with <code>--threads</code> the review threads with their notes, each with the id a reply takes where the forge keeps it as a thread. GitLab's own system notes are left out, on GitHub the general threads are the conversation's comments and the review bodies.`,
			},
			{
				Title: "Review threads as line comments",
				Desc:  template.HTML(fmt.Sprintf(`Each review thread on a line of the change request's diff is a line comment thread with the forge's icon <i class="ti ti-brand-gitlab align-text-bottom" aria-hidden="true"></i> <i class="ti ti-brand-github align-text-bottom" aria-hidden="true"></i>: its first note opens it, the others are its replies, and <em>Open the thread on</em> links it on the forge. They are read with the branch's status, at most every %s, follow their line like any comment and go with the change request or the binding. They change on the forge alone, <em>Delete all comments</em> keeps them. A general thread of the conversation is a project comment with the forge's icon, under <em>Project</em> at the top of <em>Line comments</em>, a thread on a whole file stands on no line. A guest reads them and answers none.`, pace.Review)),
			},
			{
				Title: "Branches the forge stays off for",
				Desc:  `A branch whose name, or its name on origin, holds a space, a control character or an invisible one, like a direction mark or a zero width space, is never read from the forge nor written to it: such a name could hide in a line or rewrite a terminal. A change request that merges into such a branch shows its number alone. The row's warning, the forge sheet and an assistant's read name the character.`,
			},
			{
				Title: "Run failed jobs again",
				Desc:  `<em>Retry failed jobs</em>, on GitHub <em>Re-run failed jobs</em>, in the forge sheet, its <em>Jobs</em> and the git menu of the project's row runs the failed jobs of a failed pipeline again, <em>Retry job</em> in a job's log that job. The forge is read again first: a branch pushed since or a job that ran again is refused. A commit status and a check run of another app cannot run again, the row names them. An assistant retries only once you approve, see Approvals under Assistants.`,
			},
			{
				Title: "Open a change request",
				Desc:  `A pushed branch without an open change request, other than the default branch, offers <em>New merge request</em>, on GitHub <em>New pull request</em>, in the forge sheet. Its title and description come from the commits the pushed head carries since the default branch, it merges into the default branch and opens as a draft, all of which the form changes. Only what is pushed is in it, the form says when the local branch stands elsewhere. An assistant opens one with <code>forge-change-new</code> only once you approve.`,
			},
			{
				Title: "Publish line comments",
				Desc:  `Your own line comments publish as review comments on the open change request of the branch HEAD is on, each a thread of its own: <em>Publish</em> in its entry of <em>Line comments</em>, <em>Publish comment</em> in the line's menu, <em>Publish all comments</em> in the list. Its line has to be in the pushed head and in the change request's diff, else the publish is refused as a whole. A project comment publishes as a general comment of the change request, without a line. A published comment becomes that review thread, a project comment that general thread. A guest's comment and one with replies do not publish. On GitLab a line of a comment, reply or description that opens with a slash goes behind a backslash, so it runs no quick action like <code>/merge</code>. An assistant publishes with <code>forge-comment-publish</code> only once you approve.`,
			},
			{
				Title: "Answer and resolve review threads",
				Desc:  `<em>Open thread</em> on a review thread of the forge offers a reply and, while it is unresolved, <em>Resolve the thread</em>, after the reply or alone. A general thread takes them too on GitLab, which resolves only a comment that started a thread; a comment of GitHub's conversation is no thread and stays read only. A resolved thread reopens on the forge, not here. On GitLab a resolve needs the Developer role or the token's own merge request. GitHub shows a reply filed under a pending review of the token's account once that review is submitted. An assistant answers with <code>forge-thread-reply</code> and resolves with <code>forge-thread-resolve</code> only once you approve.`,
			},
			{
				Title: "Pipeline events",
				Desc:  `A trigger on <em>Pipeline succeeded</em>, <em>Pipeline failed</em> or <em>Pipeline ended</em> names a bound project and optionally a branch. It fires when the pipeline of the checked out branch passes, fails or ends in any way, once per pipeline and end, also across a restart; a named branch only while it is checked out. A pipeline that ended before the cockpit first read the branch fires nothing.`,
			},
			{
				Title: "Review comment events",
				Desc:  `A trigger on <em>Review comment</em> names a project and branch the same way. It fires once per note somebody else wrote on the change request, a reply included, also across a restart. Notes of the token's account and those standing at the first read fire nothing.`,
			},
		},
	}
}

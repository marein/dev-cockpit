package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
	"github.com/spf13/cobra"
)

// worktreeCreateTimeout bounds a worktree create: git's own bound for the
// working copy, the post script's, and an action's for the rest.
const worktreeCreateTimeout = git.WorktreeTimeout + project.PostScriptTimeout + actionTimeout

func newProjectWorktreeNewCommand(opts *inspectOptions) *cobra.Command {
	var from, name string
	var fastForward bool
	cmd := &cobra.Command{
		Use:   "project-worktree-new <project> <branch>",
		Short: "Make a git worktree of a project, as a project of its own",
		Long: "Make a linked git worktree of a project the same way the create form does. The " +
			"worktree is a project of its own, named <project>-<branch> unless --name says " +
			"otherwise. The project is a main repository from `status`, never a worktree. " +
			"Without --from the branch exists already: a local branch, or a remote one like " +
			"origin/feature, which becomes the local branch feature that follows it. A branch " +
			"another working copy holds is refused. With --from a new branch of that name " +
			"starts at the given branch.\n\n" +
			"The worktree post script from Settings › Projects runs in the new worktree, its " +
			"output is printed. A failed script fails the command, the worktree stands; tell " +
			"the user instead of making it again.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectWorktreeNew(cmd.OutOrStdout(), *opts, args[0], args[1], from, name, fastForward)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "create the branch new, starting at this branch")
	cmd.Flags().StringVar(&name, "name", "", "project name (default: <project>-<branch>)")
	cmd.Flags().BoolVar(&fastForward, "fast-forward", false, "bring an existing branch up to its upstream when it is purely behind")
	return cmd
}

func runProjectWorktreeNew(out io.Writer, opts inspectOptions, source, branch, from, name string, fastForward bool) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	form := url.Values{
		"create":       {render.WorktreeChoice(strings.TrimSpace(source))},
		"project_name": {strings.TrimSpace(name)},
	}
	if from = strings.TrimSpace(from); from != "" {
		form.Set("branch_mode", "new")
		form.Set("new_branch", strings.TrimSpace(branch))
		form.Set("start", from)
	} else {
		form.Set("branch_mode", "existing")
		form.Set("branch", strings.TrimSpace(branch))
	}
	if fastForward {
		form.Set("fast_forward", "on")
	}
	made, err := client.PostForm("/projects", form, worktreeCreateTimeout)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "project %s created at %s, a worktree of %s on branch %s\n",
		text(made["name"]), text(made["path"]), text(made["worktree_of"]), text(made["branch"]))
	if onto, _ := made["fast_forwarded"].(string); onto != "" {
		fmt.Fprintf(out, "fast-forwarded to %s\n", onto)
	}
	if failed, _ := made["fast_forward_error"].(string); failed != "" {
		fmt.Fprintf(out, "not fast-forwarded: %s\n", failed)
	}
	output, ran := made["post_script_output"].(string)
	if !ran {
		return nil
	}
	failed, _ := made["post_script_error"].(string)
	if failed != "" {
		fmt.Fprintf(out, "post script failed: %s\n", failed)
	} else {
		fmt.Fprintln(out, "post script ran")
	}
	if strings.TrimSpace(output) != "" {
		fmt.Fprintf(out, "--- output\n%s", output)
		if !strings.HasSuffix(output, "\n") {
			fmt.Fprintln(out)
		}
	}
	if failed != "" {
		return errors.New("the post script failed, the worktree project stands")
	}
	return nil
}

func newProjectWorktreeListCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "project-worktree-list <project>",
		Short: "List the git worktrees of a project",
		Long: "List the linked worktrees of a project's repository: the project each one is, " +
			"its branch and its directory. A worktree outside the projects root is no project " +
			"and stands with its directory alone. Asked about a worktree, it lists the " +
			"worktrees of its main project. Reads only, changes nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectWorktreeList(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runProjectWorktreeList(out io.Writer, opts inspectOptions, name string) error {
	picture, err := openTerminals(opts)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, worktreeList(picture.projects.List(), strings.TrimSpace(name)))
	return err
}

// worktreeList is what project-worktree-list prints, kept apart from the
// reading so it is testable without a projects root.
func worktreeList(projects []project.Project, name string) string {
	byName := map[string]project.Project{}
	for _, p := range projects {
		byName[p.Name] = p
	}
	p, ok := byName[name]
	if !ok {
		return fmt.Sprintf("there is no project %s\n", name)
	}
	if !p.GitRepo {
		return fmt.Sprintf("%s is no git repository\n", name)
	}
	var b strings.Builder
	if p.GitWorktree {
		if p.GitWorktreeOf == "" {
			return fmt.Sprintf("%s is a worktree of %s, which is no project\n", name, p.GitWorktreeMain)
		}
		fmt.Fprintf(&b, "%s is a worktree of %s\n", name, p.GitWorktreeOf)
		p = byName[p.GitWorktreeOf]
	}
	if len(p.GitWorktrees) == 0 {
		fmt.Fprintf(&b, "%s has no worktrees\n", p.Name)
		return b.String()
	}
	fmt.Fprintf(&b, "Worktrees of %s (%d)\n", p.Name, len(p.GitWorktrees))
	for _, w := range p.GitWorktrees {
		wt, isProject := byName[w.Project]
		if w.Project == "" || !isProject {
			fmt.Fprintf(&b, "  %s (no project)\n", w.Path)
			continue
		}
		fmt.Fprintf(&b, "  %s (%s) %s\n", wt.Name, orDash(wt.GitBranch), w.Path)
	}
	return b.String()
}

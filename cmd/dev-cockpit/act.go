package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/assistant"
	"github.com/local/dev-cockpit/internal/localapi"
	"github.com/spf13/cobra"
)

// The inspection commands read state files directly. Acting cannot: a state
// directory belongs to one serve process, and writing tmux or a state file from
// the side would bypass its caches and its event stream. So this goes through
// the running server, over the socket in its state directory, and lands in
// exactly the handler a browser would hit.

// Typing into a terminal is one tmux call and answers immediately. Starting a
// coder or removing a project is a session coming up or terminals going down,
// which is seconds of work, so those get a bound that does not cut it off half
// way.
const (
	inputTimeout  = 10 * time.Second
	actionTimeout = 90 * time.Second
)

// turnSource is where the message came from that the turn running this command
// answers. The server puts it into the turn's environment, every process under
// it inherits it, and the two commands that create a job hand it on, so a
// report ends up where the work was asked for. Unset means the browser, which
// is also what a command typed by hand is.
func turnSource() string { return assistant.Source(os.Getenv(assistant.TurnSourceEnv)) }

func newSendCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "coder-send-prompt <terminal> <text>",
		Short: "Send a prompt to a running coder",
		Long: "Send text to a coder the cockpit runs, the way the prompt box does: " +
			"the text is typed into the session and submitted. The terminal is a coder id " +
			"from `status`.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSend(cmd.OutOrStdout(), *opts, args[0], strings.Join(args[1:], " "))
		},
	}
}

func runSend(out io.Writer, opts inspectOptions, target, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("nothing to send")
	}
	return postInput(out, opts, target, []map[string]string{{"prompt": text}}, "sent to "+target)
}

func newKeysCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "coder-send-control-keys <terminal> <key> [<key>...]",
		Short: "Press keys in a running coder",
		Long: "Press named keys in a coder the cockpit runs, in the order given. This is how a " +
			"dialog is answered: a chooser takes keys, and text sent to it lands in the chooser as " +
			"text instead of choosing anything. Known keys are arrow-up, arrow-down, arrow-left, " +
			"arrow-right, enter, escape, tab, space, backspace, page-up and page-down, and ctrl-, " +
			"alt- and meta- in front of them. Read the screen with `terminal-screen` first: " +
			"the marked option is where the cursor stands, and the keys count from there.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeys(cmd.OutOrStdout(), *opts, args[0], args[1:])
		},
	}
}

func runKeys(out io.Writer, opts inspectOptions, target string, keys []string) error {
	items := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			items = append(items, map[string]string{"control": key})
		}
	}
	if len(items) == 0 {
		return errors.New("nothing to press")
	}
	// One request for the whole sequence, so the keys arrive in this order and
	// nothing of the person's own typing can land between two of them.
	return postInput(out, opts, target, items, fmt.Sprintf("pressed %s in %s", strings.Join(keys, " "), target))
}

// postInput sends one batch of input the way the prompt box does.
func postInput(out io.Writer, opts inspectOptions, target string, items []map[string]string, done string) error {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	if _, err := client.PostJSON("/coders/"+target+"/input", map[string]any{"items": items}, inputTimeout); err != nil {
		return err
	}
	fmt.Fprintln(out, done)
	return nil
}

func newResumeCoderCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "coder-resume <terminal>",
		Short: "Bring a stopped coder session back",
		Long: "Resume a coder session that is not running any more. `status` lists " +
			"those as resumable: their transcript is still there, only the terminal is gone. " +
			"The session comes back with its history, and this prints the identifier `coder-send-prompt` " +
			"takes, which is what a follow up needs. A session that is already running is " +
			"answered with its own identifier, so calling this twice costs nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResumeCoder(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runResumeCoder(out io.Writer, opts inspectOptions, target string) error {
	resumed, err := postCoderAction(opts, target, "resume")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "coder %s resumed in %s\n", text(resumed["id"]), text(resumed["project"]))
	return nil
}

func newStopCoderCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "coder-stop <terminal>",
		Short: "Stop a running coder session, keeping it resumable",
		Long: "Stop a coder session the way the stop button does: the terminal goes away, the " +
			"session and its transcript stay, and `coder-resume` brings it back under the same " +
			"identifier. Use it when a coder is done or is running on something the user does " +
			"not want any more. A job steering this coder ends with it, because a stopped " +
			"coder cannot report again.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStopCoder(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runStopCoder(out io.Writer, opts inspectOptions, target string) error {
	stopped, err := postCoderAction(opts, target, "stop")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "coder %s stopped, it can be resumed\n", text(stopped["name"]))
	return nil
}

func newDeleteCoderCommand(opts *inspectOptions) *cobra.Command {
	var confirmed bool
	cmd := &cobra.Command{
		Use:   "coder-delete <terminal> --yes",
		Short: "Delete a coder session for good",
		Long: "Delete a coder session: it is stopped if it runs, and its session is removed " +
			"from the coder. There is no way back, the transcript is gone and `coder-resume` cannot " +
			"bring it up again. Use `coder-stop` when the work may still be needed. Because it " +
			"cannot be undone, the call has to say so: `--yes`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeleteCoder(cmd.OutOrStdout(), *opts, args[0], confirmed)
		},
	}
	cmd.Flags().BoolVar(&confirmed, "yes", false, "confirm that this session is removed for good")
	return cmd
}

func runDeleteCoder(out io.Writer, opts inspectOptions, target string, confirmed bool) error {
	// The confirmation is part of the call, not a prompt: this runs unattended
	// as often as not, and a session removed by accident cannot be brought back.
	if !confirmed {
		return errors.New("Deleting a coder cannot be undone. Repeat the call with --yes, or use coder-stop, which keeps the session resumable.")
	}
	deleted, err := postCoderAction(opts, target, "delete")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "coder %s deleted\n", text(deleted["name"]))
	return nil
}

// postCoderAction posts one of the coder routes that take no body, which is
// what resuming, stopping and deleting a session are.
func postCoderAction(opts inspectOptions, target, action string) (map[string]any, error) {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return nil, err
	}
	return client.PostForm("/coders/"+strings.TrimSpace(target)+"/"+action, url.Values{}, actionTimeout)
}

func newCoderCommand(opts *inspectOptions) *cobra.Command {
	var coderID, agent, prompt, doneWhen string
	cmd := &cobra.Command{
		Use:   "coder-new <project> <name>",
		Short: "Start a coder in a project",
		Long: "Start a coder session the same way the new coder form does. The project is " +
			"a name from `status` or an absolute path, the name is what the " +
			"session is called. Prints the identifier, which is what `coder-send-prompt` takes.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNewCoder(cmd.OutOrStdout(), *opts, args[0], args[1], coderID, agent, prompt, doneWhen)
		},
	}
	cmd.Flags().StringVar(&coderID, "coder", "", "which coder answers (default: the cockpit's first installed one)")
	cmd.Flags().StringVar(&agent, "agent", "", "agent the session starts with")
	cmd.Flags().StringVar(&prompt, "prompt", "", "task the coder starts working on")
	cmd.Flags().StringVar(&doneWhen, "done-when", "", "steer the coder until this is true, then report")
	return cmd
}

func runNewCoder(out io.Writer, opts inspectOptions, project, name, coderID, agent, prompt, doneWhen string) error {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	form := url.Values{
		"name":               {strings.TrimSpace(name)},
		"project":            {projectPath(opts.projectsDir, project)},
		"automatic_approval": {"on"},
	}
	if coderID = strings.TrimSpace(coderID); coderID != "" {
		form.Set("coder", coderID)
	}
	if agent = strings.TrimSpace(agent); agent != "" {
		form.Set("agent", agent)
	}
	// The task starts the session instead of being typed into it afterwards, so
	// there is no window in which it can be lost.
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		form.Set("prompt", prompt)
	}
	// The criterion travels with the create request, one call for both: the
	// server checks it before the session exists, so a refused criterion never
	// leaves a running coder without its job, and repeating the command cannot
	// pile up sessions on the same task.
	if doneWhen = strings.TrimSpace(doneWhen); doneWhen != "" {
		form.Set("done_when", doneWhen)
	}
	if source := turnSource(); source != "" {
		form.Set("source", source)
	}
	created, err := client.PostForm("/coders/new", form, actionTimeout)
	if err != nil {
		return err
	}
	id, _ := created["id"].(string)
	if id == "" {
		return errors.New("The cockpit started the coder but did not name it.")
	}
	if prompt != "" {
		fmt.Fprintf(out, "coder %s started in %s, working on the task\n", id, project)
	} else {
		fmt.Fprintf(out, "coder %s started in %s\n", id, project)
	}
	if doneWhen == "" {
		return nil
	}
	// The coder is running either way; a job that could not be attached is a
	// failure of the command, and the output above already said what runs.
	if steerError, _ := created["steerError"].(string); steerError != "" {
		return errors.New(steerError)
	}
	fmt.Fprintf(out, "steering it, %v checks at most\n", created["maxWakes"])
	if notice, _ := created["notice"].(string); notice != "" {
		fmt.Fprintln(out, notice)
	}
	return nil
}

func newSteerCommand(opts *inspectOptions) *cobra.Command {
	var doneWhen, task string
	cmd := &cobra.Command{
		Use:   "coder-steer <terminal>",
		Short: "Steer a coder until a job is done",
		Long: "Steer a running coder: when it reports something, the assistant checks where " +
			"the job stands and tells the user once it is done or stuck. The criterion is " +
			"required and has to be checkable, because that is what decides done. Ten checks " +
			"and eight hours per job, then it stops on its own.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSteer(cmd.OutOrStdout(), *opts, args[0], task, doneWhen)
		},
	}
	cmd.Flags().StringVar(&doneWhen, "done-when", "", "what has to be true for the job to count as done")
	cmd.Flags().StringVar(&task, "task", "", "what the coder was asked to do")
	return cmd
}

func runSteer(out io.Writer, opts inspectOptions, terminal, task, doneWhen string) error {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	steered, err := client.PostForm(assistantJobsPath, url.Values{
		"form":      {"steer"},
		"terminal":  {strings.TrimSpace(terminal)},
		"task":      {strings.TrimSpace(task)},
		"done_when": {strings.TrimSpace(doneWhen)},
		"source":    {turnSource()},
	}, actionTimeout)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "steering %s, %v checks at most\n", text(steered["name"]), steered["maxWakes"])
	if notice, _ := steered["notice"].(string); notice != "" {
		fmt.Fprintln(out, notice)
	}
	return nil
}

func newReleaseCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "coder-release <terminal>",
		Short: "Release a steered coder",
		Long:  "Call a steered job off. The coder keeps running, it just stops waking the assistant.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRelease(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runRelease(out io.Writer, opts inspectOptions, terminal string) error {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	if _, err := client.PostForm(assistantJobsPath, url.Values{
		"form":     {"release"},
		"terminal": {strings.TrimSpace(terminal)},
	}, actionTimeout); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s is released\n", strings.TrimSpace(terminal))
	return nil
}

// assistantJobsPath is where the steered jobs live, the same path the page
// posts to. A job belongs to the assistant, not to one conversation, so this
// command carries no conversation of its own.
const assistantJobsPath = "/assistant/jobs"

func newProjectCommand(opts *inspectOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "project-new <name>",
		Short: "Create a project directory",
		Long: "Create a project the same way the new project form does: a directory under " +
			"the cockpit's projects root, ready for coders and shells.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNewProject(cmd.OutOrStdout(), *opts, args[0])
		},
	}
}

func runNewProject(out io.Writer, opts inspectOptions, name string) error {
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	created, err := client.PostForm("/projects", url.Values{"project_name": {strings.TrimSpace(name)}}, actionTimeout)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "project %s created at %s\n", text(created["name"]), text(created["path"]))
	return nil
}

func newDeleteProjectCommand(opts *inspectOptions) *cobra.Command {
	var confirmed bool
	cmd := &cobra.Command{
		Use:   "project-delete <name> --yes",
		Short: "Delete a project and everything in it",
		Long: "Delete a project the same way the projects page does: its coders and shells " +
			"are stopped and the directory is removed with everything in it. There is no " +
			"undo, so only run this when the user asked for exactly this project. Because it " +
			"cannot be undone, the call has to say so: `--yes`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeleteProject(cmd.OutOrStdout(), *opts, args[0], confirmed)
		},
	}
	cmd.Flags().BoolVar(&confirmed, "yes", false, "confirm that this project is removed for good")
	return cmd
}

func runDeleteProject(out io.Writer, opts inspectOptions, name string, confirmed bool) error {
	// The confirmation is part of the call, not a prompt, for the same reason
	// coder-delete carries it: this runs unattended, and a directory removed by
	// accident cannot be brought back.
	if !confirmed {
		return errors.New("Deleting a project cannot be undone. Repeat the call with --yes if the user asked for exactly this project.")
	}
	client, err := localapi.Dial(opts.stateDir)
	if err != nil {
		return err
	}
	deleted, err := client.PostForm("/projects/delete", url.Values{"project": {strings.TrimSpace(name)}}, actionTimeout)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "project %s deleted\n", text(deleted["name"]))
	return nil
}

// projectPath is what the create form expects: the absolute directory. A name
// is resolved against the projects root, so a caller can say what the user
// says instead of repeating the layout.
func projectPath(projectsDir, project string) string {
	project = strings.TrimSpace(project)
	if project == "" || filepath.IsAbs(project) {
		return project
	}
	return filepath.Join(projectsDir, project)
}

func text(value any) string {
	s, _ := value.(string)
	if s == "" {
		return "?"
	}
	return s
}

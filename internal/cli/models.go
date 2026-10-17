package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/marein/dev-cockpit/internal/localapi"
	"github.com/spf13/cobra"
)

// modelsPath is where a coder's models are read, the list every select is
// built from.
const modelsPath = "/assistants/models"

func newModelListCommand(opts *inspectOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model-list [coder]",
		Short: "List the models and effort levels a coder offers",
		Long: "List the models a coder offers, one per line marked cli or added, or with a " +
			"coder's own word for a list of its own such as ollama, a model that names its own " +
			"effort levels with them in brackets, then the effort levels the coder takes, the " +
			"defaults set for it and the note saying where the CLI's names come from; without " +
			"a coder, every installed one. A name `--model` takes is one of these or spelled " +
			"like one, a level `--effort` takes is one of the levels, for a model that names " +
			"its own one of those. Reads only, changes nothing.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			coderID := ""
			if len(args) == 1 {
				coderID = args[0]
			}
			return runModelList(cmd.OutOrStdout(), *opts, coderID)
		},
	}
	return cmd
}

func runModelList(out io.Writer, opts inspectOptions, coderID string) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	path := modelsPath
	if coderID = strings.TrimSpace(coderID); coderID != "" {
		path += "?coder=" + url.QueryEscape(coderID)
	}
	answer, err := client.GetJSON(path, assistantReadTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatModels(answer))
	return err
}

// formatModels renders the list per coder: a head with the count, one row per
// name with its source in front and its own levels behind where it names any,
// the levels the coder takes, the defaults where any is set, and the coder's
// note as the last line. An empty list says so, so a turn reading nothing
// knows the coder lists nothing rather than that the read failed.
func formatModels(answer map[string]any) string {
	raw, _ := answer["coders"].([]any)
	if len(raw) == 0 {
		return "No coder is installed.\n"
	}
	var b strings.Builder
	for i, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if i > 0 {
			b.WriteString("\n")
		}
		models, _ := m["models"].([]any)
		fmt.Fprintf(&b, "Models of %s (%d)\n", text(m["id"]), len(models))
		if len(models) == 0 {
			b.WriteString("  none, the CLI names none and none was added\n")
		}
		for _, e := range models {
			em, ok := e.(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "  %-6s %s", text(em["source"]), text(em["name"]))
			if levels := words(em["efforts"]); len(levels) > 0 {
				fmt.Fprintf(&b, " (%s)", strings.Join(levels, ", "))
			}
			b.WriteString("\n")
		}
		if levels := words(m["efforts"]); len(levels) > 0 {
			fmt.Fprintf(&b, "  efforts: %s\n", strings.Join(levels, ", "))
		}
		if defaults, ok := m["defaults"].(map[string]any); ok {
			var set []string
			for _, purpose := range []string{"chat", "check", "trigger", "start"} {
				if name, _ := defaults[purpose].(string); name != "" {
					set = append(set, purpose+" "+name)
				}
				if level, _ := defaults[purpose+"Effort"].(string); level != "" {
					set = append(set, purpose+" effort "+level)
				}
			}
			if len(set) > 0 {
				fmt.Fprintf(&b, "  defaults: %s\n", strings.Join(set, ", "))
			}
		}
		if note, _ := m["note"].(string); note != "" {
			fmt.Fprintf(&b, "  %s\n", note)
		}
	}
	return b.String()
}

// words reads a JSON array of strings, dropping what is no string.
func words(raw any) []string {
	list, _ := raw.([]any)
	var out []string
	for _, entry := range list {
		if value, ok := entry.(string); ok && value != "" {
			out = append(out, value)
		}
	}
	return out
}

// resolvedModelsPath is where the calling assistant's own resolutions are
// read.
const resolvedModelsPath = "/assistants/models/resolved"

func newAssistantModelsGetCommand(opts *inspectOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "assistant-models-get",
		Short: "Show which models and efforts your own turns run on",
		Long: "Show which model and which effort your chat turns, your checks and your triggers " +
			"run on where nothing narrower is picked, each with where it comes from: the ring, " +
			"the coder's own default or the CLI's default, and for the checks and the triggers " +
			"whether that is your chat's own reading, same as chat, evaluated when the turn starts. " +
			"The Triggers line is what a trigger without a model or an effort of its own runs on, " +
			"your Triggers pick at the ring where one stands. It reads your own, so it needs --as. " +
			"Reads only, changes nothing; `assistant-models-set` is what moves a pick.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAssistantModelsGet(cmd.OutOrStdout(), *opts)
		},
	}
	return cmd
}

func runAssistantModelsGet(out io.Writer, opts inspectOptions) error {
	client, err := localapi.Dial(opts.stateDir, opts.assistantID)
	if err != nil {
		return err
	}
	answer, err := client.GetJSON(resolvedModelsPath, assistantReadTimeout)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, formatAssistantModels(answer))
	return err
}

// modelsSetArgs are the ring's six picks as flags, each optional: what a
// flag that was typed does is decided by whether it was named, see
// modelsSetForm.
type modelsSetArgs struct {
	chat, checks, triggers                   string
	chatEffort, checksEffort, triggersEffort string
}

func newAssistantModelsSetCommand(opts *inspectOptions) *cobra.Command {
	var a modelsSetArgs
	cmd := &cobra.Command{
		Use:   "assistant-models-set",
		Short: "Move your own picks at the ring",
		Long: "Move the picks at the ring of the assistant running this command, the way the user " +
			"moves them in the ring's menu: only the flags you name change anything, everything " +
			"else stands, and `default` clears a pick back to its empty entry, the chain below it " +
			"for the chat and Same as chat for the checks and the triggers, for a model and an " +
			"effort alike; an empty effort beside a model of its own is the CLI's level. A name " +
			"has to be one the coder takes, out of `model-list` or spelled " +
			"like one, and a refused name changes nothing; a level has to be one the model it runs " +
			"on takes, out of `model-list`, and a refused level changes nothing either. The answer is the sentence the " +
			"ring's save shows, what the chat, the checks " +
			"and the triggers run on afterwards; `assistant-models-get` reads it back with where " +
			"each comes from. It moves your own, so it needs --as.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAssistantModelsSet(cmd.OutOrStdout(), *opts, a, cmd.Flags().Changed)
		},
	}
	cmd.Flags().StringVar(&a.chat, "chat", "", "the model your chat turns run on; default clears the pick")
	cmd.Flags().StringVar(&a.checks, "checks", "", "the model the checks of your steered coders run on; default clears the pick back to Same as chat")
	cmd.Flags().StringVar(&a.triggers, "triggers", "", "the model your triggers without a model of their own run on; default clears the pick back to Same as chat")
	cmd.Flags().StringVar(&a.chatEffort, "chat-effort", "", "the effort your chat turns run at; default clears the pick")
	cmd.Flags().StringVar(&a.checksEffort, "checks-effort", "", "the effort the checks of your steered coders run at; default clears the pick back to Same as chat")
	cmd.Flags().StringVar(&a.triggersEffort, "triggers-effort", "", "the effort your triggers without an effort of their own run at; default clears the pick back to Same as chat")
	return cmd
}

// modelsSetForm is what assistant-models-set posts: form=model with one
// field per named flag, the fields the ring's menu posts, the way
// trigger-edit posts what was typed, so a field nobody named leaves that
// pick standing and `default` posts the empty field that clears it. A call
// that names nothing is refused here: it would move nothing and read as a
// save.
func modelsSetForm(a modelsSetArgs, named func(string) bool) (url.Values, error) {
	form := url.Values{"form": {"model"}}
	for _, flag := range []struct{ name, field, value string }{
		{"chat", "model", a.chat}, {"checks", "check_model", a.checks}, {"triggers", "trigger_model", a.triggers},
		{"chat-effort", "effort", a.chatEffort}, {"checks-effort", "check_effort", a.checksEffort}, {"triggers-effort", "trigger_effort", a.triggersEffort},
	} {
		if named(flag.name) {
			form.Set(flag.field, modelField(flag.value))
		}
	}
	if len(form) == 1 {
		return nil, errors.New("Name what to move: --chat, --checks, --triggers, --chat-effort, --checks-effort or --triggers-effort.")
	}
	return form, nil
}

// runAssistantModelsSet posts the picks to the calling assistant's own path,
// the one the ring's menu posts to, and prints the sentence it answers. The
// path is built from --as and from nothing else, so a turn moves its own
// picks and nobody else's, and without --as there is no path to build.
func runAssistantModelsSet(out io.Writer, opts inspectOptions, a modelsSetArgs, named func(string) bool) error {
	form, err := modelsSetForm(a, named)
	if err != nil {
		return err
	}
	own := strings.TrimSpace(opts.assistantID)
	if own == "" {
		return errors.New("This command moves your own picks, so it needs --as <id>.")
	}
	client, err := localapi.Dial(opts.stateDir, own)
	if err != nil {
		return err
	}
	answer, err := client.PostForm("/assistants/"+own, form, actionTimeout)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, word(answer["message"]))
	return err
}

// formatAssistantModels is three lines, one per purpose: the model and where
// it comes from, Default where the resolution ended at empty, and for the
// checks and the triggers the words same as chat in front of where the chat
// resolved, because that is what an empty pick there stands for; a Triggers
// pick of its own reads as the ring alone, like a Checks pick does. The
// effort follows on the same line, read the same way, where the answer
// carries one.
func formatAssistantModels(answer map[string]any) string {
	var b strings.Builder
	for _, row := range []struct{ label, key string }{{"Chat", "chat"}, {"Checks", "check"}, {"Triggers", "trigger"}} {
		choice, _ := answer[row.key].(map[string]any)
		model, _ := choice["model"].(string)
		if model == "" {
			model = "Default"
		}
		fmt.Fprintf(&b, "%s: %s (%s)", row.label, model, readingWhere(choice["source"], choice["sameAsChat"]))
		if effort, ok := choice["effort"].(map[string]any); ok {
			level, _ := effort["effort"].(string)
			if level == "" {
				level = "Default"
			}
			fmt.Fprintf(&b, ", effort %s (%s)", level, readingWhere(effort["source"], effort["sameAsChat"]))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// readingWhere is where a resolution came from, with same as chat in front
// where it is the chat's own reading.
func readingWhere(source, sameAsChat any) string {
	raw, _ := source.(string)
	where := modelSourceWord(raw)
	if same, _ := sameAsChat.(bool); same {
		where = "same as chat, " + where
	}
	return where
}

// modelSourceWord names a resolution's origin the way the user sees it: the
// ring, the coder's own start default, or the CLI. The Models tab is never
// one, what it holds is copied onto a new assistant and reads as the ring.
func modelSourceWord(source string) string {
	switch source {
	case "pick":
		return "ring"
	case "coder":
		return "coder default"
	}
	return "CLI"
}

// modelField is what a model or an effort flag posts: the value, or the
// empty field for `default`, the word that clears a pick back to the
// default below it, the way `never` clears an expiry.
func modelField(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "default") {
		return ""
	}
	return raw
}

// pickWord is how a started or added line names a model or an effort a flag
// asked for: as the cockpit answered it under key, and only where it differs
// from what would have run without the flag, answered under defaultKey;
// empty otherwise, so a line names what runs and never what was asked for.
func pickWord(asked string, answer map[string]any, key, defaultKey string) string {
	if asked == "" {
		return ""
	}
	on, _ := answer[key].(string)
	if on == "" {
		on = asked
	}
	if without, _ := answer[defaultKey].(string); on == without {
		return ""
	}
	return on
}

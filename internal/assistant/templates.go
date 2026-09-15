package assistant

import (
	"embed"
	"log"
	"strings"
	"text/template"

	"github.com/marein/dev-cockpit/internal/clirun"
)

// The texts the cockpit writes for an assistant live next to the code as
// files, so they read like text: the instruction files and the wrapper of a
// workspace, the prompt of a check and the reports a check ends in. They are
// text/template files, parsed once, and the Go side hands over the data.
//
//go:embed templates/*.tmpl
var templateFS embed.FS

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"quote":        clirun.ShellQuote,
	"trim":         strings.TrimSpace,
	"lines":        lines,
	"oneLine":      oneLine,
	"doneWhenLine": DoneWhenLine,
	"name":         jobName,
}).ParseFS(templateFS, "templates/*.tmpl"))

// render fills one template. The templates are part of the binary and the
// data is typed, so an execution error is a programming error: it is logged,
// and what was rendered up to it is returned rather than nothing at all.
func render(name string, data any) string {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name, data); err != nil {
		log.Printf("assistant: render %s: %v", name, err)
	}
	return b.String()
}

// lines splits a text into its lines, each trimmed. A criterion may be a
// list, one condition per line, and a template asks how many there are.
func lines(text string) []string {
	out := strings.Split(text, "\n")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out
}

// jobName is what a report calls a job: its coder's name, or the terminal
// when nobody named it.
func jobName(job Job) string {
	if job.Name == "" {
		return job.Terminal
	}
	return job.Name
}

package web

import (
	"bytes"
	"encoding/base64"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/askpass"
	"github.com/local/dev-cockpit/internal/git"
)

// gitProxyRequest is one proxied git command line: the arguments exactly as
// they were typed behind `dev-cockpit git`, plus the caller's own working
// directory, which is where git will run.
//
// The caller sends the raw directory and nothing else. It carries no project
// and no projects root: naming a project would be a second way to say where
// the call runs, and a projects root would be a second copy of a value only
// the server is authoritative for, which is exactly where the two disagreed
// the moment one of them spelled it `~/projects`.
type gitProxyRequest struct {
	CWD  string   `json:"cwd"`
	Args []string `json:"args"`
}

// gitProxyNoDir is what a caller reads that sent no usable directory. It is
// the one thing about the working directory this route insists on, because
// everything downstream is built on it.
const gitProxyNoDir = "The request carried no absolute working directory."

// handleGitProxy is the cockpit's git proxy: it runs one git command line in
// the working copy of the project the caller stands in and answers output and
// exit code one to one, base64 so a stream that is not UTF-8 survives the
// JSON. The point of the detour through this server is the askpass bridge
// alone: the call carries the bridge's environment, so a passphrase or
// credential question reaches the browser as the app-wide dialog, and it only
// ever surfaces when git really asks.
//
// It sits on one path (`POST /git`) and asks for no project at all. The
// caller's working directory is the whole of it: git runs there, the dialog
// shows it, and it is what the scope below is derived from. A directory
// outside every project is no error, this is a proxy and not a project
// surface — somebody may keep a checkout in /tmp, and refusing it would only
// send them back to the plain git that cannot ask for the passphrase.
//
// That the directory may be anything is safe for one reason, and it is the
// same reason `CheckProxyArgs` exists: with `-C`, `-c` and `--git-dir`
// refused, the working directory is the only thing that decides where git
// runs. The dialog therefore cannot name one place while the call runs in
// another, which is the divergence a project check used to guard against.
//
// It deliberately takes no gitWrites lock: the caller is a coder on the
// command line, and between a coder's git and the editor's git the
// repository's own index.lock is the only thing on purpose. The bridge still
// refuses a second asking action per scope (promptAction), so two dialogs
// can never interleave; that refusal reads like the editor's.
func (s *Server) handleGitProxy(c *gin.Context) {
	var req gitProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	dir, scope, ok := s.gitProxyScope(req.CWD)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": gitProxyNoDir})
		return
	}
	if err := git.CheckProxyArgs(req.Args); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// The command line and the working copy travel with the bridge: whoever
	// answers a passphrase here is answering for a caller they cannot see,
	// typed in a terminal or by a coding agent, so the dialog shows what is
	// about to run and in which checkout.
	action, prompt, open := s.promptActionCommand(scope, git.Subcommand(req.Args), commandLine(req.Args), dir)
	if !open {
		c.JSON(http.StatusConflict, gin.H{"error": gitInUse})
		return
	}
	if action != nil {
		defer action.End()
		// The request context is read here and not inside the watcher: gin
		// recycles its Context once the handler returned.
		defer endWhenCallerGone(action, c.Request.Context().Done())()
	}
	repo := git.New(dir)
	if prompt != nil {
		repo = repo.WithPrompt(prompt)
	}
	// The context without the request's cancellation, like every git write: a
	// caller that was killed mid push must not SIGKILL git mid working copy.
	// What ends the call is the runner's own breathing deadline.
	result, err := repo.Exec(gitWriteContext(c), req.Args)
	if err != nil {
		// The runner produced no answer at all: the question window or the
		// budget ran out, or git never ran. A cancelled question names itself.
		log.Printf("git proxy %s %s: %v", git.Subcommand(req.Args), dir, err)
		c.JSON(http.StatusConflict, gin.H{"error": promptRefusal(action, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"exitCode": result.ExitCode,
		"stdout":   base64.StdEncoding.EncodeToString(result.Stdout),
		"stderr":   base64.StdEncoding.EncodeToString(cancelNote(action, result)),
	})
}

// gitProxyScope answers the directory git will run in and the name everything
// per-place hangs on: the dialog's label, the bridge that lets one asking
// action through at a time, and the notification target.
//
// A directory inside a project is scoped by the project name, which is what a
// person reads in the dialog and in the notification. Anything else is scoped
// by its own absolute path, so a checkout in /tmp gets the same guarantees
// under a name that still says where it is. The two can never collide: a
// project name is a single path segment, a path scope starts with a separator.
//
// The resolution is the repository's own rule (`ProjectNameFor`, the first
// segment below the configured root), so a directory is read exactly the way
// every other surface reads it. The second attempt is for a root reached
// through a symlink: the repository resolves its own root (`EnsureRoot`),
// while a caller's working directory is whatever its shell handed it.
func (s *Server) gitProxyScope(cwd string) (dir, scope string, ok bool) {
	dir = strings.TrimSpace(cwd)
	if dir == "" || !filepath.IsAbs(dir) {
		return "", "", false
	}
	dir = filepath.Clean(dir)
	if name := s.projects.ProjectNameFor(dir); name != "" {
		return dir, name, true
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		if name := s.projects.ProjectNameFor(resolved); name != "" {
			return dir, name, true
		}
	}
	return dir, dir, true
}

// endWhenCallerGone takes the standing question away once the caller waiting
// on it is gone, and answers the function that stops watching.
//
// A proxied question belongs to that one caller and nobody else can answer for
// them: a coder that pressed Ctrl-C is gone together with the terminal the
// answer was for. Ending the action denies the question, so ssh fails in its
// own words and git exits, and it frees the project's bridge right away
// instead of holding it for the two minutes a person would have had — minutes
// in which every editor write on that working copy reads the busy refusal for
// a question nobody is behind any more.
//
// It is the question that is dropped, not the operation: the git process runs
// on its own context to its own end like every write, see gitWriteContext.
//
// The editor's routes deliberately do the opposite and keep their question
// when their page goes away. There the dialog is app-wide, the page may have
// been reloaded or updated away, and another device may still answer it.
func endWhenCallerGone(action *askpass.Action, gone <-chan struct{}) func() {
	stop := make(chan struct{})
	go func() {
		select {
		case <-gone:
			action.End()
		case <-stop:
		}
	}()
	return func() { close(stop) }
}

// maxCommandLine bounds what the dialog is asked to render. The arguments are
// the caller's and can be as long as a commit message, while this is one line
// somebody reads to decide whether to hand over a passphrase.
const maxCommandLine = 300

// commandLine spells the proxied call the way it will run, for the person who
// has to recognise it in the dialog. Nothing here is ever parsed back, this is
// text to read, and the cut at maxCommandLine never hides the subcommand: it
// is the first word behind "git", the route saw to that.
func commandLine(args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "git")
	for _, arg := range args {
		parts = append(parts, readableArg(arg))
	}
	line := strings.Join(parts, " ")
	if runes := []rune(line); len(runes) > maxCommandLine {
		return string(runes[:maxCommandLine]) + "…"
	}
	return line
}

// readableArg is one argument as the dialog may show it. An ordinary one
// stays as it is, everything else is quoted: an argument holding a space reads
// as the one argument it is instead of falling apart into several, and one
// holding a line break stays one line.
//
// The line break is the reason this asks what an argument is made of instead
// of listing the few characters that need quoting. The dialog renders this
// block line by line, so an argument carrying a newline can write lines of its
// own into it — another `cwd:`, another `$ git …` — and forge the picture the
// person is reading to decide whether to hand over a passphrase. Quoting
// escapes it, and the same holds for every other control character.
func readableArg(arg string) string {
	if arg == "" {
		return `""`
	}
	for _, r := range arg {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) || r == '"' || r == '\'' {
			return strconv.Quote(arg)
		}
	}
	return arg
}

// cancelNote is the honest half of a refused question. Cancelling denies the
// helper, so ssh or git fails on its own and the proxy answers that failure
// one to one, which is git's words about a key it could not use and says
// nothing about the person who pressed cancel. The editor's writes get the
// same sentence appended to their error (promptRefusal); here it joins
// stderr, because a proxied call fails through its exit code and stderr is
// where the caller reads why.
//
// Only on a failing call: a question that was cancelled while git got through
// anyway did not stop anything, and saying so would be the lie the other way
// round.
func cancelNote(action *askpass.Action, result git.ExecResult) []byte {
	if result.ExitCode == 0 || action == nil || !action.Cancelled() {
		return result.Stderr
	}
	note := "dev-cockpit: the question was cancelled.\n"
	if len(result.Stderr) > 0 && !bytes.HasSuffix(result.Stderr, []byte("\n")) {
		note = "\n" + note
	}
	return append(result.Stderr, note...)
}

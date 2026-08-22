package web

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/terminalstate"
)

// The input routes are shared: a browser types into them and the assistant
// sends through the local socket into the same handler. When the terminal is
// gone, both have to be told why, and neither is served by the bare lookup
// error: it says a session was not found and not whether the session is merely
// stopped and can be brought back.
//
// So the id is classified once (internal/terminalstate) and worded per caller.
// The local caller acts through commands, so it gets the command that brings
// the session back; the browser gets browser words and never a command, because
// a person on a phone cannot type one.

// coderNotRunning answers a coder input that could not be delivered.
func (s *Server) coderNotRunning(c *gin.Context, id string) {
	s.answerNotRunning(c, id, terminalstate.Classify(id, s.coderLookups(), s.shells))
}

// shellNotRunning answers a shell input that could not be delivered. The route
// already knows it was asked about a shell, which is what tells a shell that
// ended from an id nobody knows.
func (s *Server) shellNotRunning(c *gin.Context, id string) {
	s.answerNotRunning(c, id, terminalstate.ClassifyShell(id, s.coderLookups(), s.shells))
}

func (s *Server) answerNotRunning(c *gin.Context, id string, state terminalstate.State) {
	message := notRunningMessage(id, state, s.localCall(c))
	// Same rule every other refusal follows (see renderError): JSON for a caller
	// that asked for JSON, so the CLI prints the sentence as it stands, plain
	// text otherwise. Both reach the toast, @dc/toast reads either shape.
	if wantsJSON(c.Request) {
		c.JSON(http.StatusGone, gin.H{"error": message})
		return
	}
	c.String(http.StatusGone, message)
}

// notRunningMessage words one classified id for whoever asked. Local is the
// assistant on the socket: it names the command, and it names the id, because
// it holds several terminals at once. The browser is a person looking at the
// terminal, so its wording carries neither.
func notRunningMessage(id string, state terminalstate.State, local bool) string {
	if local {
		switch state {
		case terminalstate.Running:
			return fmt.Sprintf("Terminal %q is running, but the input did not reach it. Send it again.", id)
		case terminalstate.Resumable:
			return fmt.Sprintf("Coder %q is not running. `coder-resume %s` brings the session back, then send again.", id, id)
		case terminalstate.ShellGone:
			return fmt.Sprintf("Shell %q is not running. A shell cannot be resumed.", id)
		default:
			return fmt.Sprintf("No terminal %q is running, and no session with that id can be resumed.", id)
		}
	}
	switch state {
	case terminalstate.Running:
		return "The terminal is running, but the input did not reach it. Try again."
	case terminalstate.Resumable:
		return "This coder is not running. Resume it from the projects page to keep working in it."
	case terminalstate.ShellGone:
		return "This shell is not running any more. A shell cannot be resumed."
	default:
		return "This terminal is not running any more, and there is no session to resume."
	}
}

// coderLookups hands the coder managers to the classifier, which reads two of
// their methods and nothing else.
func (s *Server) coderLookups() []terminalstate.CoderLookup {
	out := make([]terminalstate.CoderLookup, 0, len(s.coders))
	for i := range s.coders {
		out = append(out, s.coders[i])
	}
	return out
}

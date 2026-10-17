package web

import (
	"io"
	"log"
	"regexp"

	"github.com/gin-gonic/gin"
)

// shareTokenInLog is the address of a shared link anywhere in a log line: a
// request's path, a query that names a page, a full URL in a header, a value
// relative to it, as written or URL encoded, its slashes %2F in either case
// and encoded again.
var shareTokenInLog = regexp.MustCompile(`(?i)((?:%(?:25)*2f|\b)share(?:/|%(?:25)*2f))[a-z0-9_-]+`)

// redactShareTokens cuts the token of a shared link out of a log line: the
// token opens the link for whoever reads the log.
func redactShareTokens(p []byte) []byte {
	return shareTokenInLog.ReplaceAll(p, []byte("${1}…"))
}

// redactingWriter cuts the tokens out of everything the request logging
// writes.
type redactingWriter struct{ out io.Writer }

func (w redactingWriter) Write(p []byte) (int, error) {
	if _, err := w.out.Write(redactShareTokens(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// redactStandardLog sends every line of the standard logger through the
// redaction too: a handler's line may name what a request asked for, and the
// HTTP server writes its own errors there. Once per process, however many
// servers stand.
func redactStandardLog() {
	if _, ok := log.Writer().(redactingWriter); !ok {
		log.SetOutput(redactingWriter{log.Writer()})
	}
}

// logRequestError logs what went wrong with a request under its address,
// which on a guest's route carries the token the standard log cuts out.
func logRequestError(c *gin.Context, err error) {
	log.Print(c.Request.Method + " " + c.Request.URL.Path + ": " + err.Error())
}

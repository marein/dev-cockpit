package web

import (
	"io"
	"log"
	"regexp"

	"github.com/gin-gonic/gin"
)

// shareTokenInLog is the address of a shared link where a log line carries
// it: quoted in the access log, after the method in a recovery's request dump
// or in a request's error line.
var shareTokenInLog = regexp.MustCompile(`([\s"])` + regexp.QuoteMeta(sharePathPrefix) + `[A-Za-z0-9_-]+`)

// redactShareTokens cuts the token of a shared link out of a log line: the
// token opens the link for whoever reads the log.
func redactShareTokens(p []byte) []byte {
	return shareTokenInLog.ReplaceAll(p, []byte("${1}"+sharePathPrefix+"…"))
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

// logRequestError logs what went wrong with a request under its address,
// which on a guest's route carries the token.
func logRequestError(c *gin.Context, err error) {
	log.Print(string(redactShareTokens([]byte(c.Request.Method + " " + c.Request.URL.Path + ": " + err.Error()))))
}

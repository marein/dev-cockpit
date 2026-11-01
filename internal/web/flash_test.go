package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// Every flash fits the session cookie next to the longest values it rides
// with, however long the message and whatever runes it holds, cut on a rune;
// uncut, the same message would not.
func TestEveryFlashFitsTheSessionCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	long := strings.Repeat("\U00100000", 4*maxFlashBytes)
	save := func(put func(ginsessions.Session)) (*httptest.ResponseRecorder, error, string) {
		var err error
		var kept string
		r := gin.New()
		r.Use(ginsessions.Sessions("dev-cockpit-session", cookie.NewStore([]byte(strings.Repeat("k", 64)))))
		r.GET("/", func(c *gin.Context) {
			sess := ginsessions.Default(c)
			sess.Set(sessionUserKey, strings.Repeat("u", 64))
			sess.Set(csrfTokenKey, randomURLToken())
			sess.Set(flashProjectKey, strings.Repeat("p", 255))
			put(sess)
			kept, _ = sess.Get(flashMessageKey).(string)
			err = sess.Save()
			c.Status(http.StatusNoContent)
		})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec, err, kept
	}
	for _, message := range []string{long, strings.Repeat("a", 10*maxFlashBytes), "a" + long} {
		for _, put := range []func(ginsessions.Session){
			func(sess ginsessions.Session) { setFlash(sess, message, "") },
			func(sess ginsessions.Session) { setFlash(sess, "", message) },
			func(sess ginsessions.Session) { putFlash(sess, message, "info") },
		} {
			rec, err, kept := save(put)
			if err != nil || rec.Header().Get("Set-Cookie") == "" {
				t.Fatalf("a flash of %d bytes did not fit: %v", len(message), err)
			}
			if len(kept) > maxFlashBytes || !utf8.ValidString(kept) || !strings.HasSuffix(kept, "…") || !strings.HasPrefix(message, strings.TrimSuffix(kept, "…")) {
				t.Fatalf("a flash of %d bytes was kept as %d bytes", len(message), len(kept))
			}
		}
	}
	if _, err, _ := save(func(sess ginsessions.Session) { setFlash(sess, "Host saved.", "") }); err != nil {
		t.Fatalf("a short flash did not fit: %v", err)
	}
	if _, err, _ := save(func(sess ginsessions.Session) { sess.Set(flashMessageKey, long[:2*maxFlashBytes]) }); err == nil {
		t.Fatal("the cookie took twice the bound, the bound is not what it takes")
	}
}

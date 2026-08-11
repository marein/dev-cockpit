package web

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/auth"
)

const (
	// The two ceremony keys hold the challenge between the options request and
	// the answer the browser brings back. They live in the session, so they are
	// signed, http only, and gone with the session.
	passkeyLoginKey    = "passkey_login"
	passkeyRegisterKey = "passkey_register"
)

// passkeyOrigin resolves what a passkey ceremony on this request binds to: the
// relying party id and the origin the browser reports. A passkey needs a domain
// name and a secure context, so an address that has neither gets a sentence
// saying so instead of a cryptic browser exception.
func (s *Server) passkeyOrigin(c *gin.Context) (rpID, origin, reason string) {
	rpID = auth.HostRPID(c.Request.Host)
	scheme := "http"
	if requestIsSecure(c) {
		scheme = "https"
	}
	origin = scheme + "://" + c.Request.Host
	switch {
	case rpID == "":
		return "", origin, "Passkeys need a domain name. This cockpit is reached at an IP address, so a browser has nothing to bind a key to."
	case scheme != "https" && rpID != "localhost":
		return rpID, origin, "Passkeys need a secure context. Reach this cockpit over https and try again."
	}
	return rpID, origin, ""
}

func storeCeremony(sess ginsessions.Session, key string, data *auth.Ceremony) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	sess.Set(key, string(raw))
	return sess.Save()
}

// takeCeremony reads the challenge and drops it from the session. It does not
// save, the caller does that once: on the way out a failed attempt burns the
// challenge just like a successful one.
func takeCeremony(sess ginsessions.Session, key string) (auth.Ceremony, bool) {
	raw, _ := sess.Get(key).(string)
	if raw == "" {
		return auth.Ceremony{}, false
	}
	sess.Delete(key)
	var data auth.Ceremony
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return auth.Ceremony{}, false
	}
	return data, true
}

// blockedJSON is respondBlocked for the JSON login routes.
func (s *Server) blockedJSON(c *gin.Context, retry time.Duration) {
	secs := int((retry + time.Second - 1) / time.Second)
	c.Header("Retry-After", strconv.Itoa(secs))
	c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many failed attempts. Try again in " + strconv.Itoa(secs) + " seconds."})
}

// passkeyRefusal turns a refused assertion into one sentence for the login
// page. The detail stays in the log.
func passkeyRefusal(err error) string {
	switch {
	case errors.Is(err, auth.ErrSignCount):
		return "This passkey looks like a copy of another one and was refused."
	case errors.Is(err, auth.ErrUnknownCredential):
		return "This passkey is not registered here."
	default:
		return "The passkey was not accepted."
	}
}

// handlePasskeyLoginOptions hands out the request options and puts the
// challenge into the session. It is reachable without a session, so it counts
// against the same rate limit as the password post: nobody gets to probe the
// credential list for free.
func (s *Server) handlePasskeyLoginOptions(c *gin.Context) {
	ip := c.ClientIP()
	if ok, retry := s.loginLimiter.allow(ip); !ok {
		s.blockedJSON(c, retry)
		return
	}
	rpID, origin, reason := s.passkeyOrigin(c)
	if reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}
	options, ceremony, err := s.auth.BeginLogin(rpID, origin, s.cfg.AuthUsername)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No passkey is registered for this address."})
		return
	}
	if err := storeCeremony(ginsessions.Default(c), passkeyLoginKey, ceremony); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": saveSessionErrorMessage})
		return
	}
	c.JSON(http.StatusOK, options)
}

// handlePasskeyLogin verifies the assertion and, when it holds, writes the one
// session line the cockpit checks. The passkey only proves the identity, it
// never becomes an identity of its own.
func (s *Server) handlePasskeyLogin(c *gin.Context) {
	ip := c.ClientIP()
	if ok, retry := s.loginLimiter.allow(ip); !ok {
		s.blockedJSON(c, retry)
		return
	}
	rpID, origin, reason := s.passkeyOrigin(c)
	if reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}
	sess := ginsessions.Default(c)
	ceremony, ok := takeCeremony(sess, passkeyLoginKey)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The passkey request expired. Try again."})
		return
	}
	credential, counter, err := s.auth.FinishLogin(rpID, origin, s.cfg.AuthUsername, ceremony, c.Request.Body)
	if err != nil {
		log.Printf("passkey login refused from %s: %v", ip, err)
		_ = sess.Save()
		if justBlocked := s.loginLimiter.fail(ip); justBlocked {
			s.blockedJSON(c, s.cfg.LoginRateBlock)
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": passkeyRefusal(err)})
		return
	}
	s.auth.RecordUse(credential.ID, counter, time.Now())
	s.loginLimiter.reset(ip)
	sess.Clear()
	sess.Set(sessionUserKey, s.cfg.AuthUsername)
	if err := sess.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": saveSessionErrorMessage})
		return
	}
	c.JSON(http.StatusOK, gin.H{"location": safeRedirectPath(c.Query("next"))})
}

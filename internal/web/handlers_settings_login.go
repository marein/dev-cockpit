package web

import (
	"log"
	"net/http"
	"strings"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/auth"
	"github.com/local/dev-cockpit/internal/web/render"
)

func (s *Server) handleSettingsLogin(c *gin.Context) {
	host := auth.HostRPID(c.Request.Host)
	_, _, reason := s.passkeyOrigin(c)
	passkeys := make([]render.PasskeyRow, 0)
	for _, credential := range s.auth.Credentials() {
		row := render.PasskeyRow{
			ID:      credential.ID,
			Label:   credential.Label,
			RPID:    credential.RPID,
			Created: credential.CreatedAt.Format("2006-01-02"),
			Current: credential.RPID == host,
		}
		if !credential.LastUsedAt.IsZero() {
			row.LastUsed = credential.LastUsedAt.Format("2006-01-02")
		}
		passkeys = append(passkeys, row)
	}
	c.HTML(http.StatusOK, "settings_login.gohtml", render.SettingsLoginData{
		Page:          s.page(c, "Settings", "settings"),
		SettingsNav:   s.settingsNav("login"),
		Host:          c.Request.Host,
		PasskeyMain:   s.auth.Registered(host),
		Passkeys:      passkeys,
		PasskeyReason: reason,
	})
}

// handlePasskeyRegisterOptions starts the registration ceremony. It sits behind
// requireAuth: a passkey is added from a session that is already signed in,
// which is why a brand new device always starts with username and password.
func (s *Server) handlePasskeyRegisterOptions(c *gin.Context) {
	rpID, origin, reason := s.passkeyOrigin(c)
	if reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}
	options, ceremony, err := s.auth.BeginRegistration(rpID, origin, s.cfg.AuthUsername)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The registration could not be prepared: " + err.Error()})
		return
	}
	if err := storeCeremony(ginsessions.Default(c), passkeyRegisterKey, ceremony); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": saveSessionErrorMessage})
		return
	}
	c.JSON(http.StatusOK, options)
}

// handlePasskeyRegister verifies the attestation and appends the passkey. The
// label comes from the query, typed by the user: guessing it would leave three
// entries called Chrome behind after a year.
func (s *Server) handlePasskeyRegister(c *gin.Context) {
	rpID, origin, reason := s.passkeyOrigin(c)
	if reason != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": reason})
		return
	}
	label := strings.TrimSpace(c.Query("label"))
	if label == "" {
		label = "Passkey"
	}
	if runes := []rune(label); len(runes) > 60 {
		label = string(runes[:60])
	}
	sess := ginsessions.Default(c)
	ceremony, ok := takeCeremony(sess, passkeyRegisterKey)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The registration expired. Start it again."})
		return
	}
	credential, err := s.auth.FinishRegistration(rpID, origin, s.cfg.AuthUsername, label, ceremony, c.Request.Body)
	if err != nil {
		log.Printf("passkey registration refused: %v", err)
		_ = sess.Save()
		c.JSON(http.StatusBadRequest, gin.H{"error": "The passkey was not accepted."})
		return
	}
	s.auth.AddCredential(credential)
	s.anchoredFlashResponse(c, "/settings/login", render.AnchorPasskeys, "Passkey \""+label+"\" added.", "")
}

// handlePasskeyDelete removes one passkey. Already signed in browsers stay
// signed in, the settings page says so.
func (s *Server) handlePasskeyDelete(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))
	if !s.auth.DeleteCredential(id) {
		s.redirectWithAnchoredFlash(c, "/settings/login", render.AnchorPasskeys, "", "That passkey is already gone.")
		return
	}
	s.redirectWithAnchoredFlash(c, "/settings/login", render.AnchorPasskeys, "Passkey removed.", "")
}

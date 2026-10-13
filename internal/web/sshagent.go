package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/sshagent"
	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const (
	sshAgentPath          = "/settings/ssh-agent"
	sshAgentAnchor        = "settings-ssh-agent"
	sshKeyLocalRefusal    = "A question of the SSH agent is only answered in a browser."
	sshAgentLocalRefusal  = "Who may sign with your SSH keys is only changed in a browser."
	maxFingerprintRequest = 100
)

// SetSSHAgent wires the cockpit's ssh agent: a terminal's questions reach the
// browser through the askpass broker, and a key that opened or closed is an
// event the settings page follows. It is set after SetAskpass, the broker has
// to stand first.
func (s *Server) SetSSHAgent(agent *sshagent.Service) {
	s.sshAgent = agent
	agent.SetOnChange(func() { s.bus.Publish(eventbus.Event{Type: "sshagent"}) })
	agent.SetAsker(s.askSSHKey)
}

// askSSHKey asks a terminal's question under a key of its own. It ends when
// the agent gives up on it, the window ran out or the process hung up.
func (s *Server) askSSHKey(ctx context.Context, r sshagent.Request) (string, bool) {
	if s.askpassBroker == nil {
		return "", false
	}
	action := s.askpassBroker.BeginSSHKey(statefile.NewID(), callerLabel(r.Caller))
	if action == nil {
		return "", false
	}
	defer action.End()
	stop := context.AfterFunc(ctx, action.End)
	defer stop()
	return action.Ask(sshKeyQuestion(r))
}

// callerDetail is the first row of an agent's question: the kind of caller
// and the name the person knows it by.
func callerDetail(c sshagent.Caller) askpass.Detail {
	name := c.Name
	if name == "" {
		name = "unnamed"
	}
	switch c.Kind {
	case sshagent.KindUI:
		return askpass.Detail{Label: "Git action", Value: name}
	case sshagent.KindCoder:
		return askpass.Detail{Label: "Coder", Value: name}
	}
	return askpass.Detail{Label: "Shell", Value: name}
}

// callerLabel is the same row as one line, what the notification names.
func callerLabel(c sshagent.Caller) string {
	d := callerDetail(c)
	return d.Label + ": " + d.Value
}

// sshKeyQuestion words a request of the agent for the dialog. The process
// that asked is read from the system and shown as it is, quoted where it
// could otherwise forge lines of the dialog.
func sshKeyQuestion(r sshagent.Request) askpass.Question {
	details := []askpass.Detail{callerDetail(r.Caller)}
	if r.Caller.Project != "" {
		details = append(details, askpass.Detail{Label: "Project", Value: r.Caller.Project})
	}
	details = append(details,
		askpass.Detail{Label: "Key", Value: r.Key.Path},
		askpass.Detail{Label: "Fingerprint", Value: r.Key.Fingerprint},
	)
	q := askpass.Question{
		Kind:    askpass.KindSSHKey,
		Action:  callerLabel(r.Caller),
		Details: details,
		Unlock:  r.Unlock,
		Retry:   r.Retry,
	}
	if r.Unlock {
		q.Prompt = "Enter the passphrase for " + r.Key.Path
	}
	if len(r.Command) > 0 {
		q.Command = argvLine(r.Command)
		if r.Dir != "" {
			q.Dir = readableArg(r.Dir)
		}
	}
	return q
}

// proxyCaller answers the session a proxied git call signs for, by the
// socket its environment names, nil where it names none of this agent's.
func (s *Server) proxyCaller(sock string) *sshagent.Caller {
	if s.sshAgent == nil || sock == "" {
		return nil
	}
	caller, ok := s.sshAgent.SessionCaller(sock)
	if !ok {
		return nil
	}
	return &caller
}

func (s *Server) handleSettingsSSHAgent(c *gin.Context) {
	data := render.SettingsSSHAgentData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("ssh-agent"),
		TTLMinutes:  int(sshagent.TTLOf(s.settings.Get) / time.Minute),
		MaxTTL:      int(sshagent.MaxTTL / time.Minute),
	}
	data.Agent = s.sshAgentView(data.CSRFToken)
	for _, kind := range sshagent.Kinds {
		data.Policies = append(data.Policies, render.SSHAgentPolicy{
			Kind:  string(kind),
			Value: string(sshagent.PolicyOf(s.settings.Get, kind)),
		})
	}
	c.HTML(http.StatusOK, "settings_ssh_agent.gohtml", data)
}

// handleSettingsSSHAgentKeys renders the key list alone for dc-ssh-keys,
// without touching the flash.
func (s *Server) handleSettingsSSHAgentKeys(c *gin.Context) {
	c.HTML(http.StatusOK, "settings_ssh_agent_keys.gohtml", s.sshAgentView(s.csrfToken(c)))
}

func (s *Server) sshAgentView(csrf string) render.SSHAgentView {
	view := render.SSHAgentView{CSRFToken: csrf}
	if s.sshAgent == nil {
		return view
	}
	view.Active = s.sshAgent.Active()
	view.External = s.sshAgent.External()
	for _, k := range s.sshAgent.Status() {
		row := render.SSHAgentKey{
			Path:        k.Path,
			Type:        k.Type,
			Fingerprint: k.Fingerprint,
			Comment:     k.Comment,
			Protected:   k.Protected,
			Open:        k.Open,
		}
		if k.Open {
			row.Expires = k.Expires.UTC().Format(time.RFC3339)
		}
		view.Keys = append(view.Keys, row)
	}
	return view
}

// handleSettingsSSHAgentSave stores the policies and the time, or forgets a
// key. The policies decide who signs without the person, so a local call may
// not change them; forgetting only takes access away and is not refused.
func (s *Server) handleSettingsSSHAgentSave(c *gin.Context) {
	if c.PostForm("form") == "forget" {
		s.forgetSSHKey(c)
		return
	}
	if s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": sshAgentLocalRefusal})
		return
	}
	policies := map[sshagent.Kind]sshagent.Policy{}
	for _, kind := range sshagent.Kinds {
		policy, ok := sshagent.ParsePolicy(c.PostForm("policy_" + string(kind)))
		if !ok {
			s.redirectWithAnchoredFlash(c, sshAgentPath, sshAgentAnchor, "", "Pick allow, ask or deny for every caller.")
			return
		}
		policies[kind] = policy
	}
	ttl, ok := sshagent.ParseTTL(c.PostForm("ttl_minutes"))
	if !ok {
		s.redirectWithAnchoredFlash(c, sshAgentPath, sshAgentAnchor, "",
			"Keep a key open between 1 and "+strconv.Itoa(int(sshagent.MaxTTL/time.Minute))+" minutes.")
		return
	}
	for kind, policy := range policies {
		s.settings.Set(sshagent.PolicySettingKey(kind), string(policy))
	}
	s.settings.Set(sshagent.TTLSettingKey, strconv.Itoa(int(ttl/time.Minute)))
	s.bus.Publish(eventbus.Event{Type: "sshagent"})
	s.redirectWithAnchoredFlash(c, sshAgentPath, sshAgentAnchor, "Settings saved.", "")
}

func (s *Server) forgetSSHKey(c *gin.Context) {
	fingerprint := c.PostForm("fingerprint")
	if s.sshAgent == nil || len(fingerprint) > maxFingerprintRequest || !s.sshAgent.Forget(fingerprint) {
		s.redirectWithAnchoredFlash(c, sshAgentPath, sshAgentAnchor, "", "This key is not open.")
		return
	}
	s.redirectWithAnchoredFlash(c, sshAgentPath, sshAgentAnchor, "The key was forgotten.", "")
}

package web

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/filesystem"
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

// SetSSHAgent wires the cockpit's ssh agent: its questions reach the browser
// through the askpass broker, so do the ones its ssh-agent and ssh-add
// processes ask through SSH_ASKPASS, and a key that opened or closed is an
// event the settings page follows. It is set after SetAskpass, the broker has
// to stand first.
func (s *Server) SetSSHAgent(agent *sshagent.Service) {
	s.sshAgent = agent
	agent.SetOnChange(func() { s.bus.Publish(eventbus.Event{Type: "sshagent"}) })
	agent.SetAsker(s.askSSHKey)
	if s.askpassBroker != nil {
		agent.SetPrompter(s.promptSSHAgent)
	}
}

func (s *Server) promptSSHAgent(answer func(string) ([]byte, bool)) ([]string, func()) {
	env, end := s.askpassBroker.Serve(answer)
	return append(env, "SSH_ASKPASS="+s.askpassScript, "SSH_ASKPASS_REQUIRE=force"), end
}

// askSSHKey asks a question of the agent under a key of its own, each caller
// named by who it runs for where that can be read. Only a question of the UI
// stays off the news, somebody pressed a button for it and looks at the page.
// It ends when the agent gives up on it, the window ran out or the process
// hung up.
func (s *Server) askSSHKey(ctx context.Context, r sshagent.Request) ([]byte, bool) {
	if s.askpassBroker == nil {
		return nil, false
	}
	name, _ := caller(r.Kind)
	action := s.askpassBroker.BeginSSHKey(statefile.NewID(), name, r.Kind != sshagent.KindUI)
	if action == nil {
		return nil, false
	}
	defer action.End()
	stop := context.AfterFunc(ctx, action.End)
	defer stop()
	q := sshKeyQuestion(r)
	chains := make([][]int, len(r.Callers))
	for i, c := range r.Callers {
		chains[i] = c.Chain
	}
	for i, name := range s.callerNames(chains) {
		q.Callers[i].Name = name
	}
	answer, ok := action.Ask(q)
	return []byte(answer), ok
}

// caller names the kind of caller whose socket a question came through, what
// the dialogs and the notification show, with the icon the app draws it by.
func caller(k sshagent.Kind) (name, icon string) {
	switch k {
	case sshagent.KindUI:
		return "Git action", "git-branch"
	case sshagent.KindCoder:
		return "Coder", "robot"
	case sshagent.KindAssistant:
		return "Assistant", "sparkles"
	}
	return "Shell", "terminal-2"
}

// transferHeads name an ssh line by the git transfer it carries, above the
// line in the dialog.
var transferHeads = map[sshagent.Transfer]string{
	sshagent.TransferPush:  "ssh for a git push",
	sshagent.TransferFetch: "ssh for a git fetch, pull or clone",
}

// sshKeyQuestion words a request of the agent for the dialog. The processes
// connected to the socket are read from the system and shown as they are,
// quoted where they could otherwise forge lines of the dialog.
func sshKeyQuestion(r sshagent.Request) askpass.Question {
	name, icon := caller(r.Kind)
	q := askpass.Question{
		Kind:   askpass.KindSSHKey,
		Action: name,
		Icon:   icon,
		Details: []askpass.Detail{
			{Label: "Key", Value: r.Key.Path, As: askpass.AsMono},
			{Label: "Fingerprint", Value: r.Key.Fingerprint, As: askpass.AsMono},
		},
		Unlock: r.Unlock,
		Retry:  r.Retry,
	}
	if r.Unlock {
		q.Prompt = "Enter the passphrase for " + r.Key.Path
	}
	for _, c := range r.Callers {
		process := askpass.Caller{Command: argvLine(c.Command), Head: transferHeads[c.Transfer]}
		if c.Dir != "" {
			process.Dir = readableArg(c.Dir)
		}
		q.Callers = append(q.Callers, process)
	}
	return q
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
	if err := s.sshAgent.Off(); err != nil {
		var (
			refused *filesystem.RefusedError
			pathErr *fs.PathError
		)
		switch {
		case errors.As(err, &refused):
			view.Refused = refused.Dir
		case errors.As(err, &pathErr):
			view.Failure = pathErr.Path + ": " + pathErr.Err.Error()
		default:
			view.Failure = err.Error()
		}
	}
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
			row.LeftMS = (k.Left + time.Millisecond - 1).Milliseconds()
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
			s.redirectWithAnchoredFlash(c, sshAgentPath, sshAgentAnchor, "", "Pick allow, ask or off for every caller.")
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
	if s.sshAgent != nil {
		s.sshAgent.Apply()
	}
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

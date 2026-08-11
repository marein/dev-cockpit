package claude

import (
	"context"
	"encoding/json"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/coderlogin"
)

// WebLogin is the claude side of the browser login: `claude auth login`
// prints an oauth URL and waits for the authorization code pasted back, and
// `claude auth status --json` answers the state machine readable.
func (p *Coder) WebLogin() coderlogin.Login { return claudeLogin{} }

type claudeLogin struct{}

func (claudeLogin) Command() (string, []string) { return "claude", []string{"auth", "login"} }

func (claudeLogin) TakesCode() bool { return true }

// loginURLPattern finds the oauth URL in the login output. The CLI prints it
// as a terminal hyperlink, so escape and bell bytes end the match.
var loginURLPattern = regexp.MustCompile(`https://[^\s\x1b\x07]+`)

// pastePrompt is the line claude waits on. After a wrong code it complains on
// stderr and keeps waiting on the same prompt.
const pastePrompt = "Paste code here"

func (claudeLogin) Read(stdout, stderr string) coderlogin.Reading {
	return coderlogin.Reading{
		URL:     loginURLPattern.FindString(stdout),
		Waiting: strings.Contains(stdout, pastePrompt),
		Note:    coderlogin.LastLine(stderr),
	}
}

// probeTimeout bounds the status probe: it feeds page renders, and a wedged
// CLI must cost a bounded wait there, not a hanging page.
const probeTimeout = 10 * time.Second

func (claudeLogin) Probe() coderlogin.State {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "auth", "status", "--json").Output()
	if err != nil {
		return coderlogin.State{}
	}
	return parseAuthStatus(out)
}

// onboardingFlag is the key claude keeps its first-run answer under. Without
// it an interactive start opens the theme wizard, the terminal half of the
// onboarding the web login replaces; verified against a fresh home directory,
// the flag alone skips the wizard and the session comes up on its task.
const onboardingFlag = "hasCompletedOnboarding"

// LoginCompleted seeds the onboarding flag once the web login went through:
// what the wizard was for, signing in, just happened, so the first terminal
// must not open it. The config is claude's own and rewritten in full, exactly
// like the trust flag next to it; a config that cannot be read is left alone,
// the wizard then simply asks.
func (claudeLogin) LoginCompleted() {
	path, err := configPath()
	if err != nil {
		return
	}
	config, mode, err := readConfig(path)
	if err != nil {
		log.Printf("claude login: onboarding flag not written: %v", err)
		return
	}
	if done, ok := config[onboardingFlag].(bool); ok && done {
		return
	}
	config[onboardingFlag] = true
	if err := writeConfig(path, config, mode); err != nil {
		log.Printf("claude login: onboarding flag not written: %v", err)
	}
}

// parseAuthStatus reads the documented `claude auth status --json` record. A
// shape this version cannot read counts as not logged in, which only costs
// the login button showing; the flow itself never depends on this.
func parseAuthStatus(raw []byte) coderlogin.State {
	var status struct {
		LoggedIn         bool   `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		Email            string `json:"email"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return coderlogin.State{}
	}
	state := coderlogin.State{LoggedIn: status.LoggedIn, Account: status.Email}
	detail := status.AuthMethod
	if status.SubscriptionType != "" {
		if detail != "" {
			detail += ", "
		}
		detail += status.SubscriptionType + " plan"
	}
	state.Detail = detail
	return state
}

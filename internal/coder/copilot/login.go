package copilot

import (
	"os"
	"regexp"
	"strings"

	"github.com/local/dev-cockpit/internal/coderlogin"
)

// WebLogin is the copilot side of the browser login: `copilot login
// --device-code` prints a one-time code plus the device URL and then polls on
// its own until the user authorized in the browser, nothing is pasted back.
func (p *Coder) WebLogin() coderlogin.Login { return copilotLogin{} }

type copilotLogin struct{}

func (copilotLogin) Command() (string, []string) {
	return "copilot", []string{"login", "--device-code"}
}

func (copilotLogin) TakesCode() bool { return false }

// devicePattern reads the one line the device flow prints: "To authenticate,
// visit https://github.com/login/device and enter code XXXX-XXXX".
var devicePattern = regexp.MustCompile(`visit (https://[^\s\x1b\x07]+) and enter code ([A-Z0-9]+(?:-[A-Z0-9]+)*)`)

func (copilotLogin) Read(stdout, _ string) coderlogin.Reading {
	if m := devicePattern.FindStringSubmatch(stdout); m != nil {
		return coderlogin.Reading{URL: m[1], Code: m[2]}
	}
	return coderlogin.Reading{}
}

// tokenVariables are the environment tokens copilot honors instead of a
// stored login, in the CLI's own precedence order.
var tokenVariables = []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}

// Probe answers the login state without running the CLI: copilot has no
// status command, but it honors the environment tokens and records its stored
// logins in its own config, the file the trust handling already reads.
func (copilotLogin) Probe() coderlogin.State {
	for _, name := range tokenVariables {
		if os.Getenv(name) != "" {
			return coderlogin.State{LoggedIn: true, Detail: "Uses the " + name + " token from the environment."}
		}
	}
	path, err := configPath()
	if err != nil {
		return coderlogin.State{}
	}
	_, config, _, err := readConfig(path)
	if err != nil {
		return coderlogin.State{}
	}
	return stateFromConfig(config)
}

// stateFromConfig reads the stored logins out of copilot's config. The
// loggedInUsers list is what a completed login adds to; lastLoggedInUser
// names the account the CLI acts as.
func stateFromConfig(config map[string]any) coderlogin.State {
	users, _ := config["loggedInUsers"].([]any)
	if len(users) == 0 {
		return coderlogin.State{}
	}
	state := coderlogin.State{LoggedIn: true}
	last, _ := config["lastLoggedInUser"].(map[string]any)
	entry, _ := users[0].(map[string]any)
	if last != nil {
		entry = last
	}
	if entry != nil {
		state.Account, _ = entry["login"].(string)
		if host, _ := entry["host"].(string); host != "" && host != "https://github.com" {
			state.Detail = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
		}
	}
	return state
}

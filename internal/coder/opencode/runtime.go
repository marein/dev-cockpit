package opencode

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
)

// createFunc pre-creates one opencode session in workdir, named title, and
// returns its id. cockpitID is written into the session's metadata when it is
// set, which is how a conversation session stays addressable under the id the
// cockpit chose, see assistant.go and session.go.
type createFunc func(workdir, title, cockpitID string) (string, error)

type runtime struct {
	sessions *sessionRepository
	// create is the session pre-creation, injected so tests never spawn a
	// server.
	create createFunc
	// notifyInbox is the directory the injected notification plugin drops
	// its event files into; empty disables the injection, like claude's.
	notifyInbox string
	// ensurePlugin writes that plugin into opencode's config directory,
	// injected so tests never touch the real one.
	ensurePlugin func() error
	// ensureConfig writes the generated terminal theme, its pin config and
	// the tui config, and answers the two paths the environment names, see
	// theme.go; injected like ensurePlugin.
	ensureConfig func() (pin, tui string, err error)
}

// UsesProvidedSessionID is false: opencode mints its own session ids
// (`ses_…`), the TUI's --session flag only resumes ids that exist (verified
// on 1.18.23, an unknown id exits with "Session not found"), and there is no
// flag that could carry a caller's id in.
func (runtime) UsesProvidedSessionID() bool { return false }

// NamesSessions is false: the TUI has no name or title flag either, so a
// session it creates itself carries a placeholder title until the model
// invents one. The promote step therefore matches the fresh session on the
// working directory, see coder.SessionNaming.
func (runtime) NamesSessions() bool { return false }

// Env carries the notify inbox and the terminal theme's pin config into the
// session. Both ride the tmux session environment and nothing else, so an
// opencode the cockpit did not start never sees them: the plugin stays inert
// and the user's own theme stands. The theme files are ensured here because
// the pin path must not travel unless both files landed on the disk, and a
// failed write costs the theming alone, never the session.
func (r runtime) Env() map[string]string {
	env := map[string]string{}
	if r.notifyInbox != "" {
		env[notifyEnv] = r.notifyInbox
	}
	if r.ensureConfig != nil {
		if pin, tui, err := r.ensureConfig(); err == nil {
			env[themeEnv] = pin
			env[tuiEnv] = tui
		} else {
			log.Printf("opencode: the session config could not be written, sessions keep opencode's own: %v", err)
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

// injectNotifyPlugin puts the notification plugin in place for the session
// about to start. A failed write is logged and costs the notifications
// alone, never the session: the plugin is help and no requirement.
func (r runtime) injectNotifyPlugin() {
	if r.notifyInbox == "" || r.ensurePlugin == nil {
		return
	}
	if err := r.ensurePlugin(); err != nil {
		log.Printf("opencode: the notification plugin could not be written: %v", err)
	}
}

// StartCommand builds the interactive session, and which shape it takes
// depends on whether a task travels along, because opencode creates its
// session record lazily and the two needs pull apart:
//
// With a task the TUI is started fresh with --prompt, which its home screen
// submits on mount, so the session record appears at once and the task cannot
// be lost. --prompt is only read by that home screen; combined with --session
// it is dropped (verified on 1.18.23), so a pre-created session cannot carry
// a task.
//
// Without a task nothing would create the session record until the first
// typed message, long after the promote window. So the session is created
// ahead of the start through opencode's own server API, named like the
// coder, and the TUI resumes it with --session. A creation that fails is
// logged and falls back to a plain start: the session then works, only its
// stored record appears late and stays under opencode's own naming.
func (r runtime) StartCommand(start coder.SessionStart) string {
	r.injectNotifyPlugin()
	base := fmt.Sprintf("cd %s && exec opencode%s",
		clirun.ShellQuote(start.Workdir), flags(start.AgentID, start.AutomaticApproval))
	if task := strings.TrimSpace(start.Task); task != "" {
		// The equals form is what keeps a task that starts with a dash text:
		// yargs reads a separate `--prompt -dfoo` as the flag without a value
		// followed by an unknown option, while `--prompt=-dfoo` is
		// unambiguous (verified on 1.18.23). The shell quoting protects the
		// shell, the equals sign protects opencode's parser.
		return base + " --prompt=" + clirun.ShellQuote(task)
	}
	if r.create != nil {
		if id, err := r.create(start.Workdir, start.Name, ""); err == nil {
			return base + " --session " + clirun.ShellQuote(id)
		} else {
			log.Printf("opencode: session could not be created ahead of the start, starting plain: %v", err)
		}
	}
	return base
}

func (r runtime) ResumeCommand(sessionID, workdir string, automaticApproval bool) string {
	r.injectNotifyPlugin()
	return fmt.Sprintf("cd %s && exec opencode%s --session %s",
		clirun.ShellQuote(workdir), flags("", automaticApproval),
		clirun.ShellQuote(r.nativeID(sessionID)))
}

// nativeID maps a session id the cockpit holds onto the id opencode knows.
// They differ only for a conversation handed over to a terminal: its store
// row is listed under the cockpit's id, while --session needs opencode's own.
func (r runtime) nativeID(sessionID string) string {
	if r.sessions == nil {
		return sessionID
	}
	return r.sessions.nativeID(sessionID)
}

func flags(agentID string, automaticApproval bool) string {
	var flags strings.Builder
	if automaticApproval {
		// --auto approves every permission that is not explicitly denied,
		// opencode's own flag for exactly this choice.
		flags.WriteString(" --auto")
	}
	if agentID != "" {
		flags.WriteString(" --agent ")
		flags.WriteString(clirun.ShellQuote(agentID))
	}
	return flags.String()
}

// createSession creates one session through opencode's own server API, the
// only surface that takes a title and metadata at creation (`POST /session`;
// neither the TUI nor `opencode session` can create one). A short-lived
// `opencode serve` is started in the working directory, asked once, and shut
// down again; the session row it leaves behind is ordinary opencode state.
// The server is bound to loopback and locked with a one-time password,
// because an unsecured port, however briefly, is an open API to everything
// the account can do.
func createSession(workdir, title, cockpitID string) (string, error) {
	port, err := freePort()
	if err != nil {
		return "", err
	}
	password, err := oneTimePassword()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("opencode", "serve", "--port", strconv.Itoa(port), "--hostname", "127.0.0.1", "--pure")
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "OPENCODE_SERVER_PASSWORD="+password)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer stopServe(cmd)

	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	if err := awaitServe(client, base, password, 20*time.Second); err != nil {
		return "", err
	}
	return postSession(client, base, password, title, cockpitID)
}

// awaitServe waits until the server answers HTTP at all; which status it
// answers with does not matter, a refused connection is the only "not yet".
func awaitServe(client *http.Client, base, password string, patience time.Duration) error {
	deadline := time.Now().Add(patience)
	for {
		req, err := http.NewRequest(http.MethodGet, base+"/session", nil)
		if err != nil {
			return err
		}
		req.SetBasicAuth("opencode", password)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the opencode server did not come up: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func postSession(client *http.Client, base, password, title, cockpitID string) (string, error) {
	payload := map[string]any{}
	if title = strings.TrimSpace(title); title != "" {
		payload["title"] = title
	}
	if cockpitID != "" {
		payload["metadata"] = map[string]any{metadataKey: cockpitID}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, base+"/session", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("opencode", password)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("session creation answered %d", resp.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(answer, &created); err != nil {
		return "", err
	}
	if _, err := validSessionID(created.ID); err != nil || !strings.HasPrefix(created.ID, "ses") {
		return "", errors.New("session creation answered no usable id")
	}
	return created.ID, nil
}

// stopServe ends the throwaway server politely and then firmly: a TERM first,
// so the process closes its database like any other exit, and a KILL only
// when it does not go.
func stopServe(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("no tcp address")
	}
	return addr.Port, nil
}

func oneTimePassword() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

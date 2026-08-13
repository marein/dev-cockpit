// Package localapi is how a process on this machine reaches the running
// cockpit. The assistant answers from a coder process that has a shell, so it
// could touch the state files directly, and that is exactly what it must not
// do: a state directory belongs to one serve process, and a second writer would
// bypass its caches and its event stream. Instead the server listens on a unix
// socket inside its own state directory, and everything that changes state goes
// through the same HTTP surface a browser uses.
//
// The socket is the whole credential. Opening it means being on this machine
// with permission to enter a directory the server owns, so nothing here mints,
// stores, rotates or compares a token, and nothing is left behind that a later
// process would have to invalidate.
package localapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/local/dev-cockpit/internal/filesystem"
)

// The socket lives in a directory of its own, and that directory carries the
// permission. A socket file gets its mode from the umask at bind time, which
// leaves a window where anybody could connect; a directory nobody else may
// enter closes that window before the socket exists. Both names are short
// because the whole path has to fit, see maxSocketPath.
const (
	socketDir  = "api"
	socketFile = "s"
)

// maxSocketPath is what a unix socket address may be: a fixed size field in the
// kernel, 108 bytes on Linux and 104 on macOS, and the limit is on the path, not
// on the file name. A state directory is wherever the user pointed --state-dir,
// so this is not a limit a caller can be expected to keep to.
const maxSocketPath = 100

// SocketPath is where the cockpit of one state directory listens. It is derived
// from the state directory alone, so a caller needs nothing but the flag it
// already passes, and the server and the command always agree.
//
// A state directory whose path is too long for a socket falls back to a private
// directory in the system temp directory, named after it. Refusing to start over
// the length of a path the user chose for something else would be the worst kind
// of failure: nothing about --state-dir suggests it has a length limit.
func SocketPath(stateDir string) string {
	dir := resolve(stateDir)
	inState := filepath.Join(dir, socketDir, socketFile)
	if len(inState) <= maxSocketPath {
		return inState
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), "dev-cockpit-"+hex.EncodeToString(sum[:8]), socketFile)
}

// resolve spells a state directory the one way both sides have to spell it: a
// command may name it relative or with a ~, the server has already expanded it,
// and two spellings of one directory must not resolve to two sockets. The rule
// lives in filesystem.AbsDir, because the askpass broker derives its own socket
// from the same directory and has to spell it the same way.
func resolve(stateDir string) string {
	return filesystem.AbsDir(stateDir)
}

// Listen binds the socket for one serve process. A socket file a dead process
// left behind is removed first: nothing listens on it, so it can only refuse
// connections.
func Listen(stateDir string) (net.Listener, error) {
	path := SocketPath(stateDir)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// MkdirAll applies the umask and leaves an existing directory alone, so the
	// mode is set explicitly. This is the access rule of the whole local API.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return net.Listen("unix", path)
}

// Client is a caller on this machine. It speaks plain HTTP over the socket, so
// the server sees the requests a browser makes and answers them in the same
// handlers.
type Client struct {
	http *http.Client
}

// dialBudget is how long Dial waits for the cockpit of a state directory to
// answer before it reports that none is running. A self-update replaces the
// process image with syscall.Exec, and between the exec and the new process
// binding the socket the file is briefly gone or refuses connections; a turn
// that calls the CLI inside that window would otherwise fail over nothing a
// caller can act on. The price is accepted: against a cockpit that is really
// off, the clear message arrives after the budget instead of at once.
const (
	dialBudget = 30 * time.Second
	dialPoll   = 250 * time.Millisecond
)

// dialBudgetEnv overrides the budget, the way DEV_COCKPIT_UPDATE_API_URL
// overrides the release feed: a test that proves the "no cockpit" message must
// not sleep the real budget out.
const dialBudgetEnv = "DEV_COCKPIT_DIAL_BUDGET"

func budget() time.Duration {
	if override, err := time.ParseDuration(os.Getenv(dialBudgetEnv)); err == nil && override >= 0 {
		return override
	}
	return dialBudget
}

// Dial returns a client for the cockpit of one state directory. It probes the
// socket and waits out a restart window: a missing socket file and a refused
// connection are retried until the budget is spent, everything else, and the
// spent budget, answer with the message that no cockpit is running.
//
// The waiting lives here and not in the transport's DialContext on purpose.
// Only the connection probe may ever be repeated: a request is one attempt
// with its own timeout, a repeated send would double a prompt. And the request
// timeouts are partly shorter than this budget, so a retry inside the request
// would be cut off before the budget mattered; Dial has no context, the budget
// collides with nothing.
func Dial(stateDir string) (*Client, error) {
	path := SocketPath(stateDir)
	deadline := time.Now().Add(budget())
	for {
		conn, err := net.DialTimeout("unix", path, time.Second)
		if err == nil {
			_ = conn.Close()
			return newClient(path), nil
		}
		retryable := errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
		if !retryable || !time.Now().Before(deadline) {
			return nil, errors.New("No running cockpit was found for this state directory.")
		}
		time.Sleep(dialPoll)
	}
}

func newClient(path string) *Client {
	return &Client{http: &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		},
		// A redirect is the browser's answer; this caller wants the JSON one and
		// would otherwise follow it into a page.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// PostForm posts a form the way the browser does.
func (c *Client) PostForm(path string, form url.Values, timeout time.Duration) (map[string]any, error) {
	return c.request(http.MethodPost, path, "application/x-www-form-urlencoded", []byte(form.Encode()), timeout)
}

// PostJSON posts a JSON body, for the routes the browser reaches with fetch.
func (c *Client) PostJSON(path string, payload any, timeout time.Duration) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return c.request(http.MethodPost, path, "application/json", body, timeout)
}

// GetJSON reads one of the JSON answers, for the routes that only report.
func (c *Client) GetJSON(path string, timeout time.Duration) (map[string]any, error) {
	return c.request(http.MethodGet, path, "", nil, timeout)
}

// request sends one request and reads the JSON a local caller is answered
// with. A refusal carries the same sentence the page would have flashed, so
// the command says what the user would have read.
func (c *Client) request(method, path, contentType string, body []byte, timeout time.Duration) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// The host is a placeholder: the connection is the socket, and every request
	// over it reaches this one cockpit.
	req, err := http.NewRequestWithContext(ctx, method, "http://cockpit"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the cockpit did not answer: %w", err)
	}
	defer res.Body.Close()
	// The bound only guards against a runaway answer. It has to hold every
	// legitimate one whole, so it is megabytes, not kilobytes: an activity
	// reading with --full carries entire messages, and the git proxy's answer
	// carries both capped streams base64 encoded, which is the largest
	// legitimate answer there is. A truncated answer is not JSON any more and
	// reads as a cockpit that answered garbage.
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))

	var answer map[string]any
	_ = json.Unmarshal(raw, &answer)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		if message, _ := answer["error"].(string); strings.TrimSpace(message) != "" {
			return nil, errors.New(message)
		}
		return nil, fmt.Errorf("the cockpit refused the request: %s", firstLine(string(raw)))
	}
	if answer == nil {
		return nil, errors.New("The cockpit answered something this command cannot read.")
	}
	return answer, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

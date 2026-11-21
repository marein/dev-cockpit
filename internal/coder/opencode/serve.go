package opencode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/marein/dev-cockpit/internal/clirun"
)

// serveBoot is how long a throwaway server may take to answer at all.
const serveBoot = 20 * time.Second

// serveAnswerBytes bounds what is read of one answer of the server.
const serveAnswerBytes = 1 << 20

// throwawayServe is one short lived `opencode serve`, the only surface for
// what neither the TUI nor `opencode run` can do: creating a session under a
// title and metadata, and compacting one. It runs in the working directory
// of the session it serves, bound to loopback and locked with a one time
// password, because an unsecured port, however briefly, is an open API to
// everything the account can do. The password lives in this process and the
// server's environment alone.
type throwawayServe struct {
	base     string
	password string
	client   *http.Client
	// end stops the server process, see stopProcess.
	end func()
}

// serveCommand is the server process: loopback only, its password in its
// environment, never on its command line.
func serveCommand(workdir string, port int, password string) *exec.Cmd {
	cmd := exec.Command("opencode", "serve", "--port", strconv.Itoa(port), "--hostname", "127.0.0.1", "--pure")
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "OPENCODE_SERVER_PASSWORD="+password)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd
}

// startServe boots a server in workdir and waits until it answers. The caller
// stops it, a server that never answered is stopped here. It dies with the
// process that starts it, the compaction helper or the cockpit: a starter that
// is killed or restarted never reaches its deferred stop, and a server left
// behind keeps an open port and finishes a compaction the thread calls failed.
func startServe(workdir string) (*throwawayServe, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	password, err := oneTimePassword()
	if err != nil {
		return nil, err
	}
	cmd := serveCommand(workdir, port, password)
	release := clirun.DiesWithParent(cmd)
	if err := cmd.Start(); err != nil {
		release()
		return nil, err
	}
	s := &throwawayServe{
		base:     "http://127.0.0.1:" + strconv.Itoa(port),
		password: password,
		client:   &http.Client{},
		end:      func() { stopProcess(cmd); release() },
	}
	if err := s.await(serveBoot); err != nil {
		s.stop()
		return nil, err
	}
	return s, nil
}

func (s *throwawayServe) stop() {
	if s.end != nil {
		s.end()
	}
}

// await waits until the server answers HTTP at all; which status it answers
// with does not matter, a refused connection is the only "not yet".
func (s *throwawayServe) await(patience time.Duration) error {
	deadline := time.Now().Add(patience)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := s.call(ctx, http.MethodGet, "/session", nil)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the opencode server did not come up: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// call sends one request, with a JSON body when payload is set, and answers
// the status and the start of the body.
func (s *throwawayServe) call(ctx context.Context, method, path string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, body)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth("opencode", s.password)
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, serveAnswerBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, answer, nil
}

// createSession creates one session through opencode's own server API, the
// only surface that takes a title and metadata at creation (`POST /session`;
// neither the TUI nor `opencode session` can create one). The session row the
// server leaves behind is ordinary opencode state.
func createSession(workdir, title, cockpitID string) (string, error) {
	s, err := startServe(workdir)
	if err != nil {
		return "", err
	}
	defer s.stop()
	return postSession(s, title, cockpitID)
}

func postSession(s *throwawayServe, title, cockpitID string) (string, error) {
	payload := map[string]any{}
	if title = strings.TrimSpace(title); title != "" {
		payload["title"] = title
	}
	if cockpitID != "" {
		payload["metadata"] = map[string]any{metadataKey: cockpitID}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, answer, err := s.call(ctx, http.MethodPost, "/session", payload)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("session creation answered %d", status)
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

// stopProcess ends the throwaway server politely and then firmly: a TERM
// first, so the process closes its database like any other exit, and a KILL
// only when it does not go.
func stopProcess(cmd *exec.Cmd) {
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

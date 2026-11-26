package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// A name is written the way codex's own /rename writes it, through the
// thread/name/set call of its app server, started for this one call over
// stdio. The index file is codex's, the cockpit never appends to it itself.
//
// A session for an assistant is made the same way, through thread/start:
// `codex exec` takes neither an id nor a name, and a turn has to know both
// before it runs, see startThread.

const (
	nameTimeout   = 20 * time.Second
	nameLineLimit = 1 << 20
	nameLines     = 200
	exitGrace     = 5 * time.Second
)

type rpcMessage struct {
	ID     *int            `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params any             `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func nameThread(sessionID, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), nameTimeout)
	defer cancel()
	return overAppServer(ctx, func(w io.Writer, r io.Reader) error {
		return nameOver(w, r, sessionID, name)
	})
}

// startThread makes the session an assistant's turns run in and answers the
// id codex minted for it (verified on 0.172.0: the rollout stands once the
// call answered, before any message). cockpitID is the id the cockpit
// addresses the session by, kept in its thread source, see cockpitSource.
func startThread(workdir, name, cockpitID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), nameTimeout)
	defer cancel()
	id := ""
	err := overAppServer(ctx, func(w io.Writer, r io.Reader) (err error) {
		id, err = startOver(w, r, workdir, name, cockpitID)
		return err
	})
	return id, err
}

func overAppServer(ctx context.Context, talk func(w io.Writer, r io.Reader) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server")
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	err = talk(stdin, stdout)
	// The server ends on its own once its input closes (0.172.0: within a
	// tenth of a second), after it wrote down what the talk changed; a kill
	// right away could cut the end of the session's record.
	_ = stdin.Close()
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(exitGrace):
		cancel()
		<-exited
	}
	return err
}

func handshake(w io.Writer, r io.Reader) (*bufio.Reader, error) {
	client := map[string]string{"name": "dev-cockpit", "title": "dev-cockpit", "version": "1"}
	reader := bufio.NewReaderSize(r, 64<<10)
	if _, err := call(w, reader, 0, "initialize", map[string]any{"clientInfo": client}); err != nil {
		return nil, err
	}
	return reader, send(w, rpcMessage{Method: "initialized"})
}

func nameOver(w io.Writer, r io.Reader, sessionID, name string) error {
	reader, err := handshake(w, r)
	if err != nil {
		return err
	}
	_, err = call(w, reader, 1, "thread/name/set", map[string]string{"threadId": sessionID, "name": name})
	return err
}

func startOver(w io.Writer, r io.Reader, workdir, name, cockpitID string) (string, error) {
	reader, err := handshake(w, r)
	if err != nil {
		return "", err
	}
	result, err := call(w, reader, 1, "thread/start", map[string]string{"cwd": workdir, "threadSource": cockpitSource + cockpitID})
	if err != nil {
		return "", err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &started) != nil || !sessionIDPattern.MatchString(started.Thread.ID) {
		return "", errors.New("codex thread/start: no thread id")
	}
	if name == "" {
		return started.Thread.ID, nil
	}
	_, err = call(w, reader, 2, "thread/name/set", map[string]string{"threadId": started.Thread.ID, "name": name})
	return started.Thread.ID, err
}

func send(w io.Writer, msg rpcMessage) error {
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = w.Write(append(line, '\n'))
	return err
}

func call(w io.Writer, r *bufio.Reader, id int, method string, params any) (json.RawMessage, error) {
	if err := send(w, rpcMessage{ID: &id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	for range nameLines {
		line, err := readLine(r, nameLineLimit)
		if err != nil {
			return nil, err
		}
		var answer rpcMessage
		if json.Unmarshal(line, &answer) != nil || answer.ID == nil || *answer.ID != id || answer.Method != "" {
			continue
		}
		if answer.Error != nil {
			return nil, fmt.Errorf("codex %s: %s", method, clip(answer.Error.Message))
		}
		return answer.Result, nil
	}
	return nil, fmt.Errorf("codex %s: no answer", method)
}

// readLine reads one line and drops what a line holds beyond the limit, so
// a line that long no longer parses and its reader skips it. The last line
// of a source comes with the error that ended it.
func readLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(line) < limit {
			line = append(line, part...)
		}
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

func clip(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 300 {
		return message[:300]
	}
	return message
}

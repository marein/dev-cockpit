package term

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
	xterm "golang.org/x/term"
)

// detachLead is the escape byte (Ctrl-\) that introduces an attach command:
// `Ctrl-\ d` detaches (the session keeps running); `Ctrl-\ Ctrl-\` sends a
// literal Ctrl-\.
const detachLead = 0x1c

// Attach connects the local terminal to a running session, like `tmux attach`.
// It blocks until the user detaches or the session ends.
func Attach(provider, key string) error {
	dir, err := RuntimeDir(provider)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", socketPath(dir, key))
	if err != nil {
		return fmt.Errorf("Session %q is not running.", key)
	}
	defer conn.Close()

	fd := int(os.Stdin.Fd())
	if !xterm.IsTerminal(fd) {
		return fmt.Errorf("attach requires an interactive terminal")
	}
	oldState, err := xterm.MakeRaw(fd)
	if err != nil {
		return err
	}
	var restoreOnce sync.Once
	restore := func() { restoreOnce.Do(func() { _ = xterm.Restore(fd, oldState) }) }
	defer restore()

	fmt.Fprintf(os.Stderr, "[attached to %s — detach with Ctrl-\\ d]\r\n", key)

	sendSize := func() {
		if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil {
			_ = writeFrame(conn, frameResize, resizePayload(int(ws.Col), int(ws.Row)))
		}
	}
	sendSize()
	_ = writeFrame(conn, frameRedraw, nil)

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			sendSize()
		}
	}()

	go func() {
		for {
			typ, payload, err := readFrame(conn)
			if err != nil {
				restore()
				fmt.Fprintf(os.Stderr, "\r\n[session %s ended]\r\n", key)
				os.Exit(0)
			}
			if typ == frameOutput {
				_, _ = os.Stdout.Write(payload)
			}
		}
	}()

	return copyInput(conn)
}

// copyInput forwards raw stdin to the session, honouring the detach escape.
func copyInput(conn net.Conn) error {
	buf := make([]byte, 4096)
	escaped := false
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			out := make([]byte, 0, n)
			for _, b := range buf[:n] {
				if escaped {
					escaped = false
					switch b {
					case 'd':
						if len(out) > 0 {
							_ = writeFrame(conn, frameInput, out)
						}
						return nil
					case detachLead:
						out = append(out, detachLead)
					default:
						out = append(out, detachLead, b)
					}
					continue
				}
				if b == detachLead {
					escaped = true
					continue
				}
				out = append(out, b)
			}
			if len(out) > 0 {
				if err := writeFrame(conn, frameInput, out); err != nil {
					return nil
				}
			}
		}
		if err != nil {
			return nil
		}
	}
}

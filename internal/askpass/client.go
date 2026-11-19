package askpass

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// askBudget bounds the helper's wait as a last resort. The broker answers
// every question eventually — the action's own watchdog sees to that — so
// this only catches a broker that died mid-question.
const askBudget = 15 * time.Minute

// Helper is the whole askpass helper: the token and the socket come from its
// environment, the prompt line from its arguments, the answer goes to out.
// It answers false for a denial, which the helper turns into its exit code
// without a word of its own: whatever it wrote would end up in git's or ssh's
// message as if they had said it.
func Helper(args []string, out io.Writer) bool {
	socket := os.Getenv("DC_ASKPASS_SOCKET")
	token := os.Getenv("DC_ASKPASS_TOKEN")
	if socket == "" || token == "" {
		return false
	}
	answer, err := Ask(socket, token, strings.TrimSpace(strings.Join(args, " ")))
	if err != nil {
		return false
	}
	_, err = fmt.Fprintln(out, answer)
	return err == nil
}

// Ask is the helper's one move: report the prompt over the broker's socket
// and block until the person answered, was denied, or the action ended. It
// is what the hidden askpass command runs, and what a test stands in for a
// real helper with.
func Ask(socket, token, prompt string) (string, error) {
	client := &http.Client{
		Timeout: askBudget,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}
	body, err := json.Marshal(askRequest{Token: token, Prompt: prompt})
	if err != nil {
		return "", err
	}
	res, err := client.Post("http://askpass/ask", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("denied (%d)", res.StatusCode)
	}
	var got struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		return "", err
	}
	return got.Answer, nil
}

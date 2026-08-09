package askpass

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// askBudget bounds the helper's wait as a last resort. The broker answers
// every question eventually — the action's own watchdog sees to that — so
// this only catches a broker that died mid-question.
const askBudget = 15 * time.Minute

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

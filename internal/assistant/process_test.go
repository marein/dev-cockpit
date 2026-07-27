package assistant

import "testing"

// The provider failure the user can act on is the one worth naming: a CLI that
// was never logged in on this machine. Everything else stays generic.
func TestLooksLikeLogin(t *testing.T) {
	yes := []string{
		"Invalid API key · Please run /login",
		"You are not logged in. Run copilot and sign in.",
		"Error: authentication required",
		"HTTP 401 Unauthorized",
	}
	for _, line := range yes {
		if !LooksLikeLogin(line) {
			t.Errorf("expected a login hint for %q", line)
		}
	}
	no := []string{
		"panic: runtime error: index out of range",
		"error: max tokens exceeded",
		"tool use failed: permission denied for /etc/shadow",
		"",
	}
	for _, line := range no {
		if LooksLikeLogin(line) {
			t.Errorf("unexpected login hint for %q", line)
		}
	}
}

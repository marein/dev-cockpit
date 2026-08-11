package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The bot token stands in every API path, so a transport error carries it in
// the URL it failed on. Everything this package produces goes through redact,
// and these checks are what keeps that true.

func TestATransportErrorCarriesNoToken(t *testing.T) {
	// A port nobody listens on: the error is a *url.Error, and its message is
	// the whole URL, token and all.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	channel := &Channel{base: url}
	_, err := channel.client(testToken).getUpdates(context.Background(), 0, 0)
	if err == nil {
		t.Fatal("a dead server answered")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("the token is in the error: %s", err)
	}
	if !strings.Contains(err.Error(), "<token>") {
		t.Fatalf("the error does not show that something was taken out: %s", err)
	}
}

func TestARefusalCarriesNoToken(t *testing.T) {
	// Telegram echoing the token back in its description is the case a plain
	// "print what the API said" would leak.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"ok":          false,
			"error_code":  401,
			"description": "Unauthorized: bot" + testToken + " is not a bot",
		})
	}))
	defer server.Close()

	channel := &Channel{base: server.URL}
	_, err := channel.client(testToken).getUpdates(context.Background(), 0, 0)
	if err == nil {
		t.Fatal("the refusal was not reported")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("the token is in the error: %s", err)
	}
	if apiCode(err) != http.StatusUnauthorized {
		t.Fatalf("the refusal lost its code: %v", err)
	}
}

func TestNothingThePollerLogsCarriesTheToken(t *testing.T) {
	logs := captureLog(t)
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.description = "Unauthorized: bot" + testToken + " went bad"
	tc.api.refuse(http.StatusInternalServerError, http.StatusUnauthorized)
	tc.Start()

	waitFor(t, "the poller to stop", func() bool { return tc.Status().State == StateStopped })
	time.Sleep(50 * time.Millisecond)
	if strings.Contains(logs.String(), testToken) {
		t.Fatalf("the token is in the log:\n%s", logs.String())
	}
	if reason := tc.Status().Reason; strings.Contains(reason, testToken) {
		t.Fatalf("the token is in what the settings page shows: %s", reason)
	}
}

func TestAnAnswerThatIsNotTheBotAPICarriesNothingOn(t *testing.T) {
	// A captive portal or a proxy: the body is not ours to repeat, only the
	// status is.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>proxy error for " + testToken + "</html>"))
	}))
	defer server.Close()

	channel := &Channel{base: server.URL}
	_, err := channel.client(testToken).getUpdates(context.Background(), 0, 0)
	if err == nil {
		t.Fatal("the proxy page was taken for an answer")
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "proxy error") {
		t.Fatalf("the foreign body reached the error: %s", err)
	}
}

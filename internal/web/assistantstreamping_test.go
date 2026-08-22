package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/config"
)

// An answer is silent while the model thinks: no frame at all travels the
// conversation stream, sometimes for minutes. The keepalive beside the ping is
// an SSE comment, which holds the socket open but fires no event in a browser,
// so without a frame the page can see there is no way to tell a thinking model
// from a socket that died, and it would either rebuild the stream over and
// over or stay deaf on a dead one. That is what this frame is for, so it has
// to go out on its own beat with nothing else happening.
func TestTheConversationStreamProvesItIsAliveWithAPingFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	current, err := conversations.Open("")
	if err != nil {
		t.Fatalf("open conversation: %v", err)
	}

	beat := assistantPingInterval
	assistantPingInterval = 20 * time.Millisecond
	t.Cleanup(func() { assistantPingInterval = beat })

	s := &Server{conversations: conversations, assistant: workspace, cfg: config.Config{StreamHeartbeatInterval: time.Second}}
	r := gin.New()
	r.GET("/assistant/:id/stream", s.handleAssistantStream)

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/assistant/"+current.ID+"/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(rec, request)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "event: assistant\ndata: {\"kind\":\"ping\"}") {
		t.Fatalf("no ping frame on a quiet conversation stream: %q", body)
	}
	// The keepalive is the second one on a much longer beat here, so a body
	// full of comments and no frame would mean the ping never went out.
	if strings.Count(body, "\"kind\":\"ping\"") < 2 {
		t.Fatalf("the ping did not repeat on its beat: %q", body)
	}
}

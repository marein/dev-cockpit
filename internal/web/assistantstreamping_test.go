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
	current, err := conversations.Create("")
	if err != nil {
		t.Fatalf("open conversation: %v", err)
	}

	beat := assistantPingInterval
	assistantPingInterval = 20 * time.Millisecond
	t.Cleanup(func() { assistantPingInterval = beat })

	s := &Server{assistants: conversations, workspace: workspace, cfg: config.Config{StreamHeartbeatInterval: time.Second}}
	r := gin.New()
	r.GET("/assistants/:id/stream", s.handleAssistantStream)

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/assistants/"+current.ID+"/stream", nil).WithContext(ctx)
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

// The stream opens with the assistant's picks, its own connect snapshot: a
// pick that moved while the socket was down was announced to nobody, so the
// reconnect has to carry the reading or the page keeps the ring it rendered.
// The frame names the assistant it is about and stands before anything
// else the stream says.
func TestTheConversationStreamOpensWithTheAssistantsPicks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	current, err := conversations.Create("")
	if err != nil {
		t.Fatalf("open conversation: %v", err)
	}
	if _, err := conversations.SetModels(current.ID, assistant.ModelChoice{Chat: "fable", ChatSet: true, Trigger: "haiku", TriggerSet: true}); err != nil {
		t.Fatalf("set the models: %v", err)
	}

	s := &Server{assistants: conversations, workspace: workspace, cfg: config.Config{StreamHeartbeatInterval: time.Second}}
	r := gin.New()
	r.GET("/assistants/:id/stream", s.handleAssistantStream)

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/assistants/"+current.ID+"/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(rec, request)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	want := "event: assistant\ndata: {\"kind\":\"models\",\"models\":{\"assistant\":\"" + current.ID + "\",\"chat\":\"fable\",\"check\":\"\",\"trigger\":\"haiku\",\"updatedAt\":\""
	at := strings.Index(body, want)
	if at < 0 {
		t.Fatalf("the stream did not open with the assistant's picks: %q", body)
	}
	if first := strings.Index(body, "event: assistant"); first != at {
		t.Fatalf("want the picks as the first frame, got %q", body)
	}
}

// A SetModels landing exactly between the stream reading the instance for its
// connect snapshot and that snapshot going out over the wire must not be
// lost: the stream has already subscribed by then, so the save's own frame
// is already queued on the channel and follows the stale snapshot right
// after it, as a second, newer frame, which is what wins in the browser on
// its own stamp.
func TestASaveBetweenTheSnapshotReadAndItsWriteStillReachesTheClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	current, err := conversations.Create("")
	if err != nil {
		t.Fatalf("open conversation: %v", err)
	}

	assistantStreamSnapshotHook = func(id string) {
		if _, err := conversations.SetModels(id, assistant.ModelChoice{Chat: "fable", ChatSet: true}); err != nil {
			t.Errorf("set the models from the hook: %v", err)
		}
	}
	t.Cleanup(func() { assistantStreamSnapshotHook = nil })

	s := &Server{assistants: conversations, workspace: workspace, cfg: config.Config{StreamHeartbeatInterval: time.Second}}
	r := gin.New()
	r.GET("/assistants/:id/stream", s.handleAssistantStream)

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/assistants/"+current.ID+"/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(rec, request)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	stale := "event: assistant\ndata: {\"kind\":\"models\",\"models\":{\"assistant\":\"" + current.ID + "\",\"chat\":\"\",\"check\":\"\",\"trigger\":\"\",\"updatedAt\":\""
	fresh := "event: assistant\ndata: {\"kind\":\"models\",\"models\":{\"assistant\":\"" + current.ID + "\",\"chat\":\"fable\",\"check\":\"\",\"trigger\":\"\",\"updatedAt\":\""
	staleAt := strings.Index(body, stale)
	freshAt := strings.Index(body, fresh)
	if staleAt < 0 {
		t.Fatalf("the connect snapshot did not carry the picks from before the save: %q", body)
	}
	if freshAt < 0 {
		t.Fatalf("the save's own frame never reached the client: %q", body)
	}
	if freshAt <= staleAt {
		t.Fatalf("the fresh frame did not follow the stale snapshot: %q", body)
	}
}

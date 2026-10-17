package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func frame(stream byte, text string) []byte {
	head := []byte{stream, 0, 0, 0, 0, 0, 0, byte(len(text))}
	return append(head, text...)
}

func TestLogsUnwrapsFramesAndDropsEscapes(t *testing.T) {
	var body []byte
	body = append(body, frame(1, "\x1b[32mready\x1b[0m\n")...)
	body = append(body, frame(2, "warn: \x1b]0;title\x07disk\n")...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/containers/abc/logs") || r.URL.Query().Get("tail") != "50" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Logs(context.Background(), "abc", 50)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ready\nwarn: disk\n"; got != want {
		t.Fatalf("Logs = %q, want %q", got, want)
	}
}

func TestLogsOfATtyStayAsTheyAre(t *testing.T) {
	if _, ok := unframeLogs([]byte("plain line\n")); ok {
		t.Fatal("a tty's log read as frames")
	}
	cut := frame(1, "complete\n")
	cut = append(cut, frame(1, "cut short")[:12]...)
	got, ok := unframeLogs(cut)
	if !ok || string(got) != "complete\ncut " {
		t.Fatalf("unframeLogs = %q, %v", got, ok)
	}
}

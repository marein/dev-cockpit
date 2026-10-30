package editorintelligence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// Pyright lists the workspace's files from a timer after the handshake and
// says nothing about it, and a usages list asked meanwhile holds the
// declaration alone. The lookup waits for its first diagnostics, which it
// publishes only once the listing is complete, and for nothing else.
func TestListsLateServerIsWaitedForItsListing(t *testing.T) {
	installFakeLSP(t, "lists-late", "pyright-langserver")
	s := newTestService(t)

	req := goRequest(t)
	req.Path = "src/inventory/models.py"
	req.Content = "class Item:\n    pass\n"
	start := time.Now()
	res, err := s.References(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Locations) != 2 || res.Outside != 1 {
		t.Fatalf("the usages must be the listed workspace's: %+v", res)
	}
	took := time.Since(start)
	if took < 600*time.Millisecond {
		t.Fatalf("answered before the listing ended, took %v", took)
	}
	if took > 5*time.Second {
		t.Fatalf("a server that lists late must be waited for its listing alone, took %v", took)
	}

	// The listing ends once per server, the next lookup goes out at once.
	start = time.Now()
	if res, _ := s.Definition(context.Background(), req); !res.Available || len(res.Locations) == 0 {
		t.Fatalf("definition after the listing: %+v", res)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("a listed workspace must not be waited for again, took %v", took)
	}
}

// The image holds no interpreter, so the Python server is pointed at the
// site-packages of a virtual environment inside the project through its
// settings, with the src root kept on. A section the profile does not hold,
// and every section of a profile without settings, stays the server's own
// default.
func TestConfigurationAnswersTheProfileSettings(t *testing.T) {
	answer := func(path string) []json.RawMessage {
		t.Helper()
		p, _, _ := ProfileForPath(path)
		var out bytes.Buffer
		c := &lspConn{profile: p, stdin: nopCloser{&out}, done: make(chan struct{})}
		id := json.RawMessage("7")
		c.answerServerRequest(rpcMessage{JSONRPC: "2.0", ID: &id, Method: "workspace/configuration",
			Params: mustMarshal(map[string]any{"items": []map[string]any{{"section": "python"}, {"section": "pyright"}}})})
		payload, err := readFrame(bufio.NewReader(&out))
		if err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Result []json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(payload, &resp); err != nil || len(resp.Result) != 2 {
			t.Fatalf("answer %s: %v", payload, err)
		}
		return resp.Result
	}

	py := answer("src/app/main.py")
	var python struct {
		Analysis struct {
			AutoSearchPaths bool     `json:"autoSearchPaths"`
			ExtraPaths      []string `json:"extraPaths"`
		} `json:"analysis"`
	}
	if err := json.Unmarshal(py[0], &python); err != nil {
		t.Fatal(err)
	}
	if !python.Analysis.AutoSearchPaths || strings.Join(python.Analysis.ExtraPaths, ",") != ".venv/lib/python3*/site-packages,venv/lib/python3*/site-packages" {
		t.Fatalf("python section: %s", py[0])
	}
	if string(py[1]) != "null" {
		t.Fatalf("a section the profile does not hold is null, got %s", py[1])
	}
	for _, raw := range answer("main.go") {
		if string(raw) != "null" {
			t.Fatalf("a profile without settings answers null, got %s", raw)
		}
	}
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// The Python way mounts the projects directory like every profile without a
// default configuration, works in the project and runs the language server
// over stdio from the image the cockpit built.
func TestPythonDockerArgv(t *testing.T) {
	py, _, _ := ProfileForPath("main.py")
	line := strings.Join(DockerLauncher(t.TempDir(), nil).Argv("/projects", "lab", "/projects/lab", py), " ")
	for _, want := range []string{
		"--name dev-cockpit-pyright-lab",
		"-v /projects:/projects",
		"-w /projects/lab",
		imageRef(py) + " pyright-langserver --stdio",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("run line misses %q: %s", want, line)
		}
	}
	if strings.Contains(line, "DC_WORKSPACE") || workspaceURI(py, "/projects/lab") != "file:///projects/lab" {
		t.Fatalf("python announces its project as the workspace: %s", line)
	}
}

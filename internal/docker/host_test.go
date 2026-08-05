package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSettingWinsOverEverything(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://env:2375")
	if got := Resolve("unix:///custom.sock"); got != "unix:///custom.sock" {
		t.Fatalf("Resolve answered %q", got)
	}
}

func TestResolveEnvBeforeContext(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://env:2375")
	t.Setenv("DOCKER_CONFIG", writeContext(t, "remote", "tcp://context:2375"))
	if got := Resolve(""); got != "tcp://env:2375" {
		t.Fatalf("Resolve answered %q", got)
	}
}

func TestResolveReadsCurrentContext(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONFIG", writeContext(t, "remote", "tcp://context:2375"))
	if got := Resolve(""); got != "tcp://context:2375" {
		t.Fatalf("Resolve answered %q", got)
	}
}

func TestContextHostIgnoresDefaultContext(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", writeContext(t, "default", "tcp://never:2375"))
	if got := contextHost(); got != "" {
		t.Fatalf("contextHost answered %q for the default context", got)
	}
}

// writeContext lays out a docker config directory the way the CLI does: the
// current context name in config.json, the endpoint in a metadata file under
// the sha256 of the name.
func writeContext(t *testing.T, name, host string) string {
	t.Helper()
	dir := t.TempDir()
	config, _ := json.Marshal(map[string]string{"currentContext": name})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), config, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(name))
	metaDir := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(digest[:]))
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"Name":"` + name + `","Endpoints":{"docker":{"Host":"` + host + `"}}}`)
	if err := os.WriteFile(filepath.Join(metaDir, "meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIsSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Skip("unix sockets unavailable")
	}
	defer listener.Close()
	if !isSocket(path) {
		t.Fatalf("socket not recognised")
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if isSocket(plain) {
		t.Fatalf("plain file taken for a socket")
	}
	if isSocket(filepath.Join(dir, "missing")) {
		t.Fatalf("missing path taken for a socket")
	}
}

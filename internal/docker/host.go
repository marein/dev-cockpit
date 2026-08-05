package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Resolve picks the docker host the cockpit talks to, in the order a docker
// CLI user expects: the cockpit's own docker-host setting when it is set,
// then DOCKER_HOST from the environment, then the endpoint of the current
// docker context, and finally the well known socket paths, first one that
// exists wins. An empty answer means no candidate was found, which is a
// normal state, not an error.
func Resolve(setting string) string {
	if setting != "" {
		return setting
	}
	if env := os.Getenv("DOCKER_HOST"); env != "" {
		return env
	}
	if host := contextHost(); host != "" {
		return host
	}
	for _, path := range socketCandidates() {
		if isSocket(path) {
			return "unix://" + path
		}
	}
	return ""
}

// contextHost reads the endpoint of the current docker context the way the
// CLI stores it: the context name in config.json, the endpoint in a metadata
// file under a digest of that name. The default context has no metadata, its
// endpoint is the standard socket, which the candidate list already covers.
func contextHost() string {
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".docker")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return ""
	}
	var config struct {
		CurrentContext string `json:"currentContext"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return ""
	}
	if config.CurrentContext == "" || config.CurrentContext == "default" {
		return ""
	}
	digest := sha256.Sum256([]byte(config.CurrentContext))
	metaPath := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(digest[:]), "meta.json")
	raw, err = os.ReadFile(metaPath)
	if err != nil {
		return ""
	}
	var meta struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if json.Unmarshal(raw, &meta) != nil {
		return ""
	}
	return meta.Endpoints.Docker.Host
}

// socketCandidates lists where a daemon socket usually sits: the standard
// path, the rootless and desktop variants under the home directory, and the
// user runtime directory.
func socketCandidates() []string {
	out := []string{"/var/run/docker.sock"}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		out = append(out, filepath.Join(runtimeDir, "docker.sock"))
	}
	out = append(out, fmt.Sprintf("/run/user/%d/docker.sock", os.Getuid()))
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out,
			filepath.Join(home, ".docker", "run", "docker.sock"),
			filepath.Join(home, ".docker", "desktop", "docker.sock"),
			filepath.Join(home, ".colima", "default", "docker.sock"),
			filepath.Join(home, ".rd", "docker.sock"),
		)
	}
	return out
}

func isSocket(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

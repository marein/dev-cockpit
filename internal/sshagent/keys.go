package sshagent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Key is one default key file of the user, as ssh-keygen reads it without
// its passphrase.
type Key struct {
	Path        string
	Type        string
	Fingerprint string
	Comment     string
	// Protected says the file needs a passphrase. Only those are the agents',
	// ssh reads a key without one itself, as it always did.
	Protected bool
	blob      []byte
}

// keyFiles are the identities ssh tries when nothing else is configured,
// without the hardware backed ones.
var keyFiles = []string{"id_rsa", "id_ecdsa", "id_ed25519"}

// probe is what ssh-keygen said about a key file, kept while the file and its
// .pub stay as they were, because every caller that lists the keys reads them.
type probe struct {
	stamp string
	key   Key
	ok    bool
}

const (
	toolWait  = 30 * time.Second
	maxOutput = 64 << 10
)

// keys reads the default key files as they are on disk right now.
func (s *Service) keys() []Key {
	if s.sshDir == "" || !s.usable {
		return nil
	}
	var keys []Key
	for _, name := range keyFiles {
		path := filepath.Join(s.sshDir, name)
		stamp, ok := fileStamp(path)
		if !ok {
			continue
		}
		s.mu.Lock()
		p, known := s.probed[path]
		s.mu.Unlock()
		if !known || p.stamp != stamp {
			p = probe{stamp: stamp}
			p.key, p.ok = s.probeKey(path)
			s.mu.Lock()
			s.probed[path] = p
			s.mu.Unlock()
		}
		if p.ok {
			keys = append(keys, p.key)
		}
	}
	return keys
}

func fileStamp(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	stamp := statStamp(info)
	if pub, err := os.Stat(path + ".pub"); err == nil {
		stamp += "/" + statStamp(pub)
	}
	return stamp, true
}

func statStamp(info os.FileInfo) string {
	ino := uint64(0)
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		ino = uint64(st.Ino)
	}
	return fmt.Sprint(ino, info.Size(), info.ModTime().UnixNano())
}

// probeKey asks ssh-keygen what a key file is: its fingerprint and comment,
// whether an empty passphrase opens it, and for one that needs a passphrase
// the public key, from the .pub beside it or the file itself, the way ssh
// finds it.
func (s *Service) probeKey(path string) (Key, bool) {
	out, err := s.run(s.tools.keygen, []string{"-l", "-E", "sha256", "-f", path}, nil)
	if err != nil {
		return Key{}, false
	}
	k, ok := parseFingerprint(path, out)
	if !ok {
		return Key{}, false
	}
	if _, err := s.run(s.tools.keygen, []string{"-y", "-P", "", "-f", path}, nil); err == nil {
		return k, true
	}
	out, err = s.run(s.tools.keygen, []string{"-e", "-f", path}, nil)
	if err != nil {
		return Key{}, false
	}
	blob, ok := parseExport(out)
	if !ok || fingerprint(blob) != k.Fingerprint {
		return Key{}, false
	}
	k.Protected = true
	k.blob = blob
	return k, true
}

// parseFingerprint reads ssh-keygen -l: bits, fingerprint, the comment and
// the type in parentheses.
func parseFingerprint(path, out string) (Key, bool) {
	line, _, _ := strings.Cut(out, "\n")
	fields := strings.Fields(line)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "SHA256:") {
		return Key{}, false
	}
	k := Key{Path: path, Fingerprint: fields[1]}
	k.Type = strings.TrimSuffix(strings.TrimPrefix(fields[len(fields)-1], "("), ")")
	if k.Comment = strings.Join(fields[2:len(fields)-1], " "); k.Comment == "no comment" {
		k.Comment = ""
	}
	return k, true
}

// parseExport reads the public key ssh-keygen -e writes, RFC 4716: header
// lines carry a colon and may go on behind a backslash, base64 has neither.
func parseExport(out string) ([]byte, bool) {
	var body strings.Builder
	inside, header := false, false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "---- BEGIN SSH2 PUBLIC KEY"):
			inside = true
		case strings.HasPrefix(line, "---- END SSH2 PUBLIC KEY"):
			blob, err := base64.StdEncoding.DecodeString(body.String())
			return blob, inside && err == nil && len(blob) > 0
		case !inside:
		case header || strings.Contains(line, ":"):
			header = strings.HasSuffix(line, `\`)
		default:
			body.WriteString(line)
		}
	}
	return nil, false
}

// fingerprint is a public key's SHA256 fingerprint the way OpenSSH writes it.
func fingerprint(blob []byte) string {
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// offered answers the protected key a public key names.
func (s *Service) offered(blob []byte) (Key, bool) {
	for _, k := range s.keys() {
		if k.Protected && string(k.blob) == string(blob) {
			return k, true
		}
	}
	return Key{}, false
}

// keyOf answers the key a fingerprint names, or the fingerprint alone.
func (s *Service) keyOf(fingerprint string) Key {
	for _, k := range s.keys() {
		if k.Fingerprint == fingerprint {
			return k
		}
	}
	return Key{Fingerprint: fingerprint}
}

// run starts one short OpenSSH program and answers what it printed. It runs
// in a session of its own, without a terminal or a display to ask on, and
// with SSH_ASKPASS_REQUIRE=never unless env routes its questions somewhere.
func (s *Service) run(name string, args, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), toolWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(append(childEnv(), "SSH_ASKPASS_REQUIRE=never"), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	data, _ := io.ReadAll(io.LimitReader(out, maxOutput))
	_, _ = io.Copy(io.Discard, out)
	return string(data), cmd.Wait()
}

// childEnv is the cockpit's environment without anything that would point a
// program at another agent, display or askpass.
func childEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "SSH_AUTH_SOCK", "SSH_AGENT_PID", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE",
			"DISPLAY", "WAYLAND_DISPLAY", "DC_ASKPASS_SOCKET", "DC_ASKPASS_TOKEN":
			continue
		}
		env = append(env, kv)
	}
	return env
}

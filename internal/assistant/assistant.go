// Package assistant is the cockpit's own conversation: one conversation with an
// installed coder that is bound to the cockpit instead of to a project.
//
// A conversation is a real provider session driven in non-interactive mode. The
// cockpit keeps the readable transcript, the conversation context lives in the
// provider's own session and is resumed by id on every turn.
//
// One substrate, one binding. The substrate is the conversation, its store, the
// streaming and the process handling; the binding is what makes it the
// assistant: its own store paths, a working directory of its own, the memory the
// user can see and edit, and the jobs it steers. The working directory
// deliberately sits next to the cockpit state, never on top of it: the state
// directory holds webhook URLs and push keys, and no coder gets those as its
// default file scope.
//
// This package must not import internal/coder. The coder package refers to
// assistant.Runner for its optional conversation capability, so an import in the
// other direction would close a cycle. Everything this package needs from the
// coder side arrives through the small interfaces in runner.go, wired in main.
//
//	<state-dir>/assistant/assistant.json          the conversation index
//	<state-dir>/assistant/conversations/<id>.json one transcript
//	<state-dir>/assistant/workspace               the working directory
//	<state-dir>/assistant/workspace/memory        one markdown file per fact
//	<state-dir>/assistant/workspace/user-upload/<id> what a message carried
package assistant

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/local/dev-cockpit/internal/filesystem"
)

// Name is what the surface is called in the UI.
const Name = "Assistant"

// Cockpit is how a turn looks at the cockpit itself: the absolute path of the
// running binary plus the arguments that point its read only inspection
// commands at this instance's data. Passing the resolved path means the
// assistant never depends on where the binary sits or on PATH.
type Cockpit struct {
	Executable  string
	StateDir    string
	ProjectsDir string
	// Version is what this build calls itself, a release tag or a dev build
	// with its commit. Named in the instructions, so an answer about the
	// software is about the software that is actually running.
	Version string
}

// Workspace owns the assistant's directories and its memory. The
// conversations themselves live in the Service built by New.
type Workspace struct {
	root       string
	workspace  string
	memoryDir  string
	uploadRoot string
	cockpit    Cockpit
}

// Paths returns the assistant's store locations for a state directory. The
// inspection commands build the same store to know which provider sessions
// belong to a conversation, so the layout lives in one place.
func Paths(stateDir string) (index, conversations, uploads string) {
	root := filepath.Join(stateDir, "assistant")
	return filepath.Join(root, "assistant.json"),
		filepath.Join(root, "conversations"),
		filepath.Join(root, "workspace", "user-upload")
}

// New wires the assistant over the installed coders and returns both halves:
// the conversation service that drives the conversations and the assistant service
// that owns workspace and memory. cockpit describes how a turn can look at
// the cockpit itself, it may be empty.
func New(stateDir string, coders Coders, cockpit Cockpit) (*Service, *Workspace, error) {
	index, conversations, uploads := Paths(stateDir)
	root := filepath.Join(stateDir, "assistant")
	s := &Workspace{
		root:       root,
		workspace:  filepath.Join(root, "workspace"),
		memoryDir:  filepath.Join(root, "workspace", "memory"),
		uploadRoot: uploads,
		cockpit:    cockpit,
	}
	if err := s.ensure(); err != nil {
		return nil, nil, err
	}
	store := NewStoreAt(index, conversations, uploads)
	return newService(store, NewRunStore(stateDir), syncingCoders{coders: coders, sync: s.Sync}, s), s, nil
}

func (s *Workspace) ensure() error {
	for _, dir := range []string{s.workspace, s.memoryDir, s.uploadRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create assistant directory %s: %w", dir, err)
		}
	}
	return s.Sync()
}

// Workspace is the working directory every turn runs in.
func (s *Workspace) Workspace() string { return s.workspace }

// ValidatePath implements conversation.Projects. The assistant is bound to its
// workspace and to nothing else, so an empty binding resolves to it and any
// other path is refused.
func (s *Workspace) ValidatePath(raw string) (string, error) {
	dir := strings.TrimSpace(raw)
	if dir == "" || dir == s.workspace {
		if info, err := os.Stat(s.workspace); err != nil || !info.IsDir() {
			if err := s.ensure(); err != nil {
				return "", errors.New("The assistant workspace is not available.")
			}
		}
		return s.workspace, nil
	}
	return "", errors.New("The assistant runs in its own workspace.")
}

// ProjectNameFor implements conversation.Projects. The assistant has no project, and
// an empty name keeps the project badge off its pages.
func (s *Workspace) ProjectNameFor(string) string { return "" }

// syncingCoders refreshes the generated instruction files before a turn
// starts. The assistant edits its own memory with its normal file tools, so
// the files a coder reads at startup have to be rebuilt from the memory
// directory right before the coder reads them, not only when the UI writes.
type syncingCoders struct {
	coders Coders
	sync   func() error
}

func (c syncingCoders) Available() []CoderInfo {
	all := c.coders.Available()
	out := make([]CoderInfo, 0, len(all))
	for _, info := range all {
		info.Runner = syncingRunner{Runner: info.Runner, sync: c.sync}
		out = append(out, info)
	}
	return out
}

type syncingRunner struct {
	Runner
	sync func() error
}

func (r syncingRunner) Command(req TurnRequest) (Command, error) {
	if err := r.sync(); err != nil {
		return Command{}, errors.New("The assistant memory could not be prepared.")
	}
	return r.Runner.Command(req)
}

// SaveUpload stores one file of a message and classifies it for the browser.
func (s *Workspace) SaveUpload(dir, name string, src io.Reader) (Attachment, error) {
	file, err := filesystem.SaveFile(dir, name, src)
	if err != nil {
		return Attachment{}, err
	}
	return Attachment{
		Name:  file.Name,
		Path:  file.Path,
		Media: MediaKind(file.Name),
		Size:  file.Size,
	}, nil
}

// MediaKind maps a file name onto how the browser should show it.
func MediaKind(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".bmp", ".svg":
		return "image"
	case ".mp4", ".webm", ".ogv", ".mov", ".m4v":
		return "video"
	case ".mp3", ".m4a", ".aac", ".wav", ".ogg", ".oga", ".opus", ".flac":
		return "audio"
	}
	return "file"
}

// ResolveWorkspaceFile turns a path an answer mentions into an absolute path
// inside the workspace. Anything pointing outside is refused, so a rendered
// answer can never link to a file the assistant does not own.
func (s *Workspace) ResolveWorkspaceFile(rel string) (string, error) {
	clean := strings.TrimSpace(rel)
	if clean == "" {
		return "", errors.New("A file is required.")
	}
	target := filepath.Join(s.workspace, filepath.FromSlash(clean))
	if !filesystem.IsUnder(target, s.workspace) {
		return "", errors.New("Refusing to access a file outside the assistant workspace.")
	}
	// The name alone is not the file. The assistant writes into this workspace,
	// so it can put a link in there that points anywhere, and a check on the
	// spelling would serve whatever it points at. What counts is where the path
	// really lands.
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", errors.New("File not found.")
	}
	root, err := filepath.EvalSymlinks(s.workspace)
	if err != nil {
		root = s.workspace
	}
	if !filesystem.IsUnder(real, root) {
		return "", errors.New("Refusing to access a file outside the assistant workspace.")
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("File not found.")
	}
	return real, nil
}

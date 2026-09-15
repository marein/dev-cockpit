// Package assistant is the cockpit's own conversation partner. There may be
// several of them at once: every instance is one assistant with a name, a
// transcript, a workspace of its own and the coder jobs it steers, and it lives
// until somebody deletes it. Nothing is archived and none of them is "the" live
// one.
//
// An instance is a real provider session driven in non-interactive mode. The
// cockpit keeps the readable transcript, the conversation context lives in the
// provider's own session and is resumed by id on every turn.
//
// One substrate, one binding. The substrate is the instance, its store, the
// streaming and the process handling; the binding is what makes it the
// assistant: its own store paths, a working directory of its own, the memory the
// user can see and edit, and the jobs it steers. The working directories
// deliberately sit next to the cockpit state, never on top of it: the state
// directory holds webhook URLs and push keys, and no coder gets those as its
// default file scope.
//
// What is shared and what is not is a deliberate line. The memory is shared,
// because what the user told one assistant is true for all of them. Everything
// that belongs to one conversation sits in that instance's own directory, the
// transcript, the jobs, the draft and the workspace it works in, and that
// separation is order, not protection: an instance may read another's
// transcript and another's workspace, it only ever writes into its own. The
// generated instruction files are one instance's too, they carry its identity.
//
// This package must not import internal/coder. The coder package refers to
// assistant.Runner for its optional conversation capability, so an import in the
// other direction would close a cycle. Everything this package needs from the
// coder side arrives through the small interfaces in runner.go, wired in main.
//
//	<state-dir>/assistant/assistant.json                                 the index of instances
//	<state-dir>/assistant/memory                                         one markdown file per fact, shared
//	<state-dir>/assistant/instances/<id>/transcript.json                 one transcript
//	<state-dir>/assistant/instances/<id>/jobs.json                       the jobs it steers
//	<state-dir>/assistant/instances/<id>/workspace                       the working directory of its turns
//	<state-dir>/assistant/instances/<id>/workspace/CLAUDE.md, AGENTS.md  its generated instructions
//	<state-dir>/assistant/instances/<id>/workspace/assistant-files       what it wrote
//	<state-dir>/assistant/instances/<id>/workspace/user-upload           what a message carried
package assistant

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

// Name is what the surface is called in the UI.
const Name = "Assistant"

// Cockpit is how a turn looks at the cockpit itself: the absolute path of the
// running binary plus the arguments that point its read only inspection
// commands at this cockpit's data. Passing the resolved path means the
// assistant never depends on where the binary sits or on PATH.
type Cockpit struct {
	Executable  string
	StateDir    string
	ProjectsDir string
	// Version is what this build calls itself, a release tag or a dev build
	// with its commit. Named in the instructions, so an answer about the
	// software is about the software that is actually running.
	Version string
	// RepoURL is the web page of the repository this software lives in, a
	// full URL. The instructions name it, so a question about the
	// implementation has somewhere to go when the source is not on the
	// machine it answers from.
	RepoURL string
}

// Workspace owns the directories the instances work in and the shared memory.
// The instances themselves live in the Service built by New.
type Workspace struct {
	root      string
	indexPath string
	instances string
	memoryDir string
	cockpit   Cockpit
}

// Paths returns the assistant's store locations for a state directory. The
// inspection commands build the same store to know which provider sessions
// belong to an instance, so the layout lives in one place.
func Paths(stateDir string) (index, instances, memory string) {
	root := Root(stateDir)
	return filepath.Join(root, "assistant.json"),
		filepath.Join(root, "instances"),
		filepath.Join(root, "memory")
}

// Root is the one directory everything about the assistant sits under.
func Root(stateDir string) string { return filepath.Join(stateDir, "assistant") }

// FilesDirName is the folder inside an instance's workspace it writes its own
// files into. Named here because three places need the same word: the
// directory that is created, the instructions that tell an instance where to
// write, and the store that deletes it with the instance.
const FilesDirName = "assistant-files"

// uploadDirName is the folder inside an instance's workspace the files a
// message carried are stored in.
const uploadDirName = "user-upload"

// workspaceDirName is the folder inside an instance's directory its turns run
// in, the one directory of the instance a coder gets as its default scope.
const workspaceDirName = "workspace"

// New wires the assistant over the installed coders and returns both halves:
// the service that drives the instances and the workspace that owns their
// directories and the memory. cockpit describes how a turn can look at the
// cockpit itself, it may be empty.
func New(stateDir string, coders Coders, cockpit Cockpit) (*Service, *Workspace, error) {
	index, instances, memory := Paths(stateDir)
	s := &Workspace{
		root:      Root(stateDir),
		indexPath: index,
		instances: instances,
		memoryDir: memory,
		cockpit:   cockpit,
	}
	// The move comes first: it is what puts the memory where ensure would
	// otherwise create an empty one.
	if err := migrate(s.root, index, instances, memory); err != nil {
		return nil, nil, err
	}
	if err := s.ensure(); err != nil {
		return nil, nil, err
	}
	store := NewStoreAt(index, instances)
	store.SweepOrphans()
	return newService(store, NewRunStore(stateDir), preparingCoders{coders: coders, workspace: s}, s), s, nil
}

func (s *Workspace) ensure() error {
	for _, dir := range []string{s.instances, s.memoryDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create assistant directory %s: %w", dir, err)
		}
	}
	return s.Sync()
}

// Dir is the workspace of one instance, the directory its turns run in. It is
// the path alone; Workdir is what creates it.
func (s *Workspace) Dir(instanceID string) string {
	return filepath.Join(s.instances, instanceID, workspaceDirName)
}

// Workdir implements Projects: the workspace of one instance, created when it
// is missing. An assistant comes to its directory the way every directory here
// comes to be, on first use: the turn that needs it creates it, and so does a
// path that is about to be handed out. The files folder is created with it,
// because the instructions name that folder as the place to write and it has
// to exist before the first turn runs.
func (s *Workspace) Workdir(instanceID string) (string, error) {
	if !ValidID(instanceID) {
		return "", errors.New("Invalid assistant.")
	}
	dir := s.Dir(instanceID)
	if err := os.MkdirAll(filepath.Join(dir, FilesDirName), 0o700); err != nil {
		return "", errors.New("The assistant workspace is not available.")
	}
	return dir, nil
}

// IsWorkdir reports whether dir is the workspace of some instance, one that
// exists or one that is gone. The check session sweep asks it: a check always
// runs in the workspace of the assistant whose job it is, so a stray check
// session is recognized by where it ran, whoever that assistant was.
func (s *Workspace) IsWorkdir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	rel, err := filepath.Rel(s.instances, filepath.Clean(dir))
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) == 2 && ValidID(parts[0]) && parts[1] == workspaceDirName
}

// Prepare is what runs right before a turn starts in an instance's workspace:
// the directory exists and its instruction files are rebuilt from the memory.
// The assistant edits its own memory with its normal file tools, so the files
// a coder reads at startup have to be rebuilt right before the coder reads
// them, not only when the UI writes.
func (s *Workspace) Prepare(instanceID string) error {
	if _, err := s.Workdir(instanceID); err != nil {
		return err
	}
	return s.syncInstance(instanceID)
}

// preparingCoders readies an instance's workspace before a turn starts in it:
// the instruction files, and the trust the coder's CLI wants for a directory
// it has never seen.
type preparingCoders struct {
	coders    Coders
	workspace *Workspace
}

func (c preparingCoders) Available() []CoderInfo {
	all := c.coders.Available()
	out := make([]CoderInfo, 0, len(all))
	for _, info := range all {
		info.Runner = preparingRunner{Runner: info.Runner, workspace: c.workspace}
		out = append(out, info)
	}
	return out
}

type preparingRunner struct {
	Runner
	workspace *Workspace
}

func (r preparingRunner) Command(req TurnRequest) (Command, error) {
	if err := r.workspace.Prepare(req.Instance); err != nil {
		return Command{}, errors.New("The assistant workspace could not be prepared.")
	}
	// A CLI that stops an unknown directory on its trust dialog would hold
	// this turn there, and a non interactive turn cannot answer. Best effort
	// on purpose, like the coder manager's: a CLI whose config cannot be
	// written still gets its turn, and the reason goes to the log.
	if truster, ok := r.Runner.(WorkdirTruster); ok {
		if err := truster.TrustWorkdir(req.Workdir); err != nil {
			log.Printf("assistant: %s was not marked as trusted, the turn may come up on the trust dialog: %v", req.Workdir, err)
		}
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
// inside that instance's workspace. Anything pointing outside is refused, so a
// rendered answer can never link to a file the assistant does not own: a link
// is read in the workspace of the assistant whose answer carries it, which is
// where its instructions tell it to write.
func (s *Workspace) ResolveWorkspaceFile(instanceID, rel string) (string, error) {
	if !ValidID(instanceID) {
		return "", errors.New("Invalid assistant.")
	}
	clean := strings.TrimSpace(rel)
	if clean == "" {
		return "", errors.New("A file is required.")
	}
	workspace := s.Dir(instanceID)
	target := filepath.Join(workspace, filepath.FromSlash(clean))
	if !filesystem.IsUnder(target, workspace) {
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
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		root = workspace
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

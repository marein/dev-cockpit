package assistant

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/statefile"
	"gopkg.in/yaml.v3"
)

// generatedHeader marks the instruction files the cockpit writes. They are
// rebuilt from the memory directory before every turn, so an edit made in them
// directly is lost, and the header says so.
const generatedHeader = "<!-- Written by dev-cockpit from the memory directory. Edit the memory files, not this one. -->"

// instructionFiles are the per coder names of the generated file, written into
// every instance's workspace. The coders read their file from the working
// directory at startup, which is how the memory and the identity reach a turn
// without spending prompt space on them.
var instructionFiles = []string{"CLAUDE.md", "AGENTS.md"}

// wrapperFileName is the script every workspace carries for the cockpit's own
// commands. It runs the binary that is serving with this cockpit's directories
// and the assistant's id already on the line and hands every argument on, so
// an instruction reads `<workspace>/cockpit status` instead of a line of flags,
// and the flags cannot be dropped or misspelled by a turn. It is written with
// the instruction files, before every turn, and it is named by its absolute
// path in them because a turn changes into other directories.
const wrapperFileName = "cockpit"

// generatedFiles is everything the cockpit writes into a workspace and
// rewrites before every turn: the instruction files and the wrapper. What is
// on this list is noise in a backup and goes when a layout moves.
var generatedFiles = append(append([]string{}, instructionFiles...), wrapperFileName)

// slugPattern guards a memory file name. It is a whitelist, not an escape, so
// no memory name can ever leave the memory directory.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// MaxMemoryBytes bounds one memory entry. The memory is read on every turn, so
// a runaway file would be paid for again and again.
const MaxMemoryBytes = 16 << 10

// Entry is one thing the assistant knows about the user.
type Entry struct {
	Slug    string
	Title   string
	Body    string
	Updated time.Time
}

type entryMeta struct {
	Title string `yaml:"title"`
}

// Memory returns every entry, newest first.
func (s *Workspace) Memory() []Entry {
	files, err := os.ReadDir(s.memoryDir)
	if err != nil {
		return nil
	}
	out := make([]Entry, 0, len(files))
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
			continue
		}
		entry, ok := s.readEntry(strings.TrimSuffix(f.Name(), ".md"))
		if !ok {
			continue
		}
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// MemoryEntry loads one entry.
func (s *Workspace) MemoryEntry(slug string) (Entry, error) {
	entry, ok := s.readEntry(slug)
	if !ok {
		return Entry{}, errors.New("That memory does not exist.")
	}
	return entry, nil
}

func (s *Workspace) readEntry(slug string) (Entry, bool) {
	if !slugPattern.MatchString(slug) {
		return Entry{}, false
	}
	path := filepath.Join(s.memoryDir, slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, false
	}
	meta, body := markdown.SplitFrontMatter(data)
	entry := Entry{Slug: slug, Body: strings.TrimSpace(string(body))}
	if len(meta) > 0 {
		var parsed entryMeta
		_ = yaml.Unmarshal(meta, &parsed)
		entry.Title = strings.TrimSpace(parsed.Title)
	}
	if entry.Title == "" {
		entry.Title = titleFromSlug(slug)
	}
	if info, err := os.Stat(path); err == nil {
		entry.Updated = info.ModTime()
	}
	return entry, true
}

// SaveMemory writes one entry and rebuilds the instruction files. A new entry
// takes its file name from the title, an existing one keeps its name so the
// links the assistant wrote to it stay valid.
func (s *Workspace) SaveMemory(slug, title, body string) (Entry, error) {
	title = strings.TrimSpace(oneLine(title))
	if title == "" {
		return Entry{}, errors.New("A memory needs a title.")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return Entry{}, errors.New("A memory needs something to remember.")
	}
	if len(body) > MaxMemoryBytes {
		return Entry{}, fmt.Errorf("That memory is too long. Keep it under %d KB.", MaxMemoryBytes/1024)
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = s.freeSlug(slugify(title))
	}
	if !slugPattern.MatchString(slug) {
		return Entry{}, errors.New("That memory name cannot be used.")
	}
	data, err := markdown.WriteFrontMatter(entryMeta{Title: title}, body)
	if err != nil {
		return Entry{}, errors.New("The memory could not be written.")
	}
	if err := os.MkdirAll(s.memoryDir, 0o700); err != nil {
		return Entry{}, errors.New("The memory could not be written.")
	}
	if err := os.WriteFile(filepath.Join(s.memoryDir, slug+".md"), data, 0o600); err != nil {
		return Entry{}, errors.New("The memory could not be written.")
	}
	if err := s.Sync(); err != nil {
		return Entry{}, err
	}
	entry, _ := s.readEntry(slug)
	return entry, nil
}

// DeleteMemory drops one entry and rebuilds the instruction files.
func (s *Workspace) DeleteMemory(slug string) error {
	if !slugPattern.MatchString(slug) {
		return errors.New("That memory does not exist.")
	}
	if err := os.Remove(filepath.Join(s.memoryDir, slug+".md")); err != nil && !os.IsNotExist(err) {
		return errors.New("The memory could not be deleted.")
	}
	return s.Sync()
}

// syncMu serializes the rebuild inside this process. Every turn of every
// assistant rebuilds its files right before its coder reads them, and the
// memory page rebuilds every assistant's, so two rebuilds of the same files
// meet regularly and they write the same bytes.
var syncMu sync.Mutex

// Sync rebuilds the generated instruction files of every assistant from the
// memory directory. The memory is shared, so a change to it reaches every
// workspace; an assistant that has no workspace yet gets its files the moment
// its first turn creates one. It writes only on a real change, so an unchanged
// memory does not touch the files a coder watches.
func (s *Workspace) Sync() error {
	var index []Summary
	statefile.Load(s.indexPath, &index)
	for _, entry := range index {
		if info, err := os.Stat(s.Dir(entry.ID)); err != nil || !info.IsDir() {
			continue
		}
		if err := s.write(entry.ID, entry.Title); err != nil {
			return err
		}
	}
	return nil
}

// syncInstance rebuilds the instruction files of one assistant, right before
// a turn of its starts. The name is read from the index, the one place that
// holds it without loading a transcript.
func (s *Workspace) syncInstance(instanceID string) error {
	title := DefaultTitle
	var index []Summary
	statefile.Load(s.indexPath, &index)
	for _, entry := range index {
		if entry.ID == instanceID {
			title = entry.Title
			break
		}
	}
	return s.write(instanceID, title)
}

// write puts the generated files of one assistant into its workspace: the
// instruction files and the wrapper. Two rebuilds at once must never leave
// half a file behind, which is what a coder starting in that moment would read
// as its whole instructions. The lock keeps them apart in this process, and the
// write is a temporary file renamed into place, so a reader sees the old file
// or the new one and never a file being written. The temporary name carries
// the process id because a second serve process on the same state directory is
// possible and one fixed name would have them overwriting each other's half
// written file.
func (s *Workspace) write(instanceID, title string) error {
	syncMu.Lock()
	defer syncMu.Unlock()
	instructions := []byte(s.instructions(instanceID, title))
	for _, name := range instructionFiles {
		if err := replace(filepath.Join(s.Dir(instanceID), name), instructions, 0o600); err != nil {
			return err
		}
	}
	return replace(s.Wrapper(instanceID), []byte(s.wrapper(instanceID)), 0o700)
}

// replace writes a generated file the atomic way, and only on a real change,
// so an unchanged file is not touched.
func replace(path string, want []byte, mode os.FileMode) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, want) {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm() == mode {
			return nil
		}
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, want, mode); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// instructionsData is what the instruction template is filled with.
type instructionsData struct {
	Header string
	ID     string
	Title  string
	// Workspace is this assistant's own directory, Instances the directory the
	// other assistants' workspaces stand under.
	Workspace        string
	Instances        string
	WorkspaceDirName string
	FilesDir         string
	MemoryDir        string
	// Cockpit is the wrapper of this workspace, by its absolute path: what
	// every command in the instructions runs through.
	Cockpit string
	Version string
	RepoURL string
	// Commands says whether the cockpit's commands are listed at all: without
	// a binary to name there is nothing a turn could run.
	Commands bool
	Memory   []Entry
}

// instructions is what a coder reads before the first message of a turn: who
// it is here, what it knows about the user, and how to remember something new.
// They are one assistant's: the id, the name, the workspace and the wrapper
// every cockpit command runs through are written in, so a turn knows who it is
// from the file it reads at startup and nothing has to be said in a prompt.
func (s *Workspace) instructions(instanceID, title string) string {
	return render("instructions.md.tmpl", instructionsData{
		Header:           generatedHeader,
		ID:               instanceID,
		Title:            title,
		Workspace:        s.Dir(instanceID),
		Instances:        s.instances,
		WorkspaceDirName: workspaceDirName,
		FilesDir:         FilesDirName,
		MemoryDir:        s.memoryDir,
		Cockpit:          s.Wrapper(instanceID),
		Version:          strings.TrimSpace(s.cockpit.Version),
		RepoURL:          strings.TrimSpace(s.cockpit.RepoURL),
		Commands:         strings.TrimSpace(s.cockpit.Executable) != "",
		Memory:           s.Memory(),
	})
}

// wrapperData is what the wrapper template is filled with.
type wrapperData struct {
	ID          string
	Executable  string
	StateDir    string
	ProjectsDir string
}

// wrapper is the script that runs the cockpit's own commands for one
// assistant: the absolute path of the binary that is running, the assistant
// command group, the directories of this cockpit and the assistant's id, with
// every argument handed on. A bare `dev-cockpit` depends on a PATH a turn does
// not control, and on a machine with several instances it would reach
// whichever one owns the default state directory, which is the wrong cockpit
// and possibly somebody else's terminal; a binary nobody named falls back to
// exactly that, the way it always did.
func (s *Workspace) wrapper(instanceID string) string {
	exe := strings.TrimSpace(s.cockpit.Executable)
	if exe == "" {
		exe = "dev-cockpit"
	}
	return render("cockpit.sh.tmpl", wrapperData{
		ID:          instanceID,
		Executable:  exe,
		StateDir:    strings.TrimSpace(s.cockpit.StateDir),
		ProjectsDir: strings.TrimSpace(s.cockpit.ProjectsDir),
	})
}

// Wrapper is the absolute path of one assistant's wrapper, the one spelling
// the instructions and the check prompt name a cockpit command by.
func (s *Workspace) Wrapper(instanceID string) string {
	return filepath.Join(s.Dir(instanceID), wrapperFileName)
}

// Cockpit implements cockpitNamer for the check prompt: the same wrapper the
// instructions name, so a check reads one spelling of a command, not two.
func (s *Workspace) Cockpit(instanceID string) string { return s.Wrapper(instanceID) }

func (s *Workspace) freeSlug(base string) string {
	if base == "" {
		base = "note"
	}
	slug := base
	for i := 2; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(s.memoryDir, slug+".md")); os.IsNotExist(err) {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	return slug
}

func slugify(title string) string {
	var b strings.Builder
	last := byte('-')
	for i := 0; i < len(title) && b.Len() < 48; i++ {
		c := title[i]
		switch {
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
			b.WriteByte(c)
			last = c
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
			last = c
		default:
			if last != '-' {
				b.WriteByte('-')
				last = '-'
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func titleFromSlug(slug string) string {
	words := strings.ReplaceAll(slug, "-", " ")
	if words == "" {
		return "Memory"
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

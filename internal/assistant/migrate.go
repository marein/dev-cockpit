package assistant

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// Until the last release the assistant was one conversation at a time: a new
// one archived the previous one, the index carried every conversation that had
// ever been had, and all of them worked in one shared workspace with the memory
// inside it. Now an instance lives until it is deleted and works in a workspace
// of its own, so an index full of archived transcripts would be an index full
// of dead entries.
//
// The move happens once, at startup, and it decides by what the old shape said:
// the conversation that was live becomes the first assistant and inherits the
// jobs and the workspace, with everything the conversations wrote into it and
// its own uploads, and every other one goes. They are not kept anywhere. An
// archived conversation had its provider session dropped when it was archived,
// so nothing could ever continue it; what is left of it is a transcript of a
// thread nobody can answer in, and carrying a hundred of those forward would
// make the one list that matters unreadable and every backup heavier for it.
// The memory is shared, so it leaves the workspace before the workspace becomes
// one assistant's.
//
// The old layout is recognized by the directory it wrote its transcripts into,
// so a state directory that has already moved is not looked at twice. This is
// the only move there is: it reads the layout of the last release and nothing
// else, and a state directory in any other shape is left as it is.
const (
	// legacyTranscripts is where a transcript used to live, one file per
	// conversation.
	legacyTranscripts = "conversations"
	// legacyJobs is where the jobs used to live, one file for the single
	// assistant there was.
	legacyJobs = "jobs.json"
	// legacyWorkspace is the one workspace every conversation worked in, with
	// the memory in it.
	legacyWorkspace = "workspace"
	// legacyMemory is where the memory lived, inside that workspace.
	legacyMemory = "memory"
)

// migrate turns the old one-conversation layout into instances. It is a no-op
// once there is no legacy transcript directory left, which is the state every
// fresh install starts in.
func migrate(root, indexPath, instancesDir, memoryDir string) error {
	legacy := filepath.Join(root, legacyTranscripts)
	if info, err := os.Stat(legacy); err != nil || !info.IsDir() {
		return nil
	}

	var index []Summary
	statefile.Load(indexPath, &index)
	// Newest activity first, the order the old index was written in, so "the
	// one that was live" is decided the same way the old service decided it.
	sort.SliceStable(index, func(i, j int) bool { return index[i].LastMessageAt.After(index[j].LastMessageAt) })

	kept := ""
	var carried []Summary
	for _, entry := range index {
		if entry.Status != StatusActive {
			continue
		}
		dir := filepath.Join(instancesDir, entry.ID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create the assistant directory of %s: %w", entry.ID, err)
		}
		// Moved out of the old directory before it is removed, so the one
		// transcript that survives is never the one being deleted.
		if err := move(filepath.Join(legacy, entry.ID+".json"), filepath.Join(dir, "transcript.json")); err != nil {
			return err
		}
		kept = entry.ID
		carried = append(carried, entry)
		break
	}

	// The jobs were the one assistant's, so they go to the one that stays. With
	// nobody to inherit them there is nobody left to report to either: a job
	// whose owner does not exist can never be checked and never reported, so it
	// goes rather than sitting in the state as a promise nothing keeps.
	jobs := filepath.Join(root, legacyJobs)
	if kept != "" {
		if err := move(jobs, filepath.Join(instancesDir, kept, jobsFileName)); err != nil {
			return err
		}
	} else if err := os.Remove(jobs); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove the old jobs: %w", err)
	}

	if err := moveWorkspace(filepath.Join(root, legacyWorkspace), memoryDir, instancesDir, kept); err != nil {
		return err
	}

	statefile.Save(indexPath, 0o600, carried)
	// Everything still in there is a conversation that is over, the ones the
	// index listed as archived and any transcript it never knew about. The
	// directory goes with them, and its absence is what makes this run once.
	if err := os.RemoveAll(legacy); err != nil {
		return fmt.Errorf("remove the old transcript directory: %w", err)
	}
	log.Printf("assistant: %d conversation(s) that were over are gone, %d became an assistant", len(index)-len(carried), len(carried))
	return nil
}

// moveWorkspace takes the one shared workspace apart. The memory goes up, to
// where every assistant reads it from now. The generated instruction files go,
// the next turn writes its own. The uploads of every conversation but the one
// that stays go with those conversations. What is left, the files the
// conversations wrote and the uploads of the one that was live, becomes that
// assistant's workspace, the whole directory renamed into its place, so a link
// an old answer carries still lands on the file. With no live conversation
// there is nobody to own it and it goes with the rest.
func moveWorkspace(workspace, memoryDir, instancesDir, kept string) error {
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		return nil
	}
	if err := moveDir(filepath.Join(workspace, legacyMemory), memoryDir); err != nil {
		return err
	}
	for _, name := range generatedFiles {
		if err := os.Remove(filepath.Join(workspace, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove the old %s: %w", name, err)
		}
	}
	uploads := filepath.Join(workspace, uploadDirName)
	if entries, err := os.ReadDir(uploads); err == nil {
		for _, entry := range entries {
			if entry.Name() == kept {
				continue
			}
			if err := os.RemoveAll(filepath.Join(uploads, entry.Name())); err != nil {
				return fmt.Errorf("remove the uploads of a conversation that is over: %w", err)
			}
		}
	}
	// The uploads sat in a folder per conversation; in a workspace that is one
	// assistant's they sit directly in the upload folder, under their names.
	if entries, err := os.ReadDir(filepath.Join(uploads, kept)); err == nil && kept != "" {
		for _, entry := range entries {
			if err := os.Rename(filepath.Join(uploads, kept, entry.Name()), filepath.Join(uploads, entry.Name())); err != nil {
				return fmt.Errorf("move the uploads of %s: %w", kept, err)
			}
		}
		if err := os.Remove(filepath.Join(uploads, kept)); err != nil {
			return fmt.Errorf("move the uploads of %s: %w", kept, err)
		}
	}
	if kept == "" {
		if err := os.RemoveAll(workspace); err != nil {
			return fmt.Errorf("remove the old workspace: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Join(instancesDir, kept), 0o700); err != nil {
		return fmt.Errorf("create the assistant directory of %s: %w", kept, err)
	}
	return moveDir(workspace, filepath.Join(instancesDir, kept, workspaceDirName))
}

// moveDir renames a directory and treats a missing source as done. A
// destination that already holds something is left alone, like move: a half
// finished move must never overwrite what the previous attempt already put
// there. An empty destination is what a start that got no further than
// creating the directory leaves, so it gives way.
func moveDir(from, to string) error {
	if info, err := os.Stat(from); err != nil || !info.IsDir() {
		return nil
	}
	if entries, err := os.ReadDir(to); err == nil {
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(to); err != nil {
			return fmt.Errorf("move %s: %w", filepath.Base(from), err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return fmt.Errorf("move %s: %w", filepath.Base(from), err)
	}
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("move %s: %w", filepath.Base(from), err)
	}
	return nil
}

// move renames a file and treats a missing source as done. A destination that
// already exists is left alone: a half finished move must never overwrite what
// the previous attempt already put there.
func move(from, to string) error {
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if _, err := os.Stat(to); err == nil {
		return os.Remove(from)
	}
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("move %s: %w", filepath.Base(from), err)
	}
	return nil
}

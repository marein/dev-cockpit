package web

import (
	"context"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/linecomment"
)

// forgeQuoteTimeout bounds the reads of the lines the threads of one review
// stand on.
const forgeQuoteTimeout = 30 * time.Second

// ForgeReview mirrors what the forge poller read of the review threads of a
// project's branch into the project's line comments: every thread on a line
// of the diff is one, its notes the thread's, at the thread's line with the
// text that line has in the commit the forge names, or the text the forge
// gives, and every general thread a project comment. A thread on a whole
// file stands on no line, forge-show lists it.
func (s *Server) ForgeReview(r forge.ReviewRead) {
	p, err := s.projects.FindByName(r.Watch.Project)
	if err != nil || !p.GitRepo {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), forgeQuoteTimeout)
	defer cancel()
	threads := append(lineThreads(ctx, git.New(p.Path), r), generalThreads(r)...)
	if s.lineComments.SyncForge(p.Name, p.Path, threads, !r.Review.Partial) {
		s.publishLineComments(p.Name)
	}
}

// ForgeKept takes the threads of the forge from the line comments of every
// project the forge poller does not keep: nothing reads its review threads
// any more, so nothing would keep them in step or let them go.
func (s *Server) ForgeKept(kept map[string]bool) {
	for _, project := range s.lineComments.DropForge(func(project string) bool { return !kept[project] }) {
		s.publishLineComments(project)
	}
}

// lineThreads are the threads on a line of r's diff, each with the text of
// its line where it can be read: the forge's own, else the line of the file
// in repo at the commit the forge names.
func lineThreads(ctx context.Context, repo *git.Repo, r forge.ReviewRead) []linecomment.ForgeThread {
	files := map[string][]string{}
	quote := func(t forge.Thread) (string, bool) {
		if t.Quote != "" {
			return t.Quote, true
		}
		if t.Commit == "" {
			return "", false
		}
		key := t.Commit + "\x00" + t.Path
		lines, seen := files[key]
		if !seen {
			if content, ok, err := repo.FileAt(ctx, t.Commit, t.Path); err == nil && ok {
				lines = strings.Split(string(content), "\n")
			}
			files[key] = lines
		}
		if t.Line > len(lines) {
			return "", false
		}
		return lines[t.Line-1], true
	}
	var threads []linecomment.ForgeThread
	for _, t := range r.Review.Diff {
		if t.Line < 1 || len(t.Notes) == 0 {
			continue
		}
		text, quoted := quote(t)
		thread := forgeLineThread(r, t)
		thread.At = linecomment.Place{Path: t.Path, Line: t.Line, Quote: text, Quoted: quoted}
		threads = append(threads, thread)
	}
	return threads
}

// generalThreads are the general threads of r, each standing on the project.
func generalThreads(r forge.ReviewRead) []linecomment.ForgeThread {
	var threads []linecomment.ForgeThread
	for _, t := range r.Review.General {
		if len(t.Notes) == 0 {
			continue
		}
		thread := forgeLineThread(r, t)
		thread.Project = true
		threads = append(threads, thread)
	}
	return threads
}

// forgeLineThread is t of r with its notes, its page the first note's, its
// id only where it takes a reply and unresolvable where it takes no resolve.
func forgeLineThread(r forge.ReviewRead, t forge.Thread) linecomment.ForgeThread {
	reply, resolve := t.Takes()
	thread := linecomment.ForgeThread{Kind: r.Watch.Binding.Host.Kind.ID, URL: t.Notes[0].URL, Unresolvable: !resolve, Resolved: t.Resolved}
	if reply {
		thread.ID = t.ID
	}
	for _, n := range t.Notes {
		thread.Notes = append(thread.Notes, linecomment.ForgeNote{ID: n.ID, Author: n.Author, Text: n.Body, Created: n.Created})
	}
	return thread
}

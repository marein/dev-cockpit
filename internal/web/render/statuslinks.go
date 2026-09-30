package render

import (
	"fmt"

	"github.com/marein/dev-cockpit/internal/docker"
)

// StatusLinks is the links chip of the status line: every address a running
// stack answers on, grouped by project and then by stack. The chip names the
// totals, its menu lists the groups.
type StatusLinks struct {
	Projects []StatusLinksProject
	Links    int
}

// StatusLinksProject is one project's group in the menu, in the browser's
// order; the page sorts the groups the way every project listing is sorted
// (@dc/project-sort), off the same keys the listings carry. Open marks the
// group that renders unfolded while the browser holds no folds of its own.
type StatusLinksProject struct {
	Name         string
	Active       bool
	LastUsedUnix int64
	WorktreeOf   string
	Stacks       []StatusLinksStack
	Links        int
	Open         bool
}

// StatusLinksStack is one stack's addresses, the routed hosts first and the
// published ports ascending, the order StackLinks answers them in. Label is
// the stack's directory under the project, empty for the project root.
type StatusLinksStack struct {
	Label string
	Links []docker.Link
}

// NewStatusLinks reads the groups out of the project browser: only the stacks
// that run, only the projects that keep one such stack with an address. The
// project of the page is the open one; without one, or with one that answers
// nothing, the project used last is.
func NewStatusLinks(projects []ProjectNav, current string) StatusLinks {
	out := StatusLinks{}
	open, pinned, lastUsed := -1, false, int64(-1)
	for _, nav := range projects {
		entry := StatusLinksProject{Name: nav.Name, Active: nav.Active, LastUsedUnix: nav.LastUsedUnix, WorktreeOf: nav.WorktreeOf}
		for _, stack := range nav.Docker.Stacks {
			if stack.Running == 0 || len(stack.Links) == 0 {
				continue
			}
			entry.Stacks = append(entry.Stacks, StatusLinksStack{Label: stack.Label, Links: stack.Links})
			entry.Links += len(stack.Links)
		}
		if entry.Links == 0 {
			continue
		}
		out.Links += entry.Links
		out.Projects = append(out.Projects, entry)
		index := len(out.Projects) - 1
		if nav.Name == current {
			open, pinned = index, true
		} else if !pinned && nav.LastUsedUnix > lastUsed {
			open, lastUsed = index, nav.LastUsedUnix
		}
	}
	if open >= 0 {
		out.Projects[open].Open = true
	}
	return out
}

// Any reports whether the chip has anything to show.
func (l StatusLinks) Any() bool { return len(l.Projects) > 0 }

// Summary is the chip's text, the totals in words.
func (l StatusLinks) Summary() string {
	return fmt.Sprintf("%s · %s", plural(l.Links, "link"), plural(len(l.Projects), "project"))
}

func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

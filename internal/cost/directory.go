package cost

import (
	"maps"
	"path/filepath"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// owner is who a session belongs to, as the cockpit last said. A known owner
// came from the cockpit's list or from an assistant's workspace, a guess
// from the directory alone, and a better guess replaces it. Name is a
// coder's name, an assistant's lives under its id. Seen is the day the list
// or a booking last named the session: an owner no row refers to stays
// while that day is within the months kept, so a session listed before it
// spent keeps its owner.
type owner struct {
	Kind      Kind      `json:"kind"`
	Project   string    `json:"project,omitempty"`
	Assistant string    `json:"assistant,omitempty"`
	Name      string    `json:"name,omitempty"`
	Known     bool      `json:"known,omitempty"`
	Seen      time.Time `json:"seen"`
}

// assistantName is an assistant's name as it was last said, kept for its
// bookings after the assistant is gone.
type assistantName struct {
	Name string    `json:"name"`
	Seen time.Time `json:"seen"`
}

// directory is sessions.json: the owners of the sessions by coder/session
// and the assistants' names by instance id. The collector keeps it from
// what the cockpit lists, the pages only read it.
type directory struct {
	Sessions   map[string]owner         `json:"sessions"`
	Assistants map[string]assistantName `json:"assistants"`
}

func (d *directory) ensure() {
	if d.Sessions == nil {
		d.Sessions = map[string]owner{}
	}
	if d.Assistants == nil {
		d.Assistants = map[string]assistantName{}
	}
}

func (d directory) clone() directory {
	return directory{Sessions: maps.Clone(d.Sessions), Assistants: maps.Clone(d.Assistants)}
}

func (d directory) equal(o directory) bool {
	return maps.Equal(d.Sessions, o.Sessions) && maps.Equal(d.Assistants, o.Assistants)
}

// attribution is who spent a row of the session now. A session nobody
// named is other.
func (d directory) attribution(coder, session string) Attribution {
	o, ok := d.Sessions[coder+"/"+session]
	if !ok {
		return Attribution{Kind: KindOther}
	}
	if o.Kind == KindAssistant {
		return Attribution{Kind: KindAssistant, Assistant: o.Assistant, Name: d.Assistants[o.Assistant].Name}
	}
	return Attribution{Kind: o.Kind, Project: o.Project, Name: o.Name}
}

// day is the stamp of Seen: a day is all the pruning reads, and a booking
// within the same day leaves the file as it is.
func day(now time.Time) time.Time { return now.UTC().Truncate(24 * time.Hour) }

func (s *Service) placeOf(cwd string) (string, string) {
	if s.place == nil || cwd == "" {
		return "", ""
	}
	return s.place(cwd)
}

// booked notes that a session spent. A session without a known owner is
// placed by its directory: an assistant's workspace names its owner for
// good, anything else is a guess of kind other in the project the
// directory lies in.
func (s *Service) booked(d *directory, key, cwd string, now time.Time) {
	o, ok := d.Sessions[key]
	if !ok || !o.Known {
		assistant, project := s.placeOf(cwd)
		switch {
		case assistant != "":
			o = owner{Kind: KindAssistant, Assistant: assistant, Known: true}
		case !ok:
			o = owner{Kind: KindOther, Project: project}
		case o.Project == "":
			o.Project = project
		}
	}
	o.Seen = day(now)
	d.Sessions[key] = o
}

// updateOwners changes sessions.json, writes it if anything moved and says
// whether it did. A file that cannot be read is not written: its owners are
// unknown, not none.
func (s *Service) updateOwners(change func(d *directory)) (bool, error) {
	s.owners.Lock()
	defer s.owners.Unlock()
	d, err := s.loadOwners()
	if err != nil {
		return false, err
	}
	before := d.clone()
	change(&d)
	if d.equal(before) {
		return false, nil
	}
	if err := statefile.Write(filepath.Join(s.dir, sessionsFile), 0o600, d); err != nil {
		return false, err
	}
	s.wrote()
	return true, nil
}

func (s *Service) loadOwners() (directory, error) {
	var d directory
	if err := readFile(filepath.Join(s.dir, sessionsFile), &d); err != nil {
		return directory{}, err
	}
	d.ensure()
	return d, nil
}

// Owners is what the cockpit runs right now: its coder sessions, running or
// stored, and its assistants' names by instance id.
type Owners struct {
	Coders     []CoderSession
	Assistants map[string]string
}

// CoderSession is a coder session the cockpit lists, with its name and the
// directory it runs in.
type CoderSession struct {
	Coder, Session, Name, CWD string
}

// NoteOwners names every coder session and every assistant the cockpit
// runs now. A name stays for its bookings once its owner is gone.
func (s *Service) NoteOwners(o Owners) error {
	seen := day(s.now())
	changed, err := s.updateOwners(func(d *directory) {
		for _, c := range o.Coders {
			assistant, project := s.placeOf(c.CWD)
			own := owner{Kind: KindCoder, Project: project, Name: c.Name, Known: true, Seen: seen}
			if assistant != "" {
				own = owner{Kind: KindAssistant, Assistant: assistant, Known: true, Seen: seen}
			}
			d.Sessions[c.Coder+"/"+c.Session] = own
		}
		for id, name := range o.Assistants {
			d.Assistants[id] = assistantName{Name: name, Seen: seen}
		}
	})
	if changed {
		s.announce()
	}
	return err
}

// noteSpent notes the sessions that spent, by coder/session with their
// directory, and drops the owners that neither the rows nor anybody since
// cut named.
func (s *Service) noteSpent(spent map[string]string, rows []Row, cut, now time.Time) (bool, error) {
	return s.updateOwners(func(d *directory) {
		for key, cwd := range spent {
			s.booked(d, key, cwd, now)
		}
		d.prune(rows, cut)
	})
}

// prune drops the owners no row refers to and nobody named since cut, and
// the names of assistants no owner refers to and nobody named since cut.
func (d *directory) prune(rows []Row, cut time.Time) {
	used := map[string]bool{}
	for _, r := range rows {
		used[r.Coder+"/"+r.Session] = true
	}
	named := map[string]bool{}
	for key, o := range d.Sessions {
		if !used[key] && o.Seen.Before(cut) {
			delete(d.Sessions, key)
			continue
		}
		named[o.Assistant] = true
	}
	for id, a := range d.Assistants {
		if !named[id] && a.Seen.Before(cut) {
			delete(d.Assistants, id)
		}
	}
}

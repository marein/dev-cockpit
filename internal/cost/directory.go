package cost

import (
	"maps"
	"time"
)

// owner is who a session belongs to, as the cockpit last said. A known owner
// came from an event or from an assistant's workspace, a guess from the
// directory alone, and a better guess replaces it. Name is a coder's name,
// an assistant's lives under its id. Seen is the day an event or a booking
// last named the session: an owner no row refers to stays while that day is
// within the months kept, so a session announced before it spent keeps its
// owner.
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
// and the assistants' names by instance id. The cost domain keeps it from
// the cockpit's events and never looks an owner up at render time.
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
func (s *Service) booked(d *directory, coder, session, cwd string, now time.Time) {
	key := coder + "/" + session
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

// coderSession is a coder the cockpit runs, with its name now: on start,
// on a resume, on a rename and for every coder known when the server starts.
func (s *Service) coderSession(d *directory, coder, session, name, cwd string, now time.Time) {
	assistant, project := s.placeOf(cwd)
	o := owner{Kind: KindCoder, Project: project, Name: name, Known: true, Seen: day(now)}
	if assistant != "" {
		o = owner{Kind: KindAssistant, Assistant: assistant, Known: true, Seen: day(now)}
	}
	d.Sessions[coder+"/"+session] = o
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

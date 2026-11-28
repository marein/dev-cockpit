package forge

import (
	"log"
	"path/filepath"
	"slices"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// endsKept is how long the record of a project whose pipeline moved no more
// stands. A project read again after it is gone is seen for the first time.
const endsKept = 30 * 24 * time.Hour

// PipelineEnd is a pipeline of a branch the poller keeps that reached a
// final status: the branch and the read that saw it end, with its change
// request and its pipeline.
type PipelineEnd struct {
	Watch  Watch
	Status BranchStatus
}

// Final reports whether a pipeline in this status has ended. One that waits
// for a person has not.
func (s Status) Final() bool {
	switch s {
	case Success, Failed, Canceled, Skipped:
		return true
	}
	return false
}

// endsFile holds, by project, what the service last saw of the pipeline of
// the branch it keeps there.
type endsFile struct {
	Projects map[string]endRecord `json:"projects"`
}

// endRecord is the branch and where it was bound, the pipeline it stood at
// and its status, the final statuses of that pipeline already handed on,
// and when any of it last moved.
type endRecord struct {
	Branch   string    `json:"branch"`
	Host     string    `json:"host"`
	Path     string    `json:"path"`
	Pipeline string    `json:"pipeline,omitempty"`
	Status   Status    `json:"status,omitempty"`
	Ended    []Status  `json:"ended,omitempty"`
	At       time.Time `json:"at"`
}

func (s *Service) endsPath() string {
	return filepath.Join(s.dir, "pipelines.json")
}

// OnPipelineEnd sets what hears of a pipeline end, per pipeline and final
// status of the branch the poller keeps of a project, also across a restart.
// The two listeners want opposite things of a process that dies in the
// middle, see noteEnd. take hears of an end while the record does not hold
// it yet and holds it on disk when it returns: it hears of one again where
// the record never got to move, and tells it by its pipeline and status.
// told hears of it after, once, or not at all where the process dies right
// there, and never twice.
func (s *Service) OnPipelineEnd(take, told func(PipelineEnd)) {
	s.endsMu.Lock()
	defer s.endsMu.Unlock()
	s.takeEnd, s.toldEnd = take, told
}

// pipelineKey names a pipeline apart from every other of its repository: by
// the forge's own id, else by the commit it ran on, which is the one
// pipeline GitHub has of a commit.
func pipelineKey(w Watch, pl *Pipeline) string {
	switch {
	case pl == nil:
		return ""
	case pl.ID != "":
		return "#" + pl.ID
	}
	return "@" + w.Head
}

// next is the record after a read of w answered st, whether it moved, and
// the final status the read is the first to see, "" for none. A branch the
// record does not hold is seen for the first time, and a pipeline that has
// ended by then ended before anybody watched it, which is nobody's news.
func (r endRecord) next(w Watch, st BranchStatus, now time.Time) (endRecord, bool, Status) {
	key, status := pipelineKey(w, st.Pipeline), Status("")
	if st.Pipeline != nil {
		status = st.Pipeline.Status
	}
	if r.Branch != w.Branch || r.Host != w.Binding.Host.ID || r.Path != w.Binding.Path {
		first := endRecord{Branch: w.Branch, Host: w.Binding.Host.ID, Path: w.Binding.Path, Pipeline: key, Status: status, At: now}
		if status.Final() {
			first.Ended = []Status{status}
		}
		return first, true, ""
	}
	n := r
	if key != r.Pipeline {
		n.Pipeline, n.Ended = key, nil
	}
	n.Status = status
	ended := Status("")
	if status.Final() && !slices.Contains(n.Ended, status) {
		n.Ended = append(slices.Clone(n.Ended), status)
		ended = status
	}
	if n.Pipeline == r.Pipeline && n.Status == r.Status && slices.Equal(n.Ended, r.Ended) {
		return r, false, ""
	}
	n.At = now
	return n, true, ended
}

// noteEnd keeps in the record of w's project what a read of the branch the
// poller keeps there answered. The end of a pipeline this read is the first
// to see is handed to whoever takes it before the record holds it, and the
// record moves once that came back: a process that dies in between finds the
// record where it stood, reads the same end again at its next start, as long
// as the forge still tells it, and hands it again. Everybody else hears of
// the end once the record holds it.
func (s *Service) noteEnd(w Watch, st BranchStatus) {
	if told := s.keepEnd(w, st); told != nil {
		told(PipelineEnd{Watch: w, Status: st})
	}
}

// keepEnd is the part of noteEnd under the lock, which stands over the hand
// over and the write, so two reads of one end hand it once. It answers who
// is told of an end the record now holds, nil without one: a file it cannot
// read hands nothing on, and one it cannot write tells nobody, the next read
// hands the end to the one that takes it again.
func (s *Service) keepEnd(w Watch, st BranchStatus) func(PipelineEnd) {
	now := s.poll.now()
	s.endsMu.Lock()
	defer s.endsMu.Unlock()
	var f endsFile
	if err := statefile.Read(s.endsPath(), &f); err != nil {
		log.Printf("forge: %v", err)
		return nil
	}
	next, moved, ended := f.Projects[w.Project].next(w, st, now)
	if !moved {
		return nil
	}
	if ended != "" && s.takeEnd != nil {
		s.takeEnd(PipelineEnd{Watch: w, Status: st})
	}
	if f.Projects == nil {
		f.Projects = map[string]endRecord{}
	}
	for project, r := range f.Projects {
		if now.Sub(r.At) > endsKept {
			delete(f.Projects, project)
		}
	}
	f.Projects[w.Project] = next
	if err := s.write(s.endsPath(), f); err != nil {
		log.Printf("forge: the pipeline ends could not be saved: %v", err)
		return nil
	}
	if ended == "" {
		return nil
	}
	return s.toldEnd
}

// forgetEnds takes away the record of a project that is gone, so one made
// again under its name is read for the first time. A file it cannot read
// stays as it is.
func (s *Service) forgetEnds(project string) {
	s.endsMu.Lock()
	defer s.endsMu.Unlock()
	var f endsFile
	if err := statefile.Read(s.endsPath(), &f); err != nil {
		log.Printf("forge: %v, the pipeline ends of %s stay", err, project)
		return
	}
	if _, ok := f.Projects[project]; !ok {
		return
	}
	delete(f.Projects, project)
	if err := s.write(s.endsPath(), f); err != nil {
		log.Printf("forge: the pipeline ends of %s could not be taken away: %v", project, err)
	}
}

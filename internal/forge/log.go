package forge

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/plaintext"
)

// The bounds of a log read: how many lines of a log's end it shows unless it
// asks for another count, how many it may ask for, how much of the log's end
// it keeps to find them in, of how many of the jobs that failed a pipeline it
// reads the logs, and how often a log on show is read again while its job
// runs.
const (
	DefaultLogLines = 100
	MaxLogLines     = 1000
	logKeep         = 256 << 10
	MaxFailedLogs   = 3
	LiveLogPace     = 5 * time.Second
)

// ErrNoLogs answers a log read on a forge whose provider cannot read job
// logs.
var ErrNoLogs = errors.New("This forge cannot read job logs.")

// LogLater answers a log read of a job that waits or runs on a forge that
// hands a job's log out only once the job ended.
type LogLater struct {
	Forge string
}

func (e *LogLater) Error() string {
	return e.Forge + " hands out a job's log only once the job ended."
}

// Log is the end of a job's log as plain text: its last lines, and whether
// lines before them were left out. Live says the job runs and its forge
// hands out what it wrote so far, so a read again shows more. Err says why
// the log could not be read.
type Log struct {
	Job   Job
	Lines []string
	Cut   bool
	Live  bool
	Err   error
}

// JobLogs reads the end of the logs the forge b binds to keeps of jobs, side
// by side, each a job of a pipeline BranchStatus read on b, at most lines
// lines of each, 1 to MaxLogLines. A job the forge keeps no log of answers
// its NoLog as the log's Err, one whose log the forge hands out only once it
// ended a LogLater while it waits or runs, and neither is asked for.
func (s *Service) JobLogs(ctx context.Context, b Binding, jobs []Job, lines int) []Log {
	logs := make([]Log, len(jobs))
	reader, live, token, err := s.logReader(b)
	var wg sync.WaitGroup
	for i, job := range jobs {
		logs[i].Job = job
		switch {
		case err != nil:
			logs[i].Err = err
		case job.NoLog != "":
			logs[i].Err = errors.New(job.NoLog)
		case !live && (job.Status == Pending || job.Status == Running):
			logs[i].Err = &LogLater{Forge: b.Host.Kind.Label}
		default:
			wg.Go(func() {
				raw, cut, err := reader.JobLog(ctx, s.client, b, token, job, logKeep)
				if err != nil {
					logs[i].Err = err
					return
				}
				logs[i].Lines, logs[i].Cut = logLines(raw, cut, lines)
				logs[i].Live = live && job.Status == Running
			})
		}
	}
	wg.Wait()
	return logs
}

func (s *Service) logReader(b Binding) (LogReader, bool, string, error) {
	p, ok := s.provider(b.Host.Kind.ID)
	if !ok {
		return nil, false, "", ErrNoLogs
	}
	reader, ok := p.(LogReader)
	if !ok {
		return nil, false, "", ErrNoLogs
	}
	token, err := s.token(b.Host.ID)
	if err != nil {
		return nil, false, "", err
	}
	if token == "" {
		return nil, false, "", ErrNoToken
	}
	_, live := p.(LiveLogReader)
	return reader, live, token, nil
}

// logLines is the end of a raw log as at most lines plain text lines, and
// whether lines before them were left out. A log kept from its middle starts
// with the rest of a line, which goes; one kept from its start may open with
// a byte order mark, which shows nothing.
func logLines(raw []byte, cut bool, lines int) ([]string, bool) {
	if cut {
		_, raw, _ = bytes.Cut(raw, []byte("\n"))
	} else {
		raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	}
	text := strings.TrimRight(plaintext.Terminal(string(raw)), "\n")
	if text == "" {
		return nil, cut
	}
	all := strings.Split(text, "\n")
	if len(all) > lines {
		return all[len(all)-lines:], true
	}
	return all, cut
}

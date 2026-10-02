// Package cost books what the coders spend at the API list price, and answers
// the aggregates the pages and the assistant read. Subscription users do not
// pay these amounts, they are an equivalent.
//
// The ledger knows no coder CLI. A Source reads one CLI's own records and
// hands over entries that are already priced: what one session spent on one
// model at one moment, with the tokens behind it. The service books them into
// buckets of the moment they were spent. Every booking carries who spent it,
// so a coder, a project or a transcript that goes later takes nothing of what
// was booked with it.
package cost

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/cost/price"
)

// Kind says what in the cockpit ran a session.
type Kind string

const (
	KindCoder Kind = "coder"
	// KindAssistant is everything an assistant ran, its own turns, its
	// checks and its triggers, found by the workspace the session ran in.
	KindAssistant Kind = "assistant"
	KindOther     Kind = "other"
)

// Attribution is who spent a booking, joined from the owners when the rows
// are read: a rename reaches every booking of the session. Assistant is the
// instance id of an assistant's booking, Name its or the coder's name now,
// or the last one the cockpit said for an owner that is gone.
type Attribution struct {
	Kind      Kind
	Project   string
	Assistant string
	Name      string
}

// Placer reads a directory a session ran in: the assistant whose workspace it
// is, else the project it lies in. Either may be empty. It reads the path
// alone, never what runs.
type Placer func(cwd string) (assistant, project string)

// Tokens is what an entry or a bucket used, per price class.
type Tokens = price.Tokens

// Entry is one priced spend a source read: what one session spent on one
// model at one moment. Unpriced says the model has no list price, the tokens
// count and USD is zero. TopUp says the spend is the part of the CLI's own
// running total that its logged calls do not explain, calls the CLI makes
// without a record; its tokens are what that total adds, where it says.
type Entry struct {
	Session  string
	CWD      string
	At       time.Time
	Model    string
	Tokens   Tokens
	USD      float64
	Unpriced bool
	TopUp    bool
}

// Source reads one coder CLI's records. cursor is what the previous call
// returned, nil on the first; it is the source's own state, stored in
// cursors.json and written right after the rows it produced. Capture reads
// what one session's records hold right now, before they are deleted; Collect
// gets it back later and reads only that, captured is nil for a read of
// everything on disk.
type Source interface {
	Coder() string
	Capture(session string) any
	Collect(cursor json.RawMessage, captured any, now time.Time) ([]Entry, json.RawMessage, error)
}

// Row is one booking bucket: what one session spent on one model within the
// bucket starting at At. Recent buckets are five minutes wide, older ones are
// folded into hours, see compact. A row stores the session alone, who owns
// it is joined from the owners when it is read.
type Row struct {
	At          time.Time `json:"at"`
	Coder       string    `json:"coder"`
	Session     string    `json:"session"`
	Model       string    `json:"model,omitempty"`
	Attribution `json:"-"`
	USD         float64 `json:"usd"`
	Tokens      Tokens  `json:"tokens,omitzero"`
	Unpriced    bool    `json:"unpriced,omitempty"`
	TopUp       bool    `json:"top_up,omitempty"`
}

const (
	fineBucket   = 5 * time.Minute
	fineKeep     = 48 * time.Hour
	coarseBucket = time.Hour
	// dayKeep is how many days keep their hours. The page cuts hours for one
	// day and the chart of the last 30 days into days, older spans read days
	// or weeks.
	dayKeep = 30
	// maintainEvery is how often a collect that booked nothing still folds,
	// drops the months past the retention and writes what that changed.
	maintainEvery = time.Hour
)

// RetentionSettingKey is how many months of bookings stay, this month
// included. DefaultRetention keeps the current month and the two before it,
// MinRetention is two because the 30 day chart reaches into the month
// before.
const (
	RetentionSettingKey = "cost-retention-months"
	DefaultRetention    = 3
	MinRetention        = 2
)

// Retention reads the setting's value, the default where it says nothing
// usable, never below the minimum.
func Retention(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return DefaultRetention
	}
	return max(n, MinRetention)
}

// Service owns the cost folder in the state directory. One worker writes
// it: the cockpit's events and the deletes only queue what they bring, the
// worker applies it, see worker.go.
type Service struct {
	dir        string
	sources    []Source
	place      Placer
	now        func() time.Time
	zone       func() *time.Location
	keepMonths func() int
	mu         sync.Mutex
	onChange   func()
	maintained time.Time

	queueMu sync.Mutex
	queue   []job
	wake    chan struct{}

	cacheMu  sync.Mutex
	cached   []Row
	cacheSig string
}

// New builds the service over its sources. Nothing is read until Collect.
func New(stateDir string, place Placer, sources ...Source) *Service {
	return &Service{
		dir:        filepath.Join(stateDir, "cost"),
		sources:    sources,
		place:      place,
		now:        time.Now,
		zone:       func() *time.Location { return time.Local },
		keepMonths: func() int { return DefaultRetention },
		wake:       make(chan struct{}, 1),
	}
}

// SetOnChange is called after the worker wrote a change the pages show.
func (s *Service) SetOnChange(fn func()) { s.onChange = fn }

// SetZone names the zone months and folded days are cut in, the one the
// pages cut their days in.
func (s *Service) SetZone(fn func() *time.Location) { s.zone = fn }

// SetRetention names how many months of bookings stay, this month included,
// read through Retention.
func (s *Service) SetRetention(fn func() int) { s.keepMonths = fn }

// sameJSON compares two cursors by content: cursors.json stores them
// indented, a source hands them back compact, and a byte compare would
// rewrite it on every poll.
func sameJSON(a, b json.RawMessage) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

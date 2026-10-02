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

// Source reads one coder CLI's records. Its cursor is its own state, stored
// in the current month file and written in one rename with the rows it
// produced. Collect reads what the records gained since the cursor, nil on
// the first read, every session's or only the named one's. Running says
// whether a process of the CLI still writes the session's records.
type Source interface {
	Coder() string
	Collect(cursor json.RawMessage, session string, now time.Time) ([]Entry, json.RawMessage, error)
	Running(session string) bool
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
	// file is the month file the row lives in, the month it was booked in.
	file string
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
// before, MaxRetention is ten years, far from where month arithmetic
// overflows.
const (
	RetentionSettingKey = "cost-retention-months"
	DefaultRetention    = 3
	MinRetention        = 2
	MaxRetention        = 120
)

// Retention reads the setting's value, the default where it says nothing
// usable, held between the minimum and the maximum.
func Retention(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return DefaultRetention
	}
	return min(max(n, MinRetention), MaxRetention)
}

// Service owns the cost folder in the state directory. books is held by
// whoever reads the sources and writes the month files, owners by whoever
// writes sessions.json; a collect takes books first.
type Service struct {
	dir        string
	sources    []Source
	place      Placer
	now        func() time.Time
	zone       func() *time.Location
	keepMonths func() int
	onChange   func()
	books      chan struct{}
	maintained time.Time
	owners     sync.Mutex

	// gen counts the writes, cachedGen is the one the cache was read at.
	cacheMu   sync.Mutex
	cached    []Row
	cachedGen uint64
	gen       uint64
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
		books:      make(chan struct{}, 1),
	}
}

// SetOnChange is called after a write the pages show.
func (s *Service) SetOnChange(fn func()) { s.onChange = fn }

// SetZone names the zone months and folded days are cut in, the one the
// pages cut their days in.
func (s *Service) SetZone(fn func() *time.Location) { s.zone = fn }

// SetRetention names how many months of bookings stay, this month included,
// read through Retention.
func (s *Service) SetRetention(fn func() int) { s.keepMonths = fn }

package render

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// SettingsForgesData feeds Settings › Forges, every host the origins resolve
// against, the built in ones first.
type SettingsForgesData struct {
	Page
	SettingsNav SettingsNav
	Hosts       []ForgeHostRow
}

// Pace is the poller's pace as the page tells it.
func (SettingsForgesData) Pace() ForgePace { return forgePace() }

// ForgePace is the pace of the forge poller, each interval written short,
// like 15s or 5m.
type ForgePace struct {
	Fast, Moved, Slow, Review string
}

func forgePace() ForgePace {
	p := forge.PollPace()
	return ForgePace{Fast: shortDuration(p.Fast), Moved: shortDuration(p.Moved), Slow: shortDuration(p.Slow), Review: shortDuration(p.Review)}
}

func shortDuration(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return fmt.Sprintf("%ds", d/time.Second)
}

// ForgeHostRow is one host of the list. Anchor is the row's id, where the
// outcome of its test lands. Token tells whether the host has one, TokenHint
// its last characters when it is long enough to show them, TokenURL the
// host's token view, Tested what the last test of the token found. EditURL
// is where the Test posts, and the Edit of an added host.
type ForgeHostRow struct {
	Anchor    string
	Kind      string
	Icon      string
	WebURL    string
	Aliases   []string
	Builtin   bool
	CABundle  bool
	Token     bool
	TokenHint string
	TokenURL  string
	Tested    string
	EditURL   string
}

// ForgeHostFormData feeds the form that adds a host and an added host's Edit,
// in the dialog (Modal) and as a page, with the host as it stands or as it
// was sent. Action is the form's own path, ID empty while the host is new.
// TokenStep is the host's token view instead, which an add leads on to and
// the row opens: the token itself never comes back, only whether there is
// one and its hint, TokenPage is the server's page that creates one and
// Anchor the host's row on the list, where the view leads.
type ForgeHostFormData struct {
	Page
	SettingsNav SettingsNav
	Modal       bool
	TokenStep   bool
	Action      string
	ID          string
	Title       string
	Anchor      string
	KindLabel   string
	Kinds       []ForgeChoice
	WebURL      string
	Aliases     string
	CABundle    string
	Token       bool
	TokenHint   string
	TokenPage   string
	WebURLMax   int
	AliasesMax  int
	CABundleMax int
	TokenMax    int
}

// ForgeChoice is one option a forge form offers, a kind or a host.
type ForgeChoice struct {
	ID     string
	Label  string
	Picked bool
}

// ProjectForgeData feeds a project's forge, in the dialog (Modal) and as a
// page: where it is bound, nil without a binding, and the form that binds a
// main project by its origin, by hand or not at all. A worktree only shows
// its main project's binding, Main empty when that is no project here.
// Origin is the origin as the row names it, OriginBound where it alone
// binds. Hosts and Path hold the binding by hand: what was sent, else the
// one set, else the one the origin gives.
type ProjectForgeData struct {
	Page
	Modal       bool
	Project     string
	Return      string
	Bound       *forge.Binding
	Worktree    bool
	Main        string
	How         forge.How
	Origin      string
	OriginBound *forge.Binding
	Hosts       []ForgeChoice
	Path        string
	PathMax     int
}

// HostPicked reports whether the binding by hand names a host yet.
func (d ProjectForgeData) HostPicked() bool {
	for _, h := range d.Hosts {
		if h.Picked {
			return true
		}
	}
	return false
}

// ForgeBadge is what the forge poller last read of a project's branch, the
// way its row and the editor's forge sheet show it: the change request by its
// number in the forge's words and the pipeline by its status, each linking to
// its page, and why the last read failed. The forge sheet lists the pipeline's
// jobs too. Branch and Head are the branch it was read of and the commit it
// was pushed at.
type ForgeBadge struct {
	Kind     forge.Kind
	Change   *forge.Change
	Pipeline *forge.Pipeline
	Problem  string
	Branch   string
	Head     string
}

// NewForgeBadge is the badge of what the poller read of a branch on b, nil
// when it read neither a change request nor a pipeline and nothing failed.
// Its problem is why the forge features are off for the branch, else why the
// last read failed.
func NewForgeBadge(b forge.Binding, p forge.Polled) *ForgeBadge {
	badge := &ForgeBadge{Kind: b.Host.Kind, Change: p.Status.Change, Pipeline: p.Status.Pipeline, Branch: p.Watch.Branch, Head: p.Watch.Head}
	var off *forge.BranchOff
	switch {
	case errors.As(p.Err, &off):
		badge.Problem = off.Error()
	case p.Err != nil:
		badge.Problem = "The last read of the forge failed: " + p.Err.Error()
	}
	if badge.Change == nil && badge.Pipeline == nil && badge.Problem == "" {
		return nil
	}
	return badge
}

// ChangeRef is the change request's number the way the forge writes it.
func (f ForgeBadge) ChangeRef() string {
	return f.Kind.ChangeRef(f.Change.Number)
}

// ChangeIcon is the glyph of where the change request stands.
func (f ForgeBadge) ChangeIcon() string {
	switch {
	case f.Change.State == forge.Merged:
		return "ti-git-merge"
	case f.Change.State == forge.Closed:
		return "ti-git-pull-request-closed"
	case f.Change.Draft:
		return "ti-git-pull-request-draft"
	}
	return "ti-git-pull-request"
}

// ChangeTitle names the change request in full: its kind, number, where it
// stands and its title.
func (f ForgeBadge) ChangeTitle() string {
	noun := f.Kind.ChangeNoun
	if noun != "" {
		noun = strings.ToUpper(noun[:1]) + noun[1:]
	}
	return fmt.Sprintf("%s %s, %s: %s", noun, f.ChangeRef(), f.changeState(), f.Change.Title)
}

func (f ForgeBadge) changeState() string {
	if f.Change.State == forge.Open && f.Change.Draft {
		return "draft"
	}
	return string(f.Change.State)
}

// Tone is the badge's colour, the pipeline's: green when it went through,
// red when it failed, blue while it runs or waits to, grey for every other
// end and without a pipeline.
func (f ForgeBadge) Tone() string {
	if f.Pipeline == nil {
		return "bg-secondary-lt"
	}
	return "bg-" + statusColor(f.Pipeline.Status) + "-lt"
}

// PipelineIcon is the glyph of the pipeline's status, the cockpit's icon
// spinner while it runs.
func (f ForgeBadge) PipelineIcon() string {
	return statusIcon(f.Pipeline.Status)
}

// PipelineColor is the colour of the pipeline's status.
func (f ForgeBadge) PipelineColor() string {
	return statusColor(f.Pipeline.Status)
}

func statusColor(s forge.Status) string {
	switch s {
	case forge.Success:
		return "green"
	case forge.Failed:
		return "red"
	case forge.Running, forge.Pending:
		return "blue"
	}
	return "secondary"
}

func statusIcon(s forge.Status) string {
	switch s {
	case forge.Success:
		return "ti-circle-check"
	case forge.Failed:
		return "ti-circle-x"
	case forge.Running:
		return "ti-progress dc-icon-spinner"
	case forge.Canceled:
		return "ti-ban"
	case forge.Skipped:
		return "ti-player-skip-forward"
	case forge.Manual:
		return "ti-hand-stop"
	}
	return "ti-clock"
}

// PipelineTitle names the pipeline and its status.
func (f ForgeBadge) PipelineTitle() string {
	if f.Pipeline.ID != "" {
		return fmt.Sprintf("Pipeline #%s %s", f.Pipeline.ID, f.Pipeline.Status)
	}
	return fmt.Sprintf("Pipeline %s", f.Pipeline.Status)
}

// Jobs are the pipeline's jobs the way the forge sheet lists them.
func (f ForgeBadge) Jobs() []ForgeJob {
	if f.Pipeline == nil {
		return nil
	}
	return f.jobs(f.Pipeline.Jobs)
}

// FailedLogs are the first jobs that failed the pipeline and have a log, as
// many as a log read takes, the way the forge sheet lists them.
func (f ForgeBadge) FailedLogs() []ForgeJob {
	if f.Pipeline == nil {
		return nil
	}
	var logged []forge.Job
	for _, j := range f.Pipeline.Failing() {
		if j.NoLog == "" && len(logged) < forge.MaxFailedLogs {
			logged = append(logged, j)
		}
	}
	return f.jobs(logged)
}

func (f ForgeBadge) jobs(jobs []forge.Job) []ForgeJob {
	out := make([]ForgeJob, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, ForgeJob{Job: j, Forge: f.Kind.Label})
	}
	return out
}

// JobsSummary counts the pipeline's jobs and how many stand at each status,
// the ones that need a look first.
func (f ForgeBadge) JobsSummary() string {
	counts := map[forge.Status]int{}
	for _, j := range f.Pipeline.Jobs {
		counts[j.Status]++
	}
	n := len(f.Pipeline.Jobs)
	parts := []string{plural(n, "job")}
	if f.Pipeline.Partial {
		parts[0] = fmt.Sprintf("the first %d jobs", n)
	}
	for _, s := range []forge.Status{forge.Failed, forge.Running, forge.Pending, forge.Manual, forge.Canceled, forge.Skipped, forge.Success} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	return strings.Join(parts, ", ")
}

// ForgeRetry is a run of failed jobs again that the forge sheet, a job's log
// and the project row's git menu offer: its label in the forge's words, the
// line under it, the branch and the commit it was read at, the pipeline by
// its id and the job by its, "" for the jobs that failed the pipeline, and
// why it cannot run, "" when it can.
type ForgeRetry struct {
	Label    string
	Sub      string
	Branch   string
	Head     string
	Pipeline string
	Job      string
	Blocked  string
}

// Retry is the run of the pipeline's failed jobs again, nil unless the
// pipeline failed on a forge that runs failed jobs again. Its line names the
// jobs that failed it which the forge cannot run again.
func (f ForgeBadge) Retry() *ForgeRetry {
	if f.Pipeline == nil || f.Pipeline.Status != forge.Failed || f.Kind.Retry == "" {
		return nil
	}
	r := &ForgeRetry{Label: f.Kind.Retry + " failed jobs", Sub: f.PipelineTitle(), Branch: f.Branch, Head: f.Head, Pipeline: f.Pipeline.ID, Blocked: f.Pipeline.RetryBlocked()}
	if left := f.Pipeline.LeftOut(); len(left) > 0 {
		names := make([]string, 0, len(left))
		for _, j := range left {
			names = append(names, j.Name)
		}
		r.Sub += " · " + strings.Join(names, ", ") + " cannot run again"
	}
	return r
}

// JobRetry is the run of one job of the pipeline again, nil unless it failed
// on a forge that runs failed jobs again.
func (f ForgeBadge) JobRetry(j forge.Job) *ForgeRetry {
	if f.Pipeline == nil || j.Status != forge.Failed || f.Kind.Retry == "" {
		return nil
	}
	return &ForgeRetry{Label: f.Kind.Retry + " job", Sub: j.Name, Branch: f.Branch, Head: f.Head, Pipeline: f.Pipeline.ID, Job: j.ID, Blocked: j.NoRetry}
}

// ForgeJob is one job of a pipeline the way the editor's forge sheet lists it,
// Forge the label of the forge that ran it.
type ForgeJob struct {
	forge.Job
	Forge string
}

// Icon is the glyph of the job's status, the cockpit's icon spinner while it
// runs.
func (j ForgeJob) Icon() string {
	return statusIcon(j.Status)
}

// Color is the colour of the job's status, the one the badge wears for it.
func (j ForgeJob) Color() string {
	return statusColor(j.Status)
}

// Sub is the line under the job's name: its stage, its status, whether its
// failure fails nothing, and for a job the forge keeps no log of, where a tap
// leads instead.
func (j ForgeJob) Sub() string {
	var parts []string
	if j.Stage != "" {
		parts = append(parts, j.Stage)
	}
	parts = append(parts, string(j.Status))
	if j.AllowFailure && j.Status == forge.Failed {
		parts = append(parts, "allowed to fail")
	}
	switch {
	case j.NoLog == "":
	case j.URL != "":
		parts = append(parts, "no log, opens on "+j.Forge)
	default:
		parts = append(parts, j.NoLog)
	}
	return strings.Join(parts, " · ")
}

// ForgeLogData feeds the end of one job's log in the editor's forge sheet: the
// job, its last lines, whether earlier ones were left out, how soon it is
// read again while the job runs, 0 for never, why the log could not be read
// or is handed out only once the job ended, and the run of the job again
// where it failed.
type ForgeLogData struct {
	Job   ForgeJob
	Lines []string
	Cut   bool
	Live  time.Duration
	Err   string
	Later string
	Retry *ForgeRetry
}

// Head says how much of the log shows, and that it follows the job while it
// runs.
func (d ForgeLogData) Head() string {
	var head string
	switch {
	case len(d.Lines) == 0:
		head = "The log is empty."
	case d.Cut:
		head = fmt.Sprintf("The last %s of the log.", plural(len(d.Lines), "line"))
	default:
		head = fmt.Sprintf("The whole log, %s.", plural(len(d.Lines), "line"))
	}
	if d.Live > 0 {
		head += " Live while the job runs."
	}
	return head
}

// ForgeBranchData feeds the editor's forge sheet and its button in the
// statusbar: the forge the project is bound to, the badge of what the forge
// poller read of the branch, nil while it read nothing to show, the change
// request it offers to open, nil where it offers none, and the one the
// editor offers to publish line comments on, nil for none.
type ForgeBranchData struct {
	Kind    forge.Kind
	Badge   *ForgeBadge
	Offer   *ForgeChangeOffer
	Publish *ForgePublishOffer
	Threads *ForgeThreadsOffer
}

// Lead sums up the forge sheet in one line: the change request by its number
// and where it stands, the pipeline and why the last read failed, else the
// change request it offers to open.
func (d ForgeBranchData) Lead() string {
	var parts []string
	if b := d.Badge; b != nil {
		if b.Change != nil {
			parts = append(parts, b.ChangeRef()+" "+b.changeState())
		}
		if b.Pipeline != nil {
			parts = append(parts, b.PipelineTitle())
		}
		if b.Problem != "" {
			parts = append(parts, b.Problem)
		}
	}
	if len(parts) == 0 && d.Offer != nil {
		parts = append(parts, d.Offer.Label)
	}
	return strings.Join(parts, " · ")
}

// ForgePublishOffer is the open change request the editor offers to publish
// line comments on as review comments: the forge's name, the change
// request's noun and number in its words.
type ForgePublishOffer struct {
	Forge string
	Noun  string
	Ref   string
}

// NewForgePublishOffer is the offer to publish line comments on the change
// request of the branch the poller read on b, nil unless it stands open, the
// forge publishes review comments and its features are on for the branch.
func NewForgePublishOffer(b forge.Binding, p forge.Polled, publishes bool) *ForgePublishOffer {
	var off *forge.BranchOff
	if !publishes || !p.Status.Change.Opened() || errors.As(p.Err, &off) {
		return nil
	}
	k := b.Host.Kind
	return &ForgePublishOffer{Forge: k.Label, Noun: k.ChangeNoun, Ref: k.ChangeRef(p.Status.Change.Number)}
}

// ForgeThreadsOffer is the change request whose review threads the editor
// offers to answer and resolve: the forge's name, the change request's noun
// and number in its words, and which of the two the forge does.
type ForgeThreadsOffer struct {
	Forge   string
	Noun    string
	Ref     string
	Reply   bool
	Resolve bool
}

// NewForgeThreadsOffer is the offer to answer and resolve the review threads
// of the change request of the branch the poller read on b, the one whose
// threads the line comments show, nil without one, where the forge does
// neither or its features are off for the branch.
func NewForgeThreadsOffer(b forge.Binding, p forge.Polled, reply, resolve bool) *ForgeThreadsOffer {
	var off *forge.BranchOff
	if (!reply && !resolve) || p.Status.Change == nil || errors.As(p.Err, &off) {
		return nil
	}
	k := b.Host.Kind
	return &ForgeThreadsOffer{Forge: k.Label, Noun: k.ChangeNoun, Ref: k.ChangeRef(p.Status.Change.Number), Reply: reply, Resolve: resolve}
}

// Hint says where a reply to a review thread is posted and who reads it.
func (o ForgeThreadsOffer) Hint() string {
	return fmt.Sprintf("It is posted on %s %s on %s, for everybody who can read it there.", o.Noun, o.Ref, o.Forge)
}

// ForgeChangeOffer is the change request the forge sheet offers to open for a
// pushed branch without an open one: its label in the forge's words, the
// line under it, and the form it opens.
type ForgeChangeOffer struct {
	Label string
	Sub   string
	URL   string
}

// NewForgeChangeOffer is the offer to open a change request of the project's
// branch the poller read on b, nil unless it was pushed, has no open change
// request and is not the default branch the forge named for it.
func NewForgeChangeOffer(project string, b forge.Binding, p forge.Polled) *ForgeChangeOffer {
	w, status := p.Watch, p.Status
	if w.Head == "" || status.Change.Opened() || status.Default == "" || status.Default == w.Remote {
		return nil
	}
	return &ForgeChangeOffer{
		Label: "New " + b.Host.Kind.ChangeNoun,
		Sub:   fmt.Sprintf("%s into %s", w.Branch, status.Default),
		URL:   "/projects/" + project + "/forge/change?" + url.Values{"branch": {w.Branch}}.Encode(),
	}
}

// ForgeChangeData feeds the form that opens a change request of a pushed
// branch of a project, in the dialog (Modal) and as a page: the forge and
// the repository's address there, the branch and the commit it was pushed
// at, Local where the local branch stands when that is elsewhere, with the
// commits it holds that the pushed head lacks (Ahead) and the other way
// round (Behind), both 0 where they could not be counted, the branch it
// merges into, its title (ChangeTitle, a Title would hide the page's) and
// body, made of the commits the branch carries unless they were sent, and
// whether it opens as a draft. Carried counts those commits, Measured says
// whether the target was known to count them against. Exists is the
// branch's open change request, which the dialog shows in place of the
// form, Problem why none opens from here.
type ForgeChangeData struct {
	Page
	Modal       bool
	Project     string
	Return      string
	Kind        forge.Kind
	Address     string
	Branch      string
	Head        string
	Local       string
	Ahead       int
	Behind      int
	Target      string
	ChangeTitle string
	Body        string
	Draft       bool
	Carried     int
	Measured    bool
	Exists      *forge.Change
	Problem     string
	TitleMax    int
	BodyMax     int
	TargetMax   int
}

// Heading names what the form opens, in the forge's words.
func (d ForgeChangeData) Heading() string {
	if d.Kind.ChangeNoun == "" {
		return "New change request"
	}
	return "New " + d.Kind.ChangeNoun
}

// HeadShort is the pushed commit the way a line names it.
func (d ForgeChangeData) HeadShort() string {
	return forge.ShortHead(d.Head)
}

// LocalShort is where the local branch stands, the way a line names it.
func (d ForgeChangeData) LocalShort() string {
	return forge.ShortHead(d.Local)
}

// LocalLevel is the notice's level for where the local branch stands: a
// warning where it holds commits the change request would lack.
func (d ForgeChangeData) LocalLevel() string {
	if d.Ahead == 0 && d.Behind > 0 {
		return "info"
	}
	return "warning"
}

// LocalLine says how the local branch stands to the pushed head, after the
// commit it stands at. A push carries its commits only while it holds all
// of the pushed ones.
func (d ForgeChangeData) LocalLine() string {
	them := "them"
	if d.Ahead == 1 {
		them = "it"
	}
	switch {
	case d.Ahead > 0 && d.Behind > 0:
		return fmt.Sprintf("%s ahead of the pushed head and %d behind it: this %s lacks %s.", plural(d.Ahead, "commit"), d.Behind, d.Kind.ChangeNoun, them)
	case d.Ahead > 0:
		return fmt.Sprintf("%s ahead of the pushed head: push first to carry %s.", plural(d.Ahead, "commit"), them)
	case d.Behind > 0:
		return fmt.Sprintf("%s behind the pushed head, which this %s carries.", plural(d.Behind, "commit"), d.Kind.ChangeNoun)
	}
	return "not at the pushed head."
}

// ExistsBadge is the branch's open change request the way the badge shows it.
func (d ForgeChangeData) ExistsBadge() ForgeBadge {
	return ForgeBadge{Kind: d.Kind, Change: d.Exists}
}

// CarriedLine says which commits the title and body were made of.
func (d ForgeChangeData) CarriedLine() string {
	switch {
	case !d.Measured:
		return fmt.Sprintf("Made of the pushed head alone: %s is not fetched here.", d.Target)
	case d.Carried == 0:
		return fmt.Sprintf("%s carries no commit %s lacks, as this repository last fetched it.", d.Branch, d.Target)
	case d.Carried == 1:
		return fmt.Sprintf("Made of the one commit %s carries since %s.", d.Branch, d.Target)
	}
	return fmt.Sprintf("Made of the %d commits %s carries since %s.", d.Carried, d.Branch, d.Target)
}

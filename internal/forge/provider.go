package forge

import "context"

// Provider is what a provider subpackage gives the service. A capability
// beyond these is an interface of its own, which only a provider that has it
// implements.
type Provider interface {
	Kind() Kind
	// Test reads through the door who the token belongs to on the host and
	// which server answers. A failure is the sentence the user reads.
	Test(ctx context.Context, c *Client, h Host, token string) (Connection, error)
}

// TokenPager is a provider whose server has a page that creates a personal
// access token.
type TokenPager interface {
	// TokenPage is the page of h that creates a token named name with the
	// scopes every forge feature needs.
	TokenPage(h Host, name string) string
}

// StatusReader is a provider that reads a branch's change request and the
// pipeline of the commit it was last pushed at.
type StatusReader interface {
	// BranchStatus reads the change requests whose source is branch of the
	// bound repository itself, never a fork's of the same name, and picks
	// one with PickChange; and the pipeline of head with its jobs, none when
	// head is "". A failure is the sentence the user reads.
	BranchStatus(ctx context.Context, c *Client, b Binding, token, branch, head string) (BranchStatus, error)
}

// LogReader is a provider that reads the log a forge keeps of a job.
type LogReader interface {
	// JobLog reads the log of job, a job of a pipeline BranchStatus read on
	// b whose NoLog is "", and answers its last keep bytes and whether more
	// came before them. A failure is the sentence the user reads.
	JobLog(ctx context.Context, c *Client, b Binding, token string, job Job, keep int) ([]byte, bool, error)
}

// LiveLogReader is a LogReader whose forge hands out the log of a job while
// it runs, as far as the job wrote it. A forge that hands a log out only once
// its job ended is a LogReader alone.
type LiveLogReader interface {
	LogReader
	// LiveLogs marks the capability, JobLog reads the log.
	LiveLogs()
}

// ThreadReader is a provider that reads the review threads of a change
// request.
type ThreadReader interface {
	// Threads reads the review threads of the change request with that
	// number on b, those on its diff and the general ones apart, without any
	// note the forge writes itself. A failure is the sentence the user reads.
	Threads(ctx context.Context, c *Client, b Binding, token string, number int) (Review, error)
}

// Retrier is a provider that runs failed jobs again. Its Kind names the verb
// its users say it with.
type Retrier interface {
	// RetryPipeline runs the jobs that failed p again, a failed pipeline
	// BranchStatus read on b at head whose NoRetry is "". A failure is the
	// sentence the user reads.
	RetryPipeline(ctx context.Context, c *Client, b Binding, token, head string, p Pipeline) error
	// RetryJob runs job again, a failed job of a pipeline BranchStatus read
	// on b whose NoRetry is "". A failure is the sentence the user reads.
	RetryJob(ctx context.Context, c *Client, b Binding, token string, job Job) error
}

// ChangeOpener is a provider that opens change requests.
type ChangeOpener interface {
	// DefaultBranch reads the branch of b's repository a change request
	// merges into unless it names another. A failure is the sentence the
	// user reads.
	DefaultBranch(ctx context.Context, c *Client, b Binding, token string) (string, error)
	// OpenChange opens a change request of the branch source of b's
	// repository itself into the branch o names, a draft where o asks for
	// one, and answers it as the forge has it. A failure is the sentence the
	// user reads.
	OpenChange(ctx context.Context, c *Client, b Binding, token, source string, o NewChange) (Change, error)
}

// Publisher is a provider that publishes notes as review threads on the diff
// of a change request.
type Publisher interface {
	// ChangeDiff reads the diff of ch, an open change request of b's
	// repository, as the forge shows it now. A failure is the sentence the
	// user reads.
	ChangeDiff(ctx context.Context, c *Client, b Binding, token string, ch Change) (Diff, error)
	// PublishNote opens a review thread of p on ch, placed on d, the diff
	// ChangeDiff read of it, and answers its note as the forge has it. A
	// failure is the sentence the user reads.
	PublishNote(ctx context.Context, c *Client, b Binding, token string, ch Change, d Diff, p Placed) (Note, error)
}

// Replier is a provider that answers review threads.
type Replier interface {
	// Reply posts body as a note of t, a thread on the diff of ch that
	// Threads read, and answers the note as the forge has it. A failure is
	// the sentence the user reads.
	Reply(ctx context.Context, c *Client, b Binding, token string, ch Change, t Thread, body string) (Note, error)
}

// Resolver is a provider that resolves review threads.
type Resolver interface {
	// Resolve resolves t, a thread on the diff of ch that Threads read
	// resolvable and not resolved. A failure is the sentence the user reads.
	Resolve(ctx context.Context, c *Client, b Binding, token string, ch Change, t Thread) error
}

// Connection is what a test of a host's token found: the account the token
// belongs to, its display name if it has one, and the server in the
// provider's words, its version included where the server names one.
type Connection struct {
	Account string
	Name    string
	Server  string
}

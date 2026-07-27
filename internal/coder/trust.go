package coder

// WorkdirTruster is the optional capability of a coder runtime to record a
// working directory as trusted in the CLI's own configuration, the way the CLI
// records it after its trust dialog was answered.
//
// It exists because a coder that comes up in a directory its CLI has never
// seen asks "do you trust the files in this folder?" before it reads anything
// else, and the task travels in the argv: the session sits on a dialog with
// the work behind it while the caller was told the coder is working. There is
// no flag for it on either CLI, the answer is state in the CLI's own config,
// so this writes what the dialog would have written. A runtime whose CLI has
// no such state leaves the method out.
type WorkdirTruster interface {
	TrustWorkdir(workdir string) error
}

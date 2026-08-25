package opencode

// opencode asks no workspace trust question. claude and copilot stop a fresh
// directory on a "do you trust this folder?" dialog and keep the answer in
// their config, which is why their runtimes implement coder.WorkdirTruster
// and write that answer ahead of a start. opencode has no such dialog and no
// such state (verified against 1.18.23: the TUI starts straight into the
// project, and neither the CLI nor its repository carries a trusted-folder
// record), so there is nothing a truster could write and the runtime leaves
// the capability out on purpose. What gates a session's tool use instead is
// opencode's permission configuration, and the one choice the cockpit makes
// about it is the --auto flag when a session is started with automatic
// approval, see runtime.go.

package cli

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/clirun"
	coderopencode "github.com/marein/dev-cockpit/internal/coder/opencode"
)

// The argv the opencode runner builds for a compaction reaches the helper
// command of this binary, with the session as its one argument.
func TestTheCompactionArgvReachesItsHelper(t *testing.T) {
	argv := append(append([]string{}, coderopencode.CompactHelper...), "--", "ses_abc")
	found, rest, err := newRootCommand().Find(argv)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Name() != coderopencode.CompactHelper[1] || found.Parent().Name() != coderopencode.CompactHelper[0] {
		t.Fatalf("the argv reaches %s", found.CommandPath())
	}
	if err := found.ParseFlags(rest); err != nil {
		t.Fatalf("flags: %v", err)
	}
	if args := found.Flags().Args(); len(args) != 1 || args[0] != "ses_abc" || found.ValidateArgs(args) != nil {
		t.Fatalf("the helper reads %v", args)
	}
}

// The argv clirun.DiesWithParent starts a guard with reaches the guard command
// of this binary, a hidden one of the root.
func TestTheGuardArgvReachesItsCommand(t *testing.T) {
	found, rest, err := newRootCommand().Find(clirun.GuardCommand)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Name() != clirun.GuardCommand[0] || found.Parent() != found.Root() || !found.Hidden || len(rest) != 0 {
		t.Fatalf("the argv reaches %s with %v", found.CommandPath(), rest)
	}
}

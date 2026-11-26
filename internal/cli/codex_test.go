package cli

import (
	"testing"

	codercodex "github.com/marein/dev-cockpit/internal/coder/codex"
)

// The argv the codex runner builds for a compaction reaches the helper command
// of this binary, with the model as its flag and the session as its argument.
func TestTheCodexCompactionArgvReachesItsHelper(t *testing.T) {
	argv := append(append([]string{}, codercodex.CompactHelper...), "--model=gpt-6.2-sol", "--", "01a21186-379d-7773-a007-91ee407c3c2d")
	found, rest, err := newRootCommand().Find(argv)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Name() != codercodex.CompactHelper[1] || found.Parent().Name() != codercodex.CompactHelper[0] || !found.Parent().Hidden {
		t.Fatalf("the argv reaches %s", found.CommandPath())
	}
	if err := found.ParseFlags(rest); err != nil {
		t.Fatalf("flags: %v", err)
	}
	model, _ := found.Flags().GetString("model")
	if args := found.Flags().Args(); len(args) != 1 || args[0] != "01a21186-379d-7773-a007-91ee407c3c2d" || model != "gpt-6.2-sol" || found.ValidateArgs(args) != nil {
		t.Fatalf("the helper reads %v on %q", args, model)
	}
}

package opencode

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

// opencode asks no workspace trust question and keeps no trusted-folder
// state, so there is nothing a truster could write: the runtime leaves the
// capability out on purpose, and the manager then pre-trusts nothing. Should
// a version grow such a dialog, this test is the reminder that the capability
// has to be built, not silently skipped.
func TestTheRuntimeDoesNotClaimATrustDialog(t *testing.T) {
	var r any = runtime{}
	if _, ok := r.(coder.WorkdirTruster); ok {
		t.Fatal("opencode has no trust dialog, the runtime must not pretend to answer one")
	}
}

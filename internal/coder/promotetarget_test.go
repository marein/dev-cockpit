package coder

import "testing"

// The promote renames the pane onto the record the CLI just created, and
// picking the wrong one hands the session an id its CLI does not drive. So
// without a name to match on, the working directory has to point at exactly
// one record.
func TestPromoteTargetPicksOneRecordOrNone(t *testing.T) {
	here, elsewhere := "/work/app", "/work/other"
	fresh := Session{SessionID: "new-1", CWD: here}
	second := Session{SessionID: "new-2", CWD: here}
	old := Session{SessionID: "old-1", CWD: here}
	seen := map[string]bool{old.SessionID: true}

	t.Run("one new record in the project", func(t *testing.T) {
		got, found, ambiguous := promoteTarget([]Session{old, fresh, {SessionID: "x", CWD: elsewhere}}, seen, here, "", false)
		if !found || ambiguous || got.SessionID != fresh.SessionID {
			t.Fatalf("got %q found=%v ambiguous=%v, want the fresh record", got.SessionID, found, ambiguous)
		}
	})

	t.Run("nothing yet", func(t *testing.T) {
		if _, found, ambiguous := promoteTarget([]Session{old}, seen, here, "", false); found || ambiguous {
			t.Fatalf("found=%v ambiguous=%v, want the promote to keep waiting", found, ambiguous)
		}
	})

	t.Run("two new records are a guess this does not make", func(t *testing.T) {
		_, found, ambiguous := promoteTarget([]Session{fresh, second}, seen, here, "", false)
		if found || !ambiguous {
			t.Fatalf("found=%v ambiguous=%v, want no promote at all", found, ambiguous)
		}
	})

	t.Run("a name still decides, and two of them do not stop it", func(t *testing.T) {
		named := Session{SessionID: "new-3", CWD: here, Name: "the task"}
		got, found, ambiguous := promoteTarget([]Session{fresh, named, second}, seen, here, "the task", true)
		if !found || ambiguous || got.SessionID != named.SessionID {
			t.Fatalf("got %q found=%v ambiguous=%v, want the record carrying the name", got.SessionID, found, ambiguous)
		}
	})
}

package filesystem

import "testing"

// A name search ranks by the rule a file search does: the first token's hit
// in the name decides, a word start first, a hit only beside the name last,
// and every token has to stand somewhere.
func TestRankQuickOpenGradesNamesLikeFileNames(t *testing.T) {
	cases := []struct {
		name, context, query string
		tier, pos            int
		ok                   bool
	}{
		{"GameController", "app/http", "controller", tierWordStart, 4, true},
		{"GameController", "app/http", "ontrol", tierInsideWord, 5, true},
		{"GameController", "app/http", "http game", tierOutsideName, 0, true},
		{"GameController", "app/http", "game http", tierWordStart, 0, true},
		{"GameController", "app/http", "game cli", 0, 0, false},
		{"HTTPClient", "net", "client", tierWordStart, 4, true},
		{"Service", "", "", 0, 0, true},
	}
	for _, tc := range cases {
		tier, pos, ok := RankQuickOpen(tc.name, tc.context, tc.query)
		if ok != tc.ok || (ok && (tier != tc.tier || pos != tc.pos)) {
			t.Errorf("%s %q: %d %d %v, want %d %d %v", tc.name, tc.query, tier, pos, ok, tc.tier, tc.pos, tc.ok)
		}
	}
}

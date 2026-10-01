package filesystem

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// syntheticQuickOpenIndex builds an index of n made up paths, two to six
// folders deep, with file names joined from the same words by the separators
// the ranking knows, so a query hits word starts, word insides and folders.
func syntheticQuickOpenIndex(n int) *quickOpenIndex {
	words := []string{"src", "internal", "web", "render", "config", "chat", "game", "lobby",
		"player", "profile", "leaderboard", "matchmaking", "connect", "four", "replay", "domain",
		"application", "test", "unit", "vendor", "assets", "static", "components", "ranking",
		"controller", "client", "http"}
	seps := []string{"-", "_", ".", ""}
	exts := []string{".go", ".php", ".js", ".md", ".yml", ".css"}
	r := rand.New(rand.NewPCG(1, 2))
	ix := &quickOpenIndex{paths: make([]string, n), lower: make([]string, n)}
	var b strings.Builder
	for i := range n {
		b.Reset()
		for range 2 + r.IntN(5) {
			b.WriteString(words[r.IntN(len(words))])
			b.WriteByte('/')
		}
		for j := range 1 + r.IntN(3) {
			if j > 0 {
				b.WriteString(seps[r.IntN(len(seps))])
			}
			word := words[r.IntN(len(words))]
			if r.IntN(2) == 0 {
				word = strings.ToUpper(word[:1]) + word[1:]
			}
			b.WriteString(word)
		}
		b.WriteString(exts[r.IntN(len(exts))])
		ix.paths[i] = b.String()
		ix.lower[i] = string(appendLowerASCII(nil, ix.paths[i]))
	}
	return ix
}

func BenchmarkQuickOpenQuery(b *testing.B) {
	ix := syntheticQuickOpenIndex(1_000_000)
	for _, query := range []string{"play", "lobby", "lay", "controller", "match go", "zzz"} {
		b.Run(query, func(b *testing.B) {
			for b.Loop() {
				ix.query(query, "", QuickOpenLimit)
			}
		})
	}
}

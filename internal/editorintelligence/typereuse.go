package editorintelligence

import (
	"encoding/json"
	"sync"
	"time"
)

// typeReuseFor bounds how long an answer of a type search stands in for the
// server's in the searches of the same client that follow it, about the
// time a query takes to type.
const typeReuseFor = 10 * time.Second

// typeReuse keeps the answers of each client's last type search per project
// and language. While a query is typed, its words are asked one by one and
// most of them stay as they were from one search to the next. intelephense
// keeps its last query only, so asked again after another word it answers
// as slowly as for a new one, and a word that filled its limit would set
// off its crowded plan again on every key.
type typeReuse struct {
	mu     sync.Mutex
	rounds map[typeReuseKey]*typeRound
}

type typeReuseKey struct {
	client, project, profile string
}

// typeRound is one search's answers by ask, with what reading them needs of
// the connection they came over.
type typeRound struct {
	root     string
	roots    []SourceRoot
	launcher Launcher
	answers  map[string]typeAnswer
}

// typeAnswer is one ask's answer and when the server gave it.
type typeAnswer struct {
	syms []lspSymbol
	cut  bool
	at   time.Time
}

// askKey tells asks apart by their query and the settings sent before them.
func askKey(ask symbolAsk) string {
	settings, _ := json.Marshal(ask.settings)
	return ask.query + "\x00" + string(settings)
}

// last answers the client's last search in the language, nil without one.
func (r *typeReuse) last(key typeReuseKey) *typeRound {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rounds[key]
}

// keep stores a search as the client's last one in the language and drops
// every search whose answers all stood too long.
func (r *typeReuse) keep(key typeReuseKey, round *typeRound, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rounds == nil {
		r.rounds = map[typeReuseKey]*typeRound{}
	}
	for k, old := range r.rounds {
		if !old.fresh(now) {
			delete(r.rounds, k)
		}
	}
	r.rounds[key] = round
}

func (round *typeRound) fresh(now time.Time) bool {
	for _, a := range round.answers {
		if now.Sub(a.at) < typeReuseFor {
			return true
		}
	}
	return false
}

// answer finds the answer the search gave the same ask, while it is young
// enough. Nil-receiver-safe for a client without a last search.
func (round *typeRound) answer(ask symbolAsk, now time.Time) (typeAnswer, bool) {
	if round == nil {
		return typeAnswer{}, false
	}
	a, ok := round.answers[askKey(ask)]
	return a, ok && now.Sub(a.at) < typeReuseFor
}

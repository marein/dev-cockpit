package assistant

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// askedAssistant is an assistant whose last answer asks the two questions of
// choicesAnswer, still open.
func askedAssistant(t *testing.T) (*Service, *Store, string, string) {
	t.Helper()
	return askedWith(t, choicesAnswer)
}

func askedWith(t *testing.T, answer string) (*Service, *Store, string, string) {
	t.Helper()
	svc, store, _ := newTestService(t, &fakeRunner{events: []Event{{Kind: EventDelta, Text: answer}}})
	created, _ := svc.create("claude")
	if _, err := svc.Send(created.ID, "Set the app up", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	return svc, store, created.ID, waitIdle(t, svc, created.ID).ID
}

// The words and the answers are two parts of one draft, saved apart: each
// save keeps the other part, the words keep their own time, and a draft that
// holds nothing but answers is stored too. The notes stay as typed.
func TestWordsAndAnswersAreSavedApartAndKeepEachOther(t *testing.T) {
	svc, store, id, asked := askedAssistant(t)
	picks := []Pick{{Option: 1}, {Option: -1, Text: " after Friday "}}
	if changed, err := svc.SaveAnswers(id, asked, picks); err != nil || !changed {
		t.Fatalf("save answers: %v (changed %v)", err, changed)
	}
	if _, err := os.Stat(filepath.Join(store.InstanceDir(id), draftFileName)); err != nil {
		t.Fatalf("want a draft of answers alone stored: %v", err)
	}
	if got, _ := svc.Draft(id); got.Text != "" || !reflect.DeepEqual(got.Answers[asked], picks) {
		t.Fatalf("want the answers as typed, got %+v", got)
	}
	if _, changed, _ := svc.SaveDraft(id, "half a thought", nil); !changed {
		t.Fatal("want the words stored beside the answers")
	}
	got, _ := svc.Draft(id)
	if got.Text != "half a thought" || !reflect.DeepEqual(got.Answers[asked], picks) {
		t.Fatalf("want the words beside the answers, got %+v", got)
	}
	if changed, _ := svc.SaveAnswers(id, asked, []Pick{{Option: 0}}); !changed {
		t.Fatal("want the new answers stored")
	}
	again, _ := svc.Draft(id)
	if again.Text != "half a thought" || !again.UpdatedAt.Equal(got.UpdatedAt) || !reflect.DeepEqual(again.Answers[asked], []Pick{{Option: 0}, {Option: -1}}) {
		t.Fatalf("want the words and their time kept beside one pick per question, got %+v", again)
	}
}

// A repeated save writes nothing, the thread and the index never, and a block
// whose answers are all taken back has no entry.
func TestARepeatedAnswersSaveWritesNothing(t *testing.T) {
	svc, store, id, asked := askedAssistant(t)
	transcript, _ := os.ReadFile(store.transcriptPath(id))
	index, _ := os.ReadFile(store.indexPath)
	picks := []Pick{{Option: 1}, {Option: -1, Text: "note"}}
	if changed, err := svc.SaveAnswers(id, asked, picks); err != nil || !changed {
		t.Fatalf("save answers: %v (changed %v)", err, changed)
	}
	if fresh, _ := os.ReadFile(store.transcriptPath(id)); string(fresh) != string(transcript) {
		t.Fatal("an answers save rewrote the transcript")
	}
	if fresh, _ := os.ReadFile(store.indexPath); string(fresh) != string(index) {
		t.Fatal("an answers save rewrote the index")
	}
	path := filepath.Join(store.InstanceDir(id), draftFileName)
	age := func() {
		t.Helper()
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	age()
	if changed, err := svc.SaveAnswers(id, asked, picks); err != nil || changed {
		t.Fatalf("want a repeated save to change nothing: %v (changed %v)", err, changed)
	}
	if info, _ := os.Stat(path); time.Since(info.ModTime()) < time.Minute {
		t.Fatal("a repeated save wrote the draft")
	}
	empty := []Pick{{Option: -1}, {Option: -1}}
	if changed, _ := svc.SaveAnswers(id, asked, empty); !changed {
		t.Fatal("want the answers taken back")
	}
	if got, _ := svc.Draft(id); got.Answers[asked] != nil {
		t.Fatalf("want no entry for a block with nothing in it, got %+v", got.Answers)
	}
	age()
	if changed, _ := svc.SaveAnswers(id, asked, empty); changed {
		t.Fatal("want nothing taken back twice")
	}
	if info, _ := os.Stat(path); time.Since(info.ModTime()) < time.Minute {
		t.Fatal("taking back nothing wrote the draft")
	}
}

// Two devices saving at nearly the same moment, one the words and the other
// the answers, both end in the draft.
func TestWordsAndAnswersSavedAtOnceBothStay(t *testing.T) {
	svc, _, id, asked := askedAssistant(t)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _, _ = svc.SaveDraft(id, fmt.Sprintf("words %d", i), nil)
		}()
		go func() {
			defer wg.Done()
			_, _ = svc.SaveAnswers(id, asked, []Pick{{Option: i % 2}, {Option: -1, Text: fmt.Sprintf("note %d", i)}})
		}()
	}
	wg.Wait()
	got, _ := svc.Draft(id)
	if !strings.HasPrefix(got.Text, "words ") || len(got.Answers[asked]) != 2 || !strings.HasPrefix(got.Answers[asked][1].Text, "note ") {
		t.Fatalf("want both parts kept, got %+v", got)
	}
}

// Sending the answers spends their entry and leaves the composer's words with
// their time. A save that comes after finds the block answered and stores
// nothing.
func TestSendingTheAnswersSpendsTheirEntryAndKeepsTheWords(t *testing.T) {
	svc, _, id, asked := askedAssistant(t)
	if _, _, err := svc.SaveDraft(id, "half a thought", nil); err != nil {
		t.Fatal(err)
	}
	picks := []Pick{{Option: 1}, {Option: 0, Text: "today"}}
	if _, err := svc.SaveAnswers(id, asked, picks); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.Draft(id)
	if _, err := svc.Answer(id, asked, picks); err != nil {
		t.Fatalf("answer: %v", err)
	}
	waitIdle(t, svc, id)
	got, _ := svc.Draft(id)
	if len(got.Answers) != 0 || got.Text != "half a thought" || !got.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("want the entry spent and the words as they were, got %+v", got)
	}
	if changed, err := svc.SaveAnswers(id, asked, []Pick{{Option: 0}}); err == nil || changed {
		t.Fatalf("want a save for an answered block refused, got %v (changed %v)", err, changed)
	}
	if got, _ := svc.Draft(id); len(got.Answers) != 0 {
		t.Fatalf("want nothing stored for an answered block, got %+v", got.Answers)
	}
}

// A message of the user that answers nothing passes the open block over, and
// its entry goes with the composer's words.
func TestAMessageThatPassesABlockOverSpendsItsEntry(t *testing.T) {
	svc, _, id, asked := askedAssistant(t)
	if _, err := svc.SaveAnswers(id, asked, []Pick{{Option: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Send(id, "Never mind, something else", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, id)
	if got, _ := svc.Draft(id); got.Answers[asked] != nil {
		t.Fatalf("want the passed block's entry gone, got %+v", got.Answers)
	}
}

// An answers save is checked against the block as stored, like a send, and a
// refused one stores nothing.
func TestAnAnswersSaveIsCheckedAgainstTheBlock(t *testing.T) {
	svc, _, id, asked := askedAssistant(t)
	instance, _ := svc.Get(id)
	for name, tc := range map[string]struct {
		message string
		picks   []Pick
	}{
		"unknown answer":  {"nope", []Pick{{Option: 0}}},
		"asks nothing":    {instance.Messages[0].ID, []Pick{{Option: 0}}},
		"no such option":  {asked, []Pick{{Option: 2}}},
		"below none":      {asked, []Pick{{Option: -2}}},
		"more than asked": {asked, []Pick{{Option: -1}, {Option: -1}, {Option: 0}}},
		"note too long":   {asked, []Pick{{Option: -1, Text: strings.Repeat("x", MaxChoiceNoteRunes+1)}}},
		"several on one":  {asked, []Pick{{Option: -1, Options: []int{0, 1}}}},
	} {
		if changed, err := svc.SaveAnswers(id, tc.message, tc.picks); err == nil || changed {
			t.Errorf("%s: want a refusal, got %v (changed %v)", name, err, changed)
		}
	}
	if _, err := svc.SaveAnswers("11111111-1111-4111-8111-111111111111", asked, []Pick{{Option: 0}}); err == nil {
		t.Error("want an unknown assistant refused")
	}
	if got, _ := svc.Draft(id); got.Answers != nil {
		t.Fatalf("want nothing stored, got %+v", got.Answers)
	}
}

// Picks on a question that takes several are stored in the question's order:
// one more pick or one taken back is a change, the same picks in another
// order are none, and a block with every pick taken back has no entry. A list
// longer than the question's options never reaches the draft.
func TestAChangedPickOnAQuestionThatTakesSeveralIsStored(t *testing.T) {
	svc, _, id, asked := askedWith(t, mixedAnswer)
	for i, step := range []struct {
		options []int
		changed bool
		want    []int
	}{
		{[]int{0}, true, []int{0}},
		{[]int{2, 0}, true, []int{0, 2}},
		{[]int{0, 2}, false, []int{0, 2}},
		{[]int{2}, true, []int{2}},
	} {
		changed, err := svc.SaveAnswers(id, asked, []Pick{{Option: -1, Options: step.options}})
		if err != nil || changed != step.changed {
			t.Fatalf("step %d: want changed %v, got %v %v", i, step.changed, changed, err)
		}
		if got, _ := svc.Draft(id); !reflect.DeepEqual(got.Answers[asked], []Pick{{Option: -1, Options: step.want}, {Option: -1}}) {
			t.Fatalf("step %d: want %v stored, got %+v", i, step.want, got.Answers[asked])
		}
	}
	if changed, err := svc.SaveAnswers(id, asked, []Pick{{Option: -1, Options: []int{0, 1, 2, 0, 1}}}); err == nil || changed {
		t.Fatalf("want a list longer than the options refused, got %v (changed %v)", err, changed)
	}
	if changed, _ := svc.SaveAnswers(id, asked, []Pick{{Option: -1}}); !changed {
		t.Fatal("want the picks taken back")
	}
	if got, _ := svc.Draft(id); got.Answers[asked] != nil {
		t.Fatalf("want no entry for a block with nothing in it, got %+v", got.Answers)
	}
}

// A draft stored before a question could take several picks reads as it
// always did, and saving the same picks again writes nothing.
func TestADraftOfSinglePicksStoredBeforeReadsAsBefore(t *testing.T) {
	svc, store, id, asked := askedAssistant(t)
	path := filepath.Join(store.InstanceDir(id), draftFileName)
	if err := os.WriteFile(path, []byte(`{"text":"words","answers":{"`+asked+`":[{"option":1},{"option":-1,"text":"note"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	picks := []Pick{{Option: 1}, {Option: -1, Text: "note"}}
	if got, _ := svc.Draft(id); got.Text != "words" || !reflect.DeepEqual(got.Answers[asked], picks) {
		t.Fatalf("want the stored draft as before, got %+v", got)
	}
	if changed, err := svc.SaveAnswers(id, asked, picks); err != nil || changed {
		t.Fatalf("want the same picks to change nothing: %v (changed %v)", err, changed)
	}
}

package assistant

import "testing"

// One reading of what can be read aloud: the transcript renders the speaker
// from it and the audio route asks it again, so a button that stands always
// reaches an answer. A check's report is a message like any other here, its
// words go to the user the way an answer does.
func TestSpeakableTakesEveryFinishedMessageButTheUsers(t *testing.T) {
	cases := []struct {
		name string
		m    Message
		want bool
	}{
		{"a check's report", Message{Role: RoleCockpit, Content: "it is done", State: StateComplete, Note: &Note{Source: NoteCheck}}, true},
		{"an answer", Message{Role: RoleAssistant, Content: "it is done", State: StateComplete}, true},
		{"an answer nobody asked for", Message{Role: RoleAssistant, Content: "it is done", State: StateComplete, Auto: true}, true},
		{"what the user typed", Message{Role: RoleUser, Content: "do it", State: StateComplete}, false},
		{"an answer still being written", Message{Role: RoleAssistant, Content: "it is", State: StateStreaming}, false},
		{"a report with no words in it", Message{Role: RoleCockpit, State: StateComplete, Note: &Note{Source: NoteCheck}}, false},
	}
	for _, c := range cases {
		if got := c.m.Speakable(); got != c.want {
			t.Errorf("%s reads as %v, want %v", c.name, got, c.want)
		}
	}
}

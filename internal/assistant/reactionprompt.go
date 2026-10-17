package assistant

import (
	"fmt"
	"strings"
)

// eventsHeadline is the line a reaction of one or several events is read by.
func eventsHeadline(events []CockpitEvent) string {
	if len(events) == 1 {
		return oneLine(events[0].Headline)
	}
	return fmt.Sprintf("%d events arrived", len(events))
}

// eventsBody is what the reaction reads about the events: the one event's
// body, or every event with its headline in front.
func eventsBody(events []CockpitEvent) string {
	if len(events) == 1 {
		return strings.TrimSpace(events[0].Body)
	}
	var b strings.Builder
	for i, ev := range events {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "**%s**", oneLine(ev.Headline))
		if body := strings.TrimSpace(ev.Body); body != "" {
			b.WriteString("\n\n" + body)
		}
	}
	return b.String()
}

// noteName is what a headline calls a coder: its name, else its terminal.
func noteName(name, terminal string) string {
	if strings.TrimSpace(name) == "" {
		return terminal
	}
	return strings.TrimSpace(name)
}

// inProject is the " in <project>" tail of a headline, empty without one.
func inProject(project string) string {
	if strings.TrimSpace(project) == "" {
		return ""
	}
	return " in " + strings.TrimSpace(project)
}

// reactionPrompt is what a reaction reads: the cockpit speaking, the event
// and the task. The session is the reaction's own, so the prompt says what
// it can see and where its answer lands, and it carries the one word that
// means nothing to say.
//
// It also says what a check's prompt says about its first line, and for the
// same reason: this answer travels to a phone, where a sentence or two of it
// is all there is (answerExcerpt in the cli package cuts it). A check may
// think out loud because parseVerdict throws the preamble away; here nothing
// is thrown away, so an announcement in front of the result eats the message
// itself. And a reaction has no verdict: it reads the same instruction file a
// check reads, which is where a DONE in an answer nobody asked for comes
// from.
//
// It says what a check's prompt says about the two hours too, and for a
// sharper reason: a check that runs into the deadline has a next check to
// carry on, a reaction has nothing and lands in the thread as one that broke
// off.
func reactionPrompt(origin Note, body string) string {
	var b strings.Builder
	b.WriteString("This turn was started by the cockpit, not by the user: an event you set a trigger on fired. You run in a session of your own and see nothing of the conversation; your answer is pushed into your thread, where the user reads it as one started without them.\n\n")
	if origin.Count > 1 {
		fmt.Fprintf(&b, "%d events arrived inside one window, here they are:\n\n", origin.Count)
	} else {
		fmt.Fprintf(&b, "Event: %s\n\n", oneLine(origin.Occasion()))
	}
	if body = strings.TrimSpace(body); body != "" {
		b.WriteString(body + "\n\n")
	}
	if task := strings.TrimSpace(origin.Task); task != "" {
		fmt.Fprintf(&b, "Your task for this event: %s\n\n", task)
	}
	b.WriteString("This turn has two hours and is killed when that runs out, and nothing carries on after it: if you cannot finish inside that, answer with what you have and what is still open.\n\n")
	b.WriteString("Your whole answer starts with the result. Do not announce what you are about to do: the user reads its first line on their phone, where a sentence or two is all that fits. You have no verdict here: DONE, BLOCKED and WORKING belong to a check, not to a reaction. Write in the language the task is written in.\n\n")
	b.WriteString("If there is nothing to do or say, answer NOTHING on the first line and nothing else: then nothing is pushed and nobody is notified. Otherwise answer as you would answer the user, they read it.")
	return b.String()
}

// quietAnswer reports whether a reaction had nothing to say. The contract is
// NOTHING and nothing else, and the word is found by the check's own reading
// (parseVerdict) rather than by a second one: a model that talks first and
// puts its NOTHING behind the preamble means what a model answering it bare
// means, and two readings of one habit drift apart, which is how a reaction
// that had decided right rang the user anyway. That reading's guards against
// prose come with it, upper case and no other verdict beside it, and what is
// left of the answer is the last of them: a reaction that carries the word
// into something it wrote for the user leaves that text standing behind it,
// and text left over is an answer to push. A turn that wrote nothing at all
// said nothing, which is not the same as saying NOTHING, so it is pushed like
// any other.
func quietAnswer(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	verdict, rest := parseVerdict(text)
	return verdict == VerdictNothing && rest == ""
}

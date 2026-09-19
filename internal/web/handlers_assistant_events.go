package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// assistantSubscriptionsPath is where the subscriptions live: the list, the
// two actions on them, and the path `dev-cockpit assistant subscription-new`
// posts to. One path for every assistant's subscriptions, like the jobs;
// which assistant one belongs to travels in the request and in the answer.
const assistantSubscriptionsPath = "/assistants/subscriptions"

// handleAssistantSubscriptions serves the subscriptions of one assistant, as
// the fragment the page's aside pulls (`?assistant=<id>`) and as the JSON the
// `subscription-list` command reads. A call over the socket that names no
// assistant lists the caller's own, `all` everybody's.
func (s *Server) handleAssistantSubscriptions(c *gin.Context) {
	owner := strings.TrimSpace(c.Query("assistant"))
	if owner == "" && s.localCall(c) {
		owner = s.callingAssistant(c)
	}
	if owner == "all" {
		owner = ""
	}
	data := s.assistantSubscriptionsData(c, owner)
	if wantsJSON(c.Request) {
		rows := make([]gin.H, 0, len(data.Subscriptions))
		for _, view := range data.Subscriptions {
			sub, _ := s.assistants.Events().Get(view.ID)
			rows = append(rows, gin.H{
				"id":           view.ID,
				"owner":        view.OwnerID,
				"ownerName":    s.assistantName(view.OwnerID),
				"event":        assistant.EventOption{Source: view.Source, Kind: view.Kind}.Name(),
				"label":        view.Label,
				"where":        view.Where,
				"targets":      subscriptionTargets(sub),
				"all":          sub.All,
				"spec":         sub.Spec,
				"task":         sub.Task,
				"once":         sub.Once,
				"state":        view.State,
				"fired":        sub.Fired,
				"lastFiredAt":  machineTime(sub.LastFiredAt),
				"nextAt":       machineTime(sub.NextAt),
				"expiresAt":    machineTime(sub.ExpiresAt),
				"maxPerHour":   sub.MaxPerHour,
				"batchSeconds": sub.BatchSeconds,
				"pending":      len(sub.Pending),
				"reacting":     sub.Reacting(),
				"note":         sub.Note,
			})
		}
		c.JSON(http.StatusOK, gin.H{"subscriptions": rows, "owners": data.Owners})
		return
	}
	c.HTML(http.StatusOK, "assistant_subscriptions_list.gohtml", data)
}

// handleAssistantSubscriptionsAction is the POST side of that path, dispatching
// on the hidden form field like every other form of the cockpit, serving the
// page's form and the assistant's own commands through the same handlers.
func (s *Server) handleAssistantSubscriptionsAction(c *gin.Context) {
	switch strings.TrimSpace(c.PostForm("form")) {
	case "new":
		s.assistantSubscribe(c)
	case "edit":
		s.assistantSubscriptionEdit(c)
	case "remove":
		s.assistantUnsubscribe(c)
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}

// assistantSubscribe makes one subscription. Who it belongs to is decided the
// way a job's owner is (steerOwner): a turn is itself, the page names one. Each
// coder target has to be a running coder, so the row can name it; a job target
// is checked by the reactor against the owner's jobs.
func (s *Server) assistantSubscribe(c *gin.Context) {
	owner, err := s.steerOwner(c)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	spec, err := s.assistantSubscriptionSpec(c, "", nil)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	spec.Owner = owner
	sub, err := s.assistants.Events().Subscribe(spec)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	summary := s.assistantSubscriptionSummary(sub)
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"id": sub.ID, "summary": summary, "state": string(sub.State)})
		return
	}
	s.redirectWithFlash(c, "/assistants/"+owner, "Subscribed: "+summary+".", "")
}

// assistantSubscriptionEdit changes one, from the row's menu on the page or
// from `subscription-edit`. The user changes any, an assistant only its own,
// the rule the removal follows. The event is not read off the request here:
// the form's select is locked and posts nothing, and a caller that does name
// one is refused by the reactor unless it is the event the subscription
// already has.
func (s *Server) assistantSubscriptionEdit(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))
	by := ""
	if s.localCall(c) {
		from, err := s.assistantCaller(c)
		if err != nil {
			s.assistantJobError(c, err)
			return
		}
		by = from
	}
	sub, ok := s.assistants.Events().Get(id)
	if !ok {
		s.assistantJobError(c, errors.New("No subscription has that id."))
		return
	}
	spec, err := s.assistantSubscriptionSpec(c, sub.Source, sub.Terminals())
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	next, changed, err := s.assistants.Events().Edit(id, by, spec)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"id": next.ID, "summary": s.assistantSubscriptionSummary(next), "changed": changed})
		return
	}
	s.redirectWithFlash(c, "/assistants/"+next.Owner, assistantSubscriptionChanged(changed), "")
}

// assistantSubscriptionChanged is the sentence a change is answered with, one
// wording for the flash and the toast; the line itself comes from the reactor.
func assistantSubscriptionChanged(changed string) string {
	if changed == "" {
		return "Nothing changed."
	}
	return "Changed: " + changed + "."
}

// assistantSubscriptionSummary is what a subscription is called in an answer:
// the event and where it listens.
func (s *Server) assistantSubscriptionSummary(sub assistant.Subscription) string {
	view := s.assistantSubscriptionView(sub, false)
	if view.Where == "" {
		return view.Label
	}
	return view.Label + ", " + view.Where
}

// assistantSubscriptionSpec reads a subscription's fields out of the form both
// surfaces post. A field the request does not carry is one nobody named, which
// on a create takes the default and on an edit leaves what stands, so the two
// ways in share one reading and one meaning. source is what the subscription
// reacts to when the request does not say (an edit never moves it), and stood
// are the terminals it already names: those keep the name they were stored
// with instead of being looked up again, a coder that stopped is still a
// terminal this subscription waits for.
func (s *Server) assistantSubscriptionSpec(c *gin.Context, source string, stood []string) (assistant.SubscriptionSpec, error) {
	spec := assistant.SubscriptionSpec{
		Event: c.PostForm("event"),
		Spec:  c.PostForm("spec"),
		Task:  c.PostForm("task"),
	}
	if kind, err := assistant.ParseEventOption(spec.Event); err == nil {
		source = kind.Source
	}
	// Several terminals fire it, and the mode says whether any of them does or
	// every one of them has to, which is a barrier. The page's select posts the
	// mode, `--all` posts it too.
	if raw, ok := c.GetPostForm("mode"); ok {
		spec.All, spec.AllSet = strings.EqualFold(strings.TrimSpace(raw), "all"), true
	}
	if raw, ok := c.GetPostFormArray("once"); ok {
		spec.OnceSet = true
		spec.Once = slices.ContainsFunc(raw, func(v string) bool { return strings.TrimSpace(v) != "" })
	}
	if raw, ok := c.GetPostFormArray("terminal"); ok {
		spec.TargetsSet = true
		for _, one := range raw {
			terminal := strings.TrimSpace(one)
			if terminal == "" {
				continue
			}
			target := assistant.SubscriptionTarget{Terminal: terminal}
			// A coder target is named here, where the running coders are; a job
			// target takes its name from the job, which the reactor reads.
			if source == assistant.EventCoder && !slices.Contains(stood, terminal) {
				running, err := s.assistantSteerTarget(terminal)
				if err != nil {
					return spec, err
				}
				target.Name = running.Name
			}
			spec.Targets = append(spec.Targets, target)
		}
	}
	if raw := strings.TrimSpace(c.PostForm("until")); raw != "" {
		if strings.EqualFold(raw, "never") {
			spec.Never = true
		} else {
			until, err := parseSpan(raw)
			if err != nil {
				return spec, fmt.Errorf("The expiry %q cannot be read: a span like 8h, 90m or 2d, or never.", raw)
			}
			spec.Until = until
		}
	}
	if raw := strings.TrimSpace(c.PostForm("max_per_hour")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return spec, errors.New("The cap per hour has to be a number.")
		}
		spec.MaxPerHour = n
	}
	if raw := strings.TrimSpace(c.PostForm("batch")); raw != "" {
		batch, err := parseSpan(raw)
		if err != nil {
			return spec, fmt.Errorf("The batch window %q cannot be read: a span like 30s or 2m, or 0.", raw)
		}
		spec.Batch, spec.BatchSet = batch, true
	}
	return spec, nil
}

// assistantUnsubscribe removes one subscription, from the page or from
// `subscription-delete`. The user removes any, an assistant only its own.
func (s *Server) assistantUnsubscribe(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))
	by := ""
	if s.localCall(c) {
		from, err := s.assistantCaller(c)
		if err != nil {
			s.assistantJobError(c, err)
			return
		}
		by = from
	}
	sub, _ := s.assistants.Events().Get(id)
	if err := s.assistants.Events().Unsubscribe(id, by); err != nil {
		s.assistantJobError(c, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"id": id, "removed": true})
		return
	}
	s.redirectWithFlash(c, "/assistants/"+sub.Owner, "The subscription is removed.", "")
}

// parseSpan reads a span the way a person writes one: what time.ParseDuration
// takes, plus days.
func parseSpan(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.ParseFloat(strings.TrimSuffix(raw, "d"), 64)
		if err != nil || days < 0 {
			return 0, errors.New("not a span")
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		// A bare number is seconds, which is what the page's batch field
		// posts.
		if n < 0 {
			return 0, errors.New("not a span")
		}
		return time.Duration(n) * time.Second, nil
	}
	span, err := time.ParseDuration(raw)
	if err != nil || span < 0 {
		return 0, errors.New("not a span")
	}
	return span, nil
}

// assistantSubscriptionsData is the model of the fragment: one assistant's
// subscriptions and what its form offers, or everybody's when no owner is
// named.
func (s *Server) assistantSubscriptionsData(c *gin.Context, owner string) render.AssistantSubscriptionsData {
	subs := s.assistants.Events().List()
	if owner != "" {
		subs = s.assistants.Events().ListOf(owner)
	}
	data := render.AssistantSubscriptionsData{
		Page:   render.Page{CSRFToken: s.csrfToken(c)},
		Owner:  owner,
		URL:    assistantSubscriptionsPath,
		Owners: owner == "",
	}
	for _, sub := range subs {
		view := s.assistantSubscriptionView(sub, owner == "")
		if view.Open {
			data.Open++
		}
		data.Subscriptions = append(data.Subscriptions, view)
	}
	for _, k := range assistant.EventOptions {
		data.Events = append(data.Events, render.AssistantEventOption{Name: k.Name(), Source: k.Source, Label: k.Label, Help: k.Help})
	}
	if owner != "" {
		for _, job := range s.watcher.OpenJobs(owner) {
			data.Jobs = append(data.Jobs, render.AssistantSubscriptionTarget{Terminal: job.Terminal, Name: job.Name, Project: job.Project})
		}
	}
	for _, m := range s.coders {
		for _, running := range m.Snapshot().Running {
			data.Coders = append(data.Coders, render.AssistantSubscriptionTarget{
				Terminal: running.Identifier,
				Name:     running.Name,
				Project:  s.projects.ProjectNameFor(running.CWD),
			})
		}
	}
	return data
}

// assistantSubscriptionView is one row: what fires it, where, and where it
// stands.
func (s *Server) assistantSubscriptionView(sub assistant.Subscription, owners bool) render.AssistantSubscriptionView {
	view := render.AssistantSubscriptionView{
		ID:       sub.ID,
		Short:    sub.ID,
		OwnerID:  sub.Owner,
		Source:   sub.Source,
		Kind:     sub.Kind,
		Label:    assistant.EventLabel(sub.Source, sub.Kind),
		Spec:     sub.Spec,
		Task:     sub.Task,
		Once:     sub.Once,
		State:    string(sub.State),
		Open:     sub.Open(),
		Fired:    sub.Fired,
		Next:     machineTime(sub.NextAt),
		Until:    machineTime(sub.ExpiresAt),
		Note:     sub.Note,
		Pending:  len(sub.Pending),
		Reacting: sub.Reacting(),
	}
	if len(view.Short) > 8 {
		view.Short = view.Short[:8]
	}
	view.Edit = subscriptionEditJSON(sub)
	if owners {
		view.Owner = s.assistantName(sub.Owner)
	}
	switch {
	case sub.Source == assistant.EventCron:
		view.Where = sub.Spec
	case len(sub.Targets) == 0 && sub.Source == assistant.EventJob:
		view.Where = "any job of mine"
	case len(sub.Targets) == 0:
		view.Where = "any coder"
	default:
		view.Where = subscriptionWhere(sub)
		if len(sub.Targets) == 1 {
			// One terminal is one place to go; with several the row leads
			// nowhere, there is no such thing as opening three coders.
			view.TargetURL = "/coders/" + sub.Targets[0].Terminal
		}
	}
	return view
}

// subscriptionWhere names the terminals a subscription waits for, and says
// which of them fires it: any one of them, or every one, which is the barrier.
// A target whose terminal was deleted says so, because a barrier counts it as
// arrived and the row would otherwise read as one still waiting for it.
func subscriptionWhere(sub assistant.Subscription) string {
	names := make([]string, 0, len(sub.Targets))
	for _, t := range sub.Targets {
		name := t.Name
		if name == "" {
			name = t.Terminal
		}
		if t.Gone {
			name += " (deleted)"
		}
		names = append(names, name)
	}
	joined := strings.Join(names, ", ")
	if len(names) < 2 {
		return joined
	}
	if sub.All {
		return "all of " + joined
	}
	return "any of " + joined
}

// subscriptionEditJSON is what a row hands its form when somebody changes it:
// the stand of every field the form offers, under the names the form posts, so
// the one form opens filled and nobody builds a second one. A subscription
// that is over answers nothing, it cannot be changed any more.
func subscriptionEditJSON(sub assistant.Subscription) string {
	if !sub.Open() {
		return ""
	}
	out, err := json.Marshal(struct {
		ID      string  `json:"id"`
		Event   string  `json:"event"`
		Source  string  `json:"source"`
		Task    string  `json:"task"`
		Targets []gin.H `json:"targets"`
		Mode    string  `json:"mode"`
		Spec    string  `json:"spec"`
		Once    bool    `json:"once"`
		Until   string  `json:"until"`
		Cap     int     `json:"maxPerHour"`
		Batch   int     `json:"batch"`
	}{
		ID:      sub.ID,
		Event:   assistant.EventOption{Source: sub.Source, Kind: sub.Kind}.Name(),
		Source:  sub.Source,
		Task:    sub.Task,
		Targets: subscriptionTargets(sub),
		Mode:    subscriptionMode(sub.All),
		Spec:    sub.Spec,
		Once:    sub.Once,
		Until:   machineTime(sub.ExpiresAt),
		Cap:     sub.MaxPerHour,
		Batch:   sub.BatchSeconds,
	})
	if err != nil {
		return ""
	}
	return string(out)
}

// subscriptionMode is the value the form's mode select carries for a barrier
// and for any of them.
func subscriptionMode(all bool) string {
	if all {
		return "all"
	}
	return "any"
}

// subscriptionTargets is what the JSON answer says about the terminals, one
// entry each, so `subscription-list` prints the same facts the row shows.
func subscriptionTargets(sub assistant.Subscription) []gin.H {
	out := make([]gin.H, 0, len(sub.Targets))
	for _, t := range sub.Targets {
		out = append(out, gin.H{"terminal": t.Terminal, "name": t.Name, "met": t.Met, "gone": t.Gone})
	}
	return out
}

// assistantNoteView describes a note: where it came from, the line it is
// read by, the first line of its body and whether more stands behind it.
func (s *Server) assistantNoteView(note *assistant.Note, content string) *render.AssistantNoteView {
	if note == nil {
		return nil
	}
	view := &render.AssistantNoteView{
		Source:   note.Source,
		Headline: note.Headline,
		Verdict:  note.Verdict,
		Terminal: note.Terminal,
		Name:     note.Name,
		Count:    note.Count,
		Task:     note.Task,
	}
	if note.Source == assistant.NoteCheck {
		view.Done = note.Verdict == string(assistant.VerdictDone)
		view.Blocked = note.Verdict == string(assistant.VerdictBlocked)
		view.Expired = note.Verdict == string(assistant.VerdictExpired)
	}
	if note.Terminal != "" {
		view.URL = "/coders/" + note.Terminal
		if view.Name == "" {
			view.Name = note.Terminal
		}
	}
	view.First, view.Rest = noteFirstLine(content)
	return view
}

// noteFirstLineRunes is how much of a note's body stands unfolded.
const noteFirstLineRunes = 160

// noteFirstLine is the first line of a body, cut for the folded note, and
// whether the body holds more than that.
func noteFirstLine(content string) (string, bool) {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	first := ""
	rest := false
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first == "" {
			first = line
			continue
		}
		if i > 0 {
			rest = true
			break
		}
	}
	runes := []rune(first)
	if len(runes) > noteFirstLineRunes {
		first = strings.TrimSpace(string(runes[:noteFirstLineRunes])) + "…"
		rest = true
	}
	return first, rest
}

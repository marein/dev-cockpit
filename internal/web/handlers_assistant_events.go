package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// assistantTriggersPath is where the triggers live: the list, the two actions
// on them, and the path `dev-cockpit assistant trigger-new` posts to. One path
// for every assistant's triggers, like the jobs; which assistant one belongs
// to travels in the request and in the answer.
const assistantTriggersPath = "/assistants/triggers"

// handleAssistantTriggers serves the triggers of one assistant, as the
// fragment the page's aside pulls (`?assistant=<id>`) and as the JSON the
// `trigger-list` command reads. A call over the socket that names no assistant
// lists the caller's own, `all` everybody's.
func (s *Server) handleAssistantTriggers(c *gin.Context) {
	// The form that makes a trigger and the one that changes it are this same
	// path asked for with `form`, the word the POST dispatches on: the create
	// dialog fetches it with modal=1 and gets the form alone, a plain browser
	// gets the page around it, and both post back here.
	if form := strings.TrimSpace(c.Query("form")); form != "" {
		s.assistantTriggerForm(c, form == "edit")
		return
	}
	// The zone alone, which is what `timezone-get` reads: a caller asking
	// where the user sits must not pay for the whole list to find out, and
	// the two sources stay apart here the way the list keeps them apart.
	if _, ok := c.GetQuery("timezone"); ok {
		zone, stored := AssistantTimezone(s.settings)
		c.JSON(http.StatusOK, gin.H{"timezone": zone, "stored": stored, "server": assistant.ServerZone()})
		return
	}
	owner := strings.TrimSpace(c.Query("assistant"))
	if owner == "" && s.localCall(c) {
		owner = s.callingAssistant(c)
	}
	if owner == "all" {
		owner = ""
	}
	if wantsJSON(c.Request) {
		s.assistantTriggersJSON(c, owner)
		return
	}
	c.HTML(http.StatusOK, "assistant_triggers_list.gohtml", s.assistantTriggersData(c, owner))
}

// assistantTriggersJSON is what `trigger-list` reads: the rows the aside shows,
// narrowed by a word before the cap and capped the way the aside caps them,
// with what each of the two left out counted. The whole picture is behind the
// two flags and nothing is dropped in silence.
func (s *Server) assistantTriggersJSON(c *gin.Context, owner string) {
	all, _ := strconv.ParseBool(c.DefaultQuery("all", "false"))
	since, err := querySince(c)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	list, dropped, older := narrowTriggers(s.triggersOf(owner), c.Query("contains"), since, all)
	rows := make([]gin.H, 0, len(list))
	for _, trigger := range list {
		view := s.assistantTriggerView(trigger, owner == "")
		rows = append(rows, gin.H{
			"id":           trigger.ID,
			"owner":        trigger.Owner,
			"ownerName":    s.assistantName(trigger.Owner),
			"name":         trigger.Name,
			"event":        assistant.EventOption{Source: trigger.Source, Kind: trigger.Kind}.Name(),
			"label":        view.Label,
			"where":        view.Where,
			"targets":      triggerTargets(trigger),
			"all":          trigger.All,
			"spec":         trigger.Spec,
			"timezone":     trigger.Timezone,
			"task":         trigger.Task,
			"model":        trigger.Model,
			"once":         trigger.Once,
			"state":        view.State,
			"open":         view.Open,
			"fired":        trigger.Fired,
			"lastFiredAt":  machineTime(trigger.LastFiredAt),
			"nextAt":       machineTime(trigger.NextAt),
			"expiresAt":    machineTime(trigger.ExpiresAt),
			"batchSeconds": trigger.BatchSeconds,
			"pending":      len(trigger.Pending),
			"reacting":     trigger.Reacting(),
			"note":         trigger.Note,
		})
	}
	zone, stored := AssistantTimezone(s.settings)
	c.JSON(http.StatusOK, gin.H{
		"triggers": rows, "owners": owner == "", "dropped": dropped, "older": older,
		// What a schedule made now would be read in, and whether anybody said
		// so: a reader that has to pick a zone needs to know the difference
		// between a stored answer and this host's own setting.
		"timezone": zone, "timezoneStored": stored,
	})
}

// spentTriggersShown bounds the spent tail of a trigger list, the number the
// closed jobs are capped at and for the same reason: a trigger that fired its
// one shot or ran past its expiry is history, it fires nothing again, and a
// host collects them for weeks while its task stays stored whole.
const spentTriggersShown = closedJobsShown

// triggersOf are the triggers a reading is about: one assistant's, or every
// assistant's where no owner is named. Both are sorted standing first and then
// newest, so everything past the cap is the oldest spent history.
func (s *Server) triggersOf(owner string) []assistant.Trigger {
	if owner == "" {
		return s.assistants.Events().List()
	}
	return s.assistants.Events().ListOf(owner)
}

// narrowTriggers is the one reading both surfaces take: the word and the
// moment narrow before the cap, so they reach triggers the capped list never
// shows, and the spent tail is capped unless all lifts it. It answers the
// rows, how many the two filters left out and how many the cap held back.
func narrowTriggers(list []assistant.Trigger, contains string, since time.Time, all bool) (rows []assistant.Trigger, dropped, older int) {
	contains = strings.ToLower(strings.TrimSpace(contains))
	spent := 0
	for _, trigger := range list {
		if contains != "" && !triggerCarries(trigger, contains) {
			dropped++
			continue
		}
		// What moved since then: a trigger's UpdatedAt is written by every
		// fire, every end and every edit, which is what "what happened
		// yesterday" is asking about.
		if !since.IsZero() && trigger.UpdatedAt.Before(since) {
			dropped++
			continue
		}
		if !trigger.Open() {
			spent++
			if !all && spent > spentTriggersShown {
				older++
				continue
			}
		}
		rows = append(rows, trigger)
	}
	return rows, dropped, older
}

// querySince is the moment a reading is narrowed to, zero where a caller named
// none. What a person writes, a span back from now or a date, is read where
// the flag is typed (`parseSince` in the cli package) and the resolved moment
// is what travels, so there is one parser; a stamp this cannot read is refused
// rather than read as no filter at all, or a narrowed call would silently
// answer with everything.
func querySince(c *gin.Context) (time.Time, error) {
	raw := strings.TrimSpace(c.Query("since"))
	if raw == "" {
		return time.Time{}, nil
	}
	moment, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("The moment %q cannot be read: a caller sends what it resolved, an RFC 3339 stamp.", raw)
	}
	return moment, nil
}

// triggerCarries answers whether a word stands in what a reading of this
// trigger shows: its name, the event and where it listens, its task and what
// its last fire left. Compared case insensitively, the way `job-list
// --contains` compares, and it searches nothing behind the surface: a hit
// nobody can see is no hit.
func triggerCarries(trigger assistant.Trigger, contains string) bool {
	for _, field := range []string{
		trigger.Name,
		assistant.EventLabel(trigger.Source, trigger.Kind),
		triggerWhere(trigger),
		trigger.Spec,
		trigger.Timezone,
		trigger.Task,
		trigger.Note,
	} {
		if strings.Contains(strings.ToLower(field), contains) {
			return true
		}
	}
	return false
}

// handleAssistantTriggersAction is the POST side of that path, dispatching on
// the hidden form field like every other form of the cockpit, serving the
// page's form and the assistant's own commands through the same handlers.
func (s *Server) handleAssistantTriggersAction(c *gin.Context) {
	switch strings.TrimSpace(c.PostForm("form")) {
	case "new":
		s.assistantTriggerNew(c)
	case "edit":
		s.assistantTriggerEdit(c)
	case "remove":
		s.assistantTriggerDelete(c)
	case "timezone":
		s.assistantTimezoneStore(c)
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}

// assistantTriggerNew makes one trigger. Who it belongs to is decided the way
// a job's owner is (steerOwner): a turn is itself, the page names one. Each
// coder target has to be a running coder, so the row can name it; a job target
// is checked by the reactor against the owner's jobs.
func (s *Server) assistantTriggerNew(c *gin.Context) {
	owner, err := s.steerOwner(c)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	spec, err := s.assistantTriggerSpec(c, "", nil)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	spec.Owner = owner
	// A schedule that named no zone takes the one that is stored, else the one
	// this server runs in. Only a schedule carries one, and a zone on any other
	// event is refused, so nothing is filled in for those.
	if kind, err := assistant.ParseEventOption(spec.Event); err == nil && kind.Source == assistant.EventCron && strings.TrimSpace(spec.Timezone) == "" {
		spec.Timezone = s.assistantZone()
	}
	trigger, err := s.assistants.Events().Add(spec)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	s.rememberAssistantZone(c, c.PostForm("timezone"))
	s.rememberTriggerModel(owner, trigger.Model)
	summary := s.assistantTriggerSummary(trigger)
	if s.formStayed(c, "Trigger added: "+summary+".") {
		return
	}
	if wantsJSON(c.Request) {
		// The model is the trigger's own where it has one, and modelDefault
		// what a reaction would run on without the flag, the assistant default
		// the form's empty entry names: the owner's Triggers pick at the ring
		// where one stands, else the chat as it resolves now, so `trigger-new`
		// names the model in its added line only where the flag made a
		// difference.
		without := ""
		if inst, err := s.assistants.Get(owner); err == nil {
			without = assistant.DefaultModelFor(assistant.RunReaction, inst.Summary, s.modelDefaults(inst.CoderID))
		}
		c.JSON(http.StatusOK, gin.H{
			"id": trigger.ID, "summary": summary, "state": string(trigger.State),
			"timezone": trigger.Timezone, "nextAt": machineTime(trigger.NextAt),
			"ignored": assistantTriggerIgnored(c, trigger), "model": trigger.Model, "modelDefault": without,
		})
		return
	}
	s.redirectWithFlash(c, "/assistants/"+owner, "Trigger added: "+summary+".", "")
}

// rememberTriggerModel puts a trigger's model into its owner's coder's
// repository, the way the ring's save does, so a name typed under Other… on
// the trigger form stands in every later list of that coder.
func (s *Server) rememberTriggerModel(owner, model string) {
	if inst, err := s.assistants.Get(owner); err == nil {
		s.rememberModel(inst.CoderID, model)
	}
}

// assistantTriggerEdit changes one, from the row's menu on the page or from
// `trigger-edit`. The user changes any, an assistant only its own, the rule
// the removal follows. The event is not read off the request here: the form's
// select is locked and posts nothing, and a caller that does name one is
// refused by the reactor unless it is the event the trigger already has.
func (s *Server) assistantTriggerEdit(c *gin.Context) {
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
	trigger, ok := s.assistants.Events().Get(id)
	if !ok {
		s.assistantJobError(c, errors.New("No trigger has that id."))
		return
	}
	spec, err := s.assistantTriggerSpec(c, trigger.Source, trigger.Terminals())
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	next, changed, err := s.assistants.Events().Edit(id, by, spec)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	s.rememberAssistantZone(c, c.PostForm("timezone"))
	s.rememberTriggerModel(next.Owner, next.Model)
	if s.formStayed(c, assistantTriggerChanged(changed)) {
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{
			"id": next.ID, "summary": s.assistantTriggerSummary(next), "changed": changed,
			"timezone": next.Timezone, "nextAt": machineTime(next.NextAt),
			"ignored": assistantTriggerIgnored(c, next),
		})
		return
	}
	s.redirectWithFlash(c, "/assistants/"+next.Owner, assistantTriggerChanged(changed), "")
}

// assistantTriggerChanged is the sentence a change is answered with, one
// wording for the flash and the toast; the line itself comes from the reactor.
func assistantTriggerChanged(changed string) string {
	if changed == "" {
		return "Nothing changed."
	}
	return "Changed: " + changed + "."
}

// assistantTriggerIgnored names a field the request carried that the trigger
// it made or changed has no use for, empty when it carried none. A schedule
// has no batch window, so one named on it is dropped rather than stored, and
// the answer says which field that was instead of leaving a caller to read the
// bound back. The page never produces one, its field is gone the moment a
// schedule is picked; a command names its flags without seeing the form.
func assistantTriggerIgnored(c *gin.Context, trigger assistant.Trigger) string {
	if trigger.Source == assistant.EventCron && strings.TrimSpace(c.PostForm("batch")) != "" {
		return "batch"
	}
	return ""
}

// assistantTriggerSummary is what a trigger is called in an answer: the same
// heading its row reads by, so a flash and a row never name it differently.
func (s *Server) assistantTriggerSummary(trigger assistant.Trigger) string {
	return s.assistantTriggerView(trigger, false).Heading
}

// assistantTriggerSpec reads a trigger's fields out of the form both surfaces
// post. A field the request does not carry is one nobody named, which on a
// create takes the default and on an edit leaves what stands, so the two ways
// in share one reading and one meaning. source is what the trigger reacts to
// when the request does not say (an edit never moves it), and stood are the
// terminals it already names: those keep the name they were stored with
// instead of being looked up again, a coder that stopped is still a terminal
// this trigger waits for.
func (s *Server) assistantTriggerSpec(c *gin.Context, source string, stood []string) (assistant.TriggerSpec, error) {
	spec := assistant.TriggerSpec{
		Event:    c.PostForm("event"),
		Spec:     c.PostForm("spec"),
		Timezone: c.PostForm("timezone"),
		Task:     c.PostForm("task"),
	}
	// The model is optional and an empty one is a value, the assistant
	// default: the page's select posts on every save and `--model default`
	// posts the empty field, while a request without it leaves what stands.
	if raw, ok := c.GetPostForm("model"); ok {
		spec.Model, spec.ModelSet = raw, true
	}
	// The name is optional and an empty one is a value: the page posts the
	// field on every save, so clearing it is saying so, while a request
	// without the field named nothing and leaves what stands.
	if raw, ok := c.GetPostForm("name"); ok {
		spec.Name, spec.NameSet = raw, true
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
			target := assistant.TriggerTarget{Terminal: terminal}
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
	// The expiry is a span either way in, and the two ways write it
	// differently: a command writes one word, `8h` or `never`, while the form
	// asks for a number with the unit beside it and says none in that same
	// select. So the unit is what the reading starts from, the number carries
	// it where there is one, and both ways end in one span and one parser.
	until := strings.TrimSpace(c.PostForm("until"))
	if unit := strings.TrimSpace(c.PostForm("untilUnit")); strings.EqualFold(unit, triggerNoExpiry) {
		// The number is disabled while that entry stands, so whatever it
		// carried says nothing: the select is the answer.
		until = triggerNoExpiry
	} else if until != "" {
		until += unit
	}
	if until != "" {
		if strings.EqualFold(until, triggerNoExpiry) {
			spec.Never = true
		} else {
			span, err := parseSpan(until)
			if err != nil {
				return spec, fmt.Errorf("The expiry %q cannot be read: a span like 8h, 90m or 2d, or never.", until)
			}
			spec.Until = span
		}
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

// assistantTriggerDelete removes one trigger, from the page or from
// `trigger-delete`. The user removes any, an assistant only its own.
func (s *Server) assistantTriggerDelete(c *gin.Context) {
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
	trigger, _ := s.assistants.Events().Get(id)
	if err := s.assistants.Events().Remove(id, by); err != nil {
		s.assistantJobError(c, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"id": id, "removed": true})
		return
	}
	s.redirectWithFlash(c, "/assistants/"+trigger.Owner, "The trigger is removed.", "")
}

// assistantTimezoneStore stores the zone new schedules start on. It is an
// action of its own and not a flag on a trigger, because the two say different
// things: a zone on one trigger is that trigger's, while this says where the
// user sits, and only they may say that. The form's select and the CLI's
// `timezone-set` are the two ways to it, and `--tz` is never one.
func (s *Server) assistantTimezoneStore(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("timezone"))
	if _, err := assistant.LoadZone(name); err != nil {
		s.assistantJobError(c, err)
		return
	}
	if s.settings != nil {
		s.settings.Set(assistantTimezoneKey, name)
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"timezone": name})
		return
	}
	s.redirectWithFlash(c, "/assistants", "New schedules are read in "+name+".", "")
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

// assistantTriggersData is the model of the fragment: one assistant's triggers
// and what its form offers, or everybody's when no owner is named.
func (s *Server) assistantTriggersData(c *gin.Context, owner string) render.AssistantTriggersData {
	triggers, _, older := narrowTriggers(s.triggersOf(owner), "", time.Time{}, false)
	data := render.AssistantTriggersData{
		Page:   render.Page{CSRFToken: s.csrfToken(c)},
		Owner:  owner,
		URL:    assistantTriggersPath,
		Owners: owner == "",
		Older:  older,
	}
	for _, trigger := range triggers {
		view := s.assistantTriggerView(trigger, owner == "")
		if view.Open {
			data.Open++
		}
		data.Triggers = append(data.Triggers, view)
	}
	return data
}

// assistantTriggerForm is the one form of both ways in, rendered on the stand
// it opens: empty for a new trigger, this job's terminal on job done for one
// begun on a steered coder's row, and everything a trigger holds for one that
// is changed. Nothing fills a form in the browser, so there is one reading of
// a stand and it is the server's.
func (s *Server) assistantTriggerForm(c *gin.Context, edit bool) {
	data := render.AssistantTriggerFormData{
		Modal:   inFormModal(c),
		Owner:   strings.TrimSpace(c.Query("assistant")),
		URL:     assistantTriggersPath,
		Event:   assistant.EventOptions[0].Name(),
		MaxName: assistant.MaxTriggerNameRunes,
		Mode:    triggerMode(false),
		Batch:   int(assistant.DefaultTriggerBatch / time.Second),
		// A new trigger expires never, which is what the select says over an
		// empty number; a unit is picked the moment somebody wants one.
		UntilUnit: triggerNoExpiry,
		// The zone a schedule made here would be read in: the stored default,
		// else this server's own. A change of it saves the new one as the
		// default, which is what makes this field the one way to move it in
		// the browser.
		Timezone: s.assistantZone(),
	}
	// A picked terminal the lists no longer offer is still one this trigger
	// waits for, so it rides along instead of being dropped by a save.
	picked := map[string]string{}
	if edit {
		trigger, ok := s.assistants.Events().Get(strings.TrimSpace(c.Query("id")))
		if !ok || !trigger.Open() {
			s.renderError(c, http.StatusNotFound, "Trigger not found", "That trigger is gone or has already been spent.")
			return
		}
		data.Edit, data.Owner = trigger.ID, trigger.Owner
		data.Event = assistant.EventOption{Source: trigger.Source, Kind: trigger.Kind}.Name()
		data.Name = trigger.Name
		data.Task, data.Spec, data.Once = trigger.Task, trigger.Spec, trigger.Once
		data.Model.Current = trigger.Model
		data.Timezone = trigger.Timezone
		data.Mode = triggerMode(trigger.All)
		data.Batch = trigger.BatchSeconds
		data.Until = machineTime(trigger.ExpiresAt)
		data.UntilCount, data.UntilUnit = triggerSpan(trigger.ExpiresAt, time.Now())
		for _, t := range trigger.Targets {
			picked[t.Terminal] = t.Name
		}
	}
	// A trigger begun on a steered coder's row: the thought is "when this one
	// is done, then", so the form opens on that job and the task is all that
	// is left to type.
	if job := strings.TrimSpace(c.Query("job")); job != "" && !edit {
		data.Event = assistant.EventOption{Source: assistant.EventJob, Kind: string(assistant.JobDone)}.Name()
		data.Once, picked[job] = true, ""
	}
	// The model select offers what the owner's coder offers, with the empty
	// entry naming the assistant default as it resolves now, the owner's
	// Triggers pick at the ring, else its chat, stored as nothing: a trigger
	// left on it runs on that default as it stands at fire time.
	repo := coder.ModelRepositoryFor(nil)
	resolved := ""
	if owner, err := s.assistants.Get(data.Owner); err == nil {
		repo = s.coderModelRepository(owner.CoderID)
		resolved = assistant.DefaultModelFor(assistant.RunReaction, owner.Summary, s.modelDefaults(owner.CoderID))
	}
	data.Model = modelPick("model", data.Model.Current, assistantDefaultLabel(resolved), repo)
	data.ModelNote = repo.Note()
	for _, k := range assistant.EventOptions {
		data.Events = append(data.Events, render.AssistantEventOption{Name: k.Name(), Source: k.Source, Label: k.Label, Help: k.Help})
	}
	for _, job := range s.watcher.OpenJobs(data.Owner) {
		data.Jobs = append(data.Jobs, render.AssistantTriggerTarget{Terminal: job.Terminal, Name: job.Name, Project: job.Project, Picked: inTargets(picked, job.Terminal)})
	}
	for _, m := range s.coders {
		for _, running := range m.Snapshot().Running {
			data.Coders = append(data.Coders, render.AssistantTriggerTarget{
				Terminal: running.Identifier,
				Name:     running.Name,
				Project:  s.projects.ProjectNameFor(running.CWD),
				Picked:   inTargets(picked, running.Identifier),
			})
		}
	}
	data.Jobs = withStale(data.Jobs, picked, assistant.EventJob, data.Event)
	data.Coders = withStale(data.Coders, picked, assistant.EventCoder, data.Event)
	data.Return = "/assistants/" + data.Owner
	if data.Modal {
		c.HTML(http.StatusOK, "assistant_trigger_form.gohtml", data)
		return
	}
	title := "New trigger"
	if edit {
		title = "Change trigger"
	}
	data.Page = s.page(c, title, "assistants")
	c.HTML(http.StatusOK, "assistant_trigger_new.gohtml", data)
}

// triggerNoExpiry is the entry the form's unit select carries for a trigger
// that expires never. It stands among the units because it answers the same
// question, how long, and it is the word `--until` takes for none, so one
// reading serves the form and the command.
const triggerNoExpiry = "never"

// triggerSpan is the expiry as the form's two fields stand on it: how long the
// trigger has left, and the unit that keeps that number whole. What is stored
// is a moment while the field asks for a span from now, so an edit that moves
// something else re-posts what is left and the expiry keeps meaning what the
// field says. The largest unit the number divides into wins, so 90 minutes
// reads as 90 minutes and never as 1.5 hours, and a trigger that expires
// never, or whose moment has passed, answers zero with the select on No
// expiry, which is the empty number the element greys out.
func triggerSpan(until, now time.Time) (int, string) {
	left := until.Sub(now)
	if until.IsZero() || left <= 0 {
		return 0, triggerNoExpiry
	}
	minutes := int((left + 30*time.Second) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	if minutes%(24*60) == 0 {
		return minutes / (24 * 60), "d"
	}
	if minutes%60 == 0 {
		return minutes / 60, "h"
	}
	return minutes, "m"
}

func inTargets(picked map[string]string, terminal string) bool {
	_, ok := picked[terminal]
	return ok
}

// withStale appends the picked terminals the offered list does not hold, so a
// coder that stopped or a job that closed stays picked and a save keeps it.
// Only the select the event actually posts gets them.
func withStale(offered []render.AssistantTriggerTarget, picked map[string]string, source, event string) []render.AssistantTriggerTarget {
	if kind, err := assistant.ParseEventOption(event); err != nil || kind.Source != source {
		return offered
	}
	for terminal, name := range picked {
		if slices.ContainsFunc(offered, func(t render.AssistantTriggerTarget) bool { return t.Terminal == terminal }) {
			continue
		}
		if name == "" {
			name = terminal
		}
		offered = append(offered, render.AssistantTriggerTarget{Terminal: terminal, Name: name, Picked: true})
	}
	return offered
}

// assistantTriggerView is one row: what fires it, where, and where it stands.
func (s *Server) assistantTriggerView(trigger assistant.Trigger, owners bool) render.AssistantTriggerView {
	view := render.AssistantTriggerView{
		ID:       trigger.ID,
		Short:    trigger.ID,
		OwnerID:  trigger.Owner,
		Name:     trigger.Name,
		Source:   trigger.Source,
		Kind:     trigger.Kind,
		Label:    assistant.EventLabel(trigger.Source, trigger.Kind),
		Spec:     trigger.Spec,
		Timezone: trigger.Timezone,
		Task:     trigger.Task,
		Model:    trigger.Model,
		Once:     trigger.Once,
		State:    string(trigger.State),
		Open:     trigger.Open(),
		Fired:    trigger.Fired,
		Next:     triggerNextText(trigger),
		Until:    machineTime(trigger.ExpiresAt),
		Note:     trigger.Note,
		Pending:  len(trigger.Pending),
		Reacting: trigger.Reacting(),
		Broke:    trigger.Broke,
	}
	if len(view.Short) > 8 {
		view.Short = view.Short[:8]
	}
	if view.Open {
		view.EditURL = assistantTriggersPath + "?form=edit&id=" + url.QueryEscape(trigger.ID)
	}
	if owners {
		view.Owner = s.assistantName(trigger.Owner)
	}
	switch {
	case trigger.Source == assistant.EventCron:
		// The zone stands on the line with the cron fields, always, and not
		// only where it differs from the reader's own: a schedule is a
		// statement about a wall clock, the zone is half of that statement,
		// and a half that is sometimes there is read wrong when it is not.
		view.Where = strings.TrimSpace(trigger.Spec + " " + trigger.Timezone)
	case len(trigger.Targets) == 0 && trigger.Source == assistant.EventJob:
		view.Where = "any job of mine"
	case len(trigger.Targets) == 0:
		view.Where = "any coder"
	default:
		view.Where = triggerWhere(trigger)
		if len(trigger.Targets) == 1 {
			// One terminal is one place to go; with several the row leads
			// nowhere, there is no such thing as opening three coders.
			view.TargetURL = "/coders/" + trigger.Targets[0].Terminal
		}
	}
	// A name is the heading and pushes the event down a line; without one the
	// event is the heading, which is what a trigger always read by.
	switch {
	case view.Name != "":
		view.Heading = view.Name
	case view.Where != "":
		view.Heading = view.Label + ", " + view.Where
	default:
		view.Heading = view.Label
	}
	return view
}

// triggerNextText is the next tick of a schedule as the wall clock of its own
// zone reads it, with the zone named. It is rendered here and not handed to
// dc-time like every other moment in this app, because dc-time answers in the
// browser's zone: a schedule that says nine o'clock in Berlin would read as
// eight on a laptop in London, which is the very confusion the stored zone
// exists to end. Empty for every trigger but a schedule, which is the only
// kind that has a next tick at all.
func triggerNextText(trigger assistant.Trigger) string {
	if trigger.NextAt.IsZero() {
		return ""
	}
	return trigger.NextAt.In(trigger.Zone()).Format("2006-01-02 15:04") + " " + trigger.Timezone
}

// triggerWhere names the terminals a trigger waits for, and says which of them
// fires it: any one of them, or every one, which is the barrier. A target
// whose terminal was deleted says so, because a barrier counts it as arrived
// and the row would otherwise read as one still waiting for it.
func triggerWhere(trigger assistant.Trigger) string {
	names := make([]string, 0, len(trigger.Targets))
	for _, t := range trigger.Targets {
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
	if trigger.All {
		return "all of " + joined
	}
	return "any of " + joined
}

// triggerMode is the value the form's mode select carries for a barrier and
// for any of them.
func triggerMode(all bool) string {
	if all {
		return "all"
	}
	return "any"
}

// triggerTargets is what the JSON answer says about the terminals, one entry
// each, so `trigger-list` prints the same facts the row shows.
func triggerTargets(trigger assistant.Trigger) []gin.H {
	out := make([]gin.H, 0, len(trigger.Targets))
	for _, t := range trigger.Targets {
		out = append(out, gin.H{"terminal": t.Terminal, "name": t.Name, "met": t.Met, "gone": t.Gone})
	}
	return out
}

// assistantNoteView describes a note: where it came from, the line it is read
// by, the preview of the message it stands over and whether anything is folded
// behind that preview.
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
	view.Preview, view.Rest = markdown.Excerpt(content, assistant.PreviewRunes)
	return view
}

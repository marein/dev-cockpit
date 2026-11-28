package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// inboxEvent is the JSON shape of one hook, what a Claude Code hook receives
// on stdin.
type inboxEvent struct {
	SessionID        string `json:"session_id"` // claude hook field name
	HookEventName    string `json:"hook_event_name"`
	Message          string `json:"message"`
	AgentID          string `json:"agent_id"`
	NotificationType string `json:"notification_type"`
	ToolName         string `json:"tool_name"`
	Source           string `json:"source"`
	Trigger          string `json:"trigger"`
}

// inboxFile is one dropped event file. A hook the cockpit wraps names the
// terminal it belongs to and carries the hook verbatim as Event; any other
// file is the hook itself, the shape the opencode plugin writes and claude
// sessions started before the wrapping keep writing until they resume.
type inboxFile struct {
	Terminal string          `json:"terminal"`
	Event    json.RawMessage `json:"event"`
}

// HookEvent is one ingested hook as the raw fact: the coder whose inbox it
// came from, the coder's own session id it reported under, and the fields a
// coder reads to tell what it means. At is the file's modification time, not
// the moment the poller found it: every writer sets it at write time, the
// rename to .json keeps it, and it is the host clock the record's messages
// carry, so the two can be ordered. The file name says nothing about it:
// claude's hook puts a nanosecond clock there, opencode's plugin a
// millisecond one.
type HookEvent struct {
	Coder            string
	Session          string
	Name             string
	AgentID          string
	NotificationType string
	ToolName         string
	Source           string
	Trigger          string
	At               time.Time
}

// RunInbox polls one coder's inbox directory and ingests every completed
// event file under that coder's name. Writers put a .tmp name first and
// rename to .json, so a .json file is always complete. The first drain runs
// at once: the files written while no server was up are the newest word on
// whatever the last process kept. Blocks; run it in a goroutine.
func (s *Service) RunInbox(dir, coderID string, interval time.Duration) {
	_ = os.MkdirAll(dir, 0o755)
	s.DrainInbox(dir, coderID)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.DrainInbox(dir, coderID)
	}
}

// DrainInbox ingests the event files that lie in one coder's inbox, one pass
// of RunInbox.
//
// A file is let go in two steps, because its two kinds of listeners want
// opposite things of a process that dies in the middle. The event listener
// hears a signal while its file still lies there, and the file goes once that
// listener returned: what it took it holds on disk by then, so a process that
// dies in between finds the file again at its next start and hands the signal
// again under the same name, which is what lets that listener take it once.
// Everybody else hears it after the file is gone: the notification for the
// person, the signal and the hook listener cannot tell a signal handed again
// from a second one, so they hear each file once or, where the process dies
// right there, not at all, and never twice.
func (s *Service) DrainInbox(dir, coderID string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		targetID, ev, news := readInboxEvent(data, coderID, info.ModTime())
		if news && s.event != nil {
			s.event(targetID, ev.Name, coderID+"/"+strings.TrimSuffix(entry.Name(), ".json"))
		}
		_ = os.Remove(path)
		if targetID == "" {
			continue
		}
		if s.hook != nil {
			s.hook(targetID, ev)
		}
		if news {
			s.Signal(targetID)
		}
	}
}

// readInboxEvent reads one dropped file: the target it is about, empty for a
// file that names none or cannot be read, the hook as the raw fact, and
// whether it is news, a turn that ended or a coder that wants attention.
func readInboxEvent(data []byte, coderID string, at time.Time) (targetID string, hook HookEvent, news bool) {
	var file inboxFile
	if err := json.Unmarshal(data, &file); err != nil {
		return "", HookEvent{}, false
	}
	targetID = strings.TrimSpace(file.Terminal)
	if targetID != "" && len(file.Event) > 0 {
		data = file.Event
	}
	var ev inboxEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return "", HookEvent{}, false
	}
	session := strings.TrimSpace(ev.SessionID)
	if targetID == "" {
		targetID = session
	}
	if targetID == "" {
		return "", HookEvent{}, false
	}
	hook = HookEvent{
		Coder: coderID, Session: session, Name: ev.HookEventName,
		AgentID: strings.TrimSpace(ev.AgentID), NotificationType: ev.NotificationType,
		ToolName: ev.ToolName, Source: ev.Source, Trigger: ev.Trigger, At: at,
	}
	switch ev.HookEventName {
	case "Stop":
		news = true
	case "Notification":
		// Claude's Notification hook also fires after 60s of plain input
		// idling, which would re-raise a target the user already saw finish;
		// only real attention requests (permissions and similar) are worth
		// news.
		news = !strings.Contains(ev.Message, "waiting for your input")
	}
	return targetID, hook, news
}

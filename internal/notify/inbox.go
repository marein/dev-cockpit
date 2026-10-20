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
	s.drainInbox(dir, coderID)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.drainInbox(dir, coderID)
	}
}

func (s *Service) drainInbox(dir, coderID string) {
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
		_ = os.Remove(path)
		s.ingestInboxEvent(data, coderID, info.ModTime())
	}
}

func (s *Service) ingestInboxEvent(data []byte, coderID string, at time.Time) {
	var file inboxFile
	if err := json.Unmarshal(data, &file); err != nil {
		return
	}
	targetID := strings.TrimSpace(file.Terminal)
	if targetID != "" && len(file.Event) > 0 {
		data = file.Event
	}
	var ev inboxEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return
	}
	session := strings.TrimSpace(ev.SessionID)
	if targetID == "" {
		targetID = session
	}
	if targetID == "" {
		return
	}
	if s.hook != nil {
		s.hook(targetID, HookEvent{
			Coder: coderID, Session: session, Name: ev.HookEventName,
			AgentID: strings.TrimSpace(ev.AgentID), NotificationType: ev.NotificationType,
			ToolName: ev.ToolName, Source: ev.Source, Trigger: ev.Trigger, At: at,
		})
	}
	switch ev.HookEventName {
	case "Stop":
		s.Event(targetID, ev.HookEventName)
	case "Notification":
		// Claude's Notification hook also fires after 60s of plain input
		// idling, which would re-raise a target the user already saw finish;
		// only real attention requests (permissions and similar) are worth
		// news.
		if strings.Contains(ev.Message, "waiting for your input") {
			return
		}
		s.Event(targetID, ev.HookEventName)
	}
}

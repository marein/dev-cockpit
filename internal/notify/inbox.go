package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// inboxEvent is the JSON shape of one dropped event file. It matches what a
// Claude Code hook receives on stdin and writes into the inbox verbatim.
type inboxEvent struct {
	SessionID     string `json:"session_id"` // claude hook field name
	HookEventName string `json:"hook_event_name"`
	Message       string `json:"message"`
}

// RunInbox polls the provider's inbox directory and ingests every completed
// event file. Writers put a .tmp name first and rename to .json, so a .json
// file is always complete. Blocks; run it in a goroutine.
func (s *Service) RunInbox(dir string, interval time.Duration) {
	_ = os.MkdirAll(dir, 0o755)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.drainInbox(dir)
	}
}

func (s *Service) drainInbox(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		_ = os.Remove(path)
		s.ingestInboxEvent(data)
	}
}

func (s *Service) ingestInboxEvent(data []byte) {
	var ev inboxEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return
	}
	targetID := strings.TrimSpace(ev.SessionID)
	if targetID == "" {
		return
	}
	switch ev.HookEventName {
	case "Stop":
		s.Add(targetID)
	case "Notification":
		// Claude's Notification hook also fires after 60s of plain input
		// idling, which would re-raise a target the user already saw finish;
		// only real attention requests (permissions and similar) are worth
		// news.
		if strings.Contains(ev.Message, "waiting for your input") {
			return
		}
		s.Add(targetID)
	}
}

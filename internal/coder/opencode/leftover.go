package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

// A compaction opencode did not finish stays in the session (verified on
// 1.19.0): a summarize that is stopped, refused or aborted leaves its user
// message with the compaction part, and one cut off leaves an unfinished
// summary under it. The next `opencode run` finishes that compaction first
// and ends with the summary as its answer, the user's own message stays
// unanswered. So whatever a compaction left is cleared before the next turn
// and before the next compaction, through the server's own delete: opencode
// owns every write of its store.

// leftoverPatience bounds the clearing of one session.
const leftoverPatience = time.Minute

// leftoverQuery lists, newest first, a summary that did not finish cleanly and
// every compaction no finished summary answers, of one session.
const leftoverQuery = `SELECT m.id AS leftover FROM message m WHERE m.session_id = '%s' AND (` +
	`(json_extract(m.data,'$.role') = 'assistant' AND json_extract(m.data,'$.summary') = 1` +
	` AND (json_extract(m.data,'$.finish') IS NULL OR json_extract(m.data,'$.error') IS NOT NULL))` +
	` OR (json_extract(m.data,'$.role') = 'user'` +
	` AND EXISTS (SELECT 1 FROM part p WHERE p.message_id = m.id AND json_extract(p.data,'$.type') = 'compaction')` +
	` AND NOT EXISTS (SELECT 1 FROM message s WHERE s.session_id = m.session_id AND json_extract(s.data,'$.parentID') = m.id` +
	` AND json_extract(s.data,'$.summary') = 1 AND json_extract(s.data,'$.finish') IS NOT NULL` +
	` AND json_extract(s.data,'$.error') IS NULL)))` +
	` ORDER BY m.time_created DESC, m.id DESC`

func leftoverMessages(query func(sql string) ([]byte, error), native string) ([]string, error) {
	out, err := query(fmt.Sprintf(leftoverQuery, native))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Leftover string `json:"leftover"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		id, err := validSessionID(row.Leftover)
		if err != nil {
			return nil, errors.New("opencode's store names a message the cockpit cannot address")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// clearLeftovers deletes what unfinished compactions left in the session. The
// session is aborted first: a summarize this server still runs keeps it busy,
// and its summary lands only once it is aborted.
func clearLeftovers(ctx context.Context, s *throwawayServe, query func(sql string) ([]byte, error), native string) error {
	ids, err := leftoverMessages(query, native)
	if err != nil || len(ids) == 0 {
		return err
	}
	if status, _, err := s.call(ctx, http.MethodPost, "/session/"+native+"/abort", nil); err != nil {
		return err
	} else if status != http.StatusOK {
		return fmt.Errorf("the abort answered %d", status)
	}
	if ids, err = leftoverMessages(query, native); err != nil {
		return err
	}
	for _, id := range ids {
		status, answer, err := s.call(ctx, http.MethodDelete, "/session/"+native+"/message/"+id, nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK && status != http.StatusNotFound {
			return errors.New(summarizeRefusal(status, answer))
		}
	}
	return nil
}

// clearLeftovers is the clearing ahead of a turn. A store that cannot be read
// leaves the turn as it was, a server is only started when there is
// something to clear.
func (r *runner) clearLeftovers(workdir, native string) error {
	if _, ok := r.sessions.dbStamp(); !ok {
		return nil
	}
	ids, err := leftoverMessages(r.sessions.query, native)
	if err != nil {
		log.Printf("opencode assistant: unfinished compactions of %s could not be read: %v", native, err)
		return nil
	}
	if len(ids) == 0 {
		return nil
	}
	s, err := r.serve(workdir)
	if err != nil {
		return err
	}
	defer s.stop()
	ctx, cancel := context.WithTimeout(context.Background(), leftoverPatience)
	defer cancel()
	return clearLeftovers(ctx, s, r.sessions.query, native)
}

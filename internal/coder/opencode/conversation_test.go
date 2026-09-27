package opencode

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

func TestAMessageKeepsItsTextAndToolPartsInRowOrder(t *testing.T) {
	rows := []conversationRow{
		{Message: "m1", Role: "user", PartType: "text", Text: "write the README", Created: 1700000000000},
		{Message: "m2", Role: "assistant", PartType: "tool", Tool: "write", Created: 1700000001000},
		{Message: "m2", Role: "assistant", PartType: "text", Text: "The README is written.", Created: 1700000001000},
		{Message: "m2", Role: "assistant", PartType: "text", Text: "injected", Synthetic: 1, Created: 1700000001000},
		{Message: "m3", Role: "assistant", PartType: "tool", Tool: "bash", Created: 1700000002000},
	}
	all := renderConversation(rows)
	if len(all) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(all), all)
	}
	user, turn := all[0], all[1]
	if user.Role != coder.RoleUser || user.ID != "m1" || user.Text() != "write the README" || user.Time.UnixMilli() != 1700000000000 {
		t.Fatalf("user message: %+v", user)
	}
	if turn.ID != "m2" || len(turn.Parts) != 3 || turn.Parts[0].Tool != "write" || turn.Parts[1].Text != "The README is written." || turn.Parts[2].Tool != "bash" {
		t.Fatalf("the synthetic part stays out and the parts keep their order: %+v", turn.Parts)
	}
}

// opencode stores one assistant message per step, and a turn with a tool
// call takes two steps: the call, then the answer. The turn is one bubble
// under the first step's id, until the user speaks again. The tool line says
// what the call was about, read from the projected arguments.
func TestTheStepsOfOneTurnAreOneBubbleWithToolLines(t *testing.T) {
	rows := []conversationRow{
		{Message: "msg_u1", Role: "user", PartType: "text", Text: "list the files", Created: 1700000000000},
		{Message: "msg_s1", Role: "assistant", PartType: "step-start", Created: 1700000001000},
		{Message: "msg_s1", Role: "assistant", PartType: "tool", Tool: "bash", Input: `{"command":"ls /tmp","filePath":null}`, Created: 1700000001000},
		{Message: "msg_s1", Role: "assistant", PartType: "step-finish", Created: 1700000001000},
		{Message: "msg_s2", Role: "assistant", PartType: "tool", Tool: "read", Input: `{"filePath":"/tmp/x/AGENTS.md"}`, Created: 1700000002000},
		{Message: "msg_s3", Role: "assistant", PartType: "tool", Tool: "apply_patch", Input: `{"patchText":"*** Begin Patch\n*** Add File: /tmp/new.txt\n+hi"}`, Created: 1700000003000},
		{Message: "msg_s4", Role: "assistant", PartType: "text", Text: "Two files.", Created: 1700000004000},
		{Message: "msg_u2", Role: "user", PartType: "text", Text: "thanks", Created: 1700000005000},
		{Message: "msg_s5", Role: "assistant", PartType: "text", Text: "You are welcome.", Created: 1700000006000},
	}
	all := renderConversation(rows)
	if len(all) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(all), all)
	}
	turn := all[1]
	if turn.ID != "msg_s1" || turn.Role != coder.RoleCoder || turn.Time.UnixMilli() != 1700000001000 || len(turn.Parts) != 4 {
		t.Fatalf("the turn: %+v", turn)
	}
	if bash := turn.Parts[0]; bash.Tool != "bash" || bash.Text != "ls /tmp" {
		t.Fatalf("the bash call: %+v", bash)
	}
	if read := turn.Parts[1]; read.Text != "/tmp/x/AGENTS.md" {
		t.Fatalf("the read: %+v", read)
	}
	if patch := turn.Parts[2]; patch.Text != "/tmp/new.txt" {
		t.Fatalf("the patch: %+v", patch)
	}
	if all[2].ID != "msg_u2" || all[3].ID != "msg_s5" {
		t.Fatalf("the user's words start the next turn: %+v", all[2:])
	}
}

// The query runs against a database of opencode's shape, through the same
// driver the reader uses: a tool part answers the line its arguments say, the
// todo list as its length, and no argument travels past projectedRunes.
func TestTheQueryAnswersToolLinesFromARealDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), dbFileName)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 3*projectedRunes)
	statements := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, time_created INTEGER, data TEXT)`,
		`INSERT INTO session VALUES ('ses_x')`,
		`INSERT INTO message VALUES
			('msg_u1','ses_x',1700000000000,'{"role":"user"}'),
			('msg_a1','ses_x',1700000001000,'{"role":"assistant"}')`,
		`INSERT INTO part VALUES
			('prt_1','msg_u1',1,'{"type":"text","text":"plan and list"}'),
			('prt_2','msg_a1',2,'{"type":"tool","tool":"todowrite","state":{"input":{"todos":[{"content":"a","status":"pending"},{"content":"b","status":"pending"}]}}}'),
			('prt_3','msg_a1',3,'{"type":"tool","tool":"bash","state":{"input":{"command":"ls /tmp ` + long + `","description":"List"},"output":"SECRETOUTPUT"}}'),
			('prt_4','msg_a1',4,'{"type":"text","text":"Done."}')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	out, err := databaseQuery(path)(fmt.Sprintf(conversationQuery, "ses_x"))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if strings.Contains(string(out), "SECRETOUTPUT") {
		t.Fatalf("a tool's output travelled: %s", out)
	}
	var rows []conversationRow
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("rows do not decode: %v\n%s", err, out)
	}
	for _, row := range rows {
		var input map[string]any
		if row.Input != "" && json.Unmarshal([]byte(row.Input), &input) != nil {
			t.Fatalf("the projected input is no JSON: %s", row.Input)
		}
		if command, _ := input["command"].(string); len([]rune(command)) > projectedRunes {
			t.Fatalf("an argument travelled past projectedRunes: %d runes", len([]rune(command)))
		}
	}
	all := renderConversation(rows)
	if len(all) != 2 || all[0].Text() != "plan and list" {
		t.Fatalf("messages: %+v", all)
	}
	parts := all[1].Parts
	if len(parts) != 3 || parts[0].Tool != "todowrite" || parts[0].Text != "2 items" {
		t.Fatalf("the todo line: %+v", parts)
	}
	if parts[1].Tool != "bash" || !strings.HasPrefix(parts[1].Text, "ls /tmp x") || parts[2].Text != "Done." {
		t.Fatalf("the bash line and the answer: %+v", parts)
	}
}

// A record not written yet reads as fs.ErrNotExist, a query that fails does
// not: the copy view shows the first as an empty conversation and the second
// as what went wrong.
func TestAMissingRecordIsNotExistAndAFailedQueryIsNot(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	nodb := &sessionRepository{dataRoot: t.TempDir()}
	if _, err := nodb.conversation(id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no database: err = %v, want fs.ErrNotExist", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, dbFileName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	norows := &sessionRepository{dataRoot: root, loaded: true, query: func(string) ([]byte, error) { return []byte("[]"), nil }}
	if _, err := norows.conversation(id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no rows: err = %v, want fs.ErrNotExist", err)
	}
	failed := &sessionRepository{dataRoot: root, loaded: true, query: func(string) ([]byte, error) { return nil, errors.New("database is locked") }}
	if _, err := failed.conversation(id); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a failed query: err = %v, want an error that is not fs.ErrNotExist", err)
	}
}

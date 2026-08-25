package opencode

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// probeDB builds a database the shape the queries expect, through the same
// driver, so the tests hold against what the reader really decodes.
func probeDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), dbFileName)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, title TEXT, directory TEXT,
			time_updated INTEGER, metadata TEXT, parent_id TEXT, time_archived INTEGER)`,
		`INSERT INTO session VALUES
			('ses_aaa','one','/work/a',1787674753933,NULL,NULL,NULL),
			('ses_bbb','chat','/work/b',1787675851208,'{"devCockpitSessionID":"11111111-2222-4333-8444-555555555555"}',NULL,NULL),
			('ses_kid','child','/work/a',1,NULL,'ses_aaa',NULL)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// The reader answers the CLI's JSON shape: row objects keyed by column name,
// TEXT as strings (never the driver's raw bytes), INTEGER as numbers and
// NULL as null, so the row decoders read it unchanged.
func TestDatabaseQueryAnswersTheColumnShape(t *testing.T) {
	query := databaseQuery(probeDB(t))
	out, err := query(listQuery)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	var rows []sessionRow
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("answer does not decode: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want the two root sessions", rows)
	}
	if rows[0].ID != "ses_bbb" || rows[0].Cockpit != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("newest row = %+v, want the mapped conversation first", rows[0])
	}
	if rows[1].ID != "ses_aaa" || rows[1].Cockpit != "" || rows[1].TimeUpdated != 1787674753933 {
		t.Fatalf("row = %+v", rows[1])
	}
	if strings.Contains(string(out), "ses_kid") {
		t.Fatal("a child session is nobody's root row")
	}
}

// The path is a read and nothing else: the connection pins read only, so a
// write is refused and can never touch opencode's store.
func TestDatabaseQueryCannotWrite(t *testing.T) {
	query := databaseQuery(probeDB(t))
	if _, err := query(`DELETE FROM session`); err == nil {
		t.Fatal("a write has to be refused on the read-only path")
	}
	out, err := query(`SELECT count(*) AS n FROM session`)
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if !strings.Contains(string(out), `"n":3`) {
		t.Fatalf("rows went missing: %s", out)
	}
}

// A missing database is an error, never a freshly created file: the caller's
// gate treats it as an empty store, and nothing on this path may leave a
// database behind that opencode did not make.
func TestDatabaseQueryRefusesAMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	query := databaseQuery(filepath.Join(dir, dbFileName))
	if _, err := query(listQuery); err == nil {
		t.Fatal("a missing database has to answer an error")
	}
	if _, err := filepath.Glob(filepath.Join(dir, "*")); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(matches) != 0 {
		t.Fatalf("the read path left files behind: %v", matches)
	}
}

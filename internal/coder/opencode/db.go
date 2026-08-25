package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

// databaseQuery answers a read-only SQL query against opencode's database,
// in the shape `opencode db --format json` answered: one JSON array of row
// objects keyed by column name. The direct read replaced the CLI here
// because every CLI call boots opencode's JavaScript runtime first, ~550ms
// for every look at the store, which the surfaces that list sessions on
// every render cannot carry; the direct read answers in under a millisecond
// (measured against the live database). It is a read and nothing else: the
// connection opens the file read only and pins query_only, so nothing on
// this path can write, and a missing database is an error instead of the
// freshly created file `opencode db` would leave behind. SQLite's WAL mode
// is built for readers beside a writer, so opencode keeps owning every
// write (the deletes stay with its CLI); the busy timeout covers the
// moments a checkpoint holds the lock. The connection opens per call on
// purpose: opening costs microseconds, and a handle held across calls would
// have to be watched for a database moved or replaced underneath it.
func databaseQuery(path string) func(sqlText string) ([]byte, error) {
	dsn := "file:" + path + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"
	return func(sqlText string) ([]byte, error) {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, fmt.Errorf("opencode database: %w", err)
		}
		defer db.Close()
		rows, err := db.Query(sqlText)
		if err != nil {
			return nil, fmt.Errorf("opencode database: %w", err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			scan := make([]any, len(columns))
			for i := range values {
				scan[i] = &values[i]
			}
			if err := rows.Scan(scan...); err != nil {
				return nil, err
			}
			row := make(map[string]any, len(columns))
			for i, column := range columns {
				// The driver answers TEXT as bytes; kept raw it would land
				// base64 in the JSON and never match what a row decodes to.
				if b, ok := values[i].([]byte); ok {
					row[column] = string(b)
					continue
				}
				row[column] = values[i]
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
}

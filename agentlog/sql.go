package agentlog

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DB is an instrumented wrapper around *sql.DB. It records a db_query
// auto-event for every Query/Exec (and their *Context variants). The embedded
// *sql.DB keeps all other methods (Ping, Begin, Stats, ...) promoted unchanged.
type DB struct {
	*sql.DB
}

// WrapDB wraps a *sql.DB so that queries and executions are recorded as
// db_query auto-events. Use the returned *DB in place of the original.
func WrapDB(db *sql.DB) *DB {
	return &DB{DB: db}
}

func (d *DB) Query(query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	rows, err := d.DB.Query(query, args...)
	d.record("query", query, time.Since(start), err, 0)
	return rows, err
}

func (d *DB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	rows, err := d.DB.QueryContext(ctx, query, args...)
	d.record("query", query, time.Since(start), err, 0)
	return rows, err
}

func (d *DB) QueryRow(query string, args ...interface{}) *sql.Row {
	start := time.Now()
	row := d.DB.QueryRow(query, args...)
	d.record("query", query, time.Since(start), nil, 0)
	return row
}

func (d *DB) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	start := time.Now()
	row := d.DB.QueryRowContext(ctx, query, args...)
	d.record("query", query, time.Since(start), nil, 0)
	return row
}

func (d *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	res, err := d.DB.Exec(query, args...)
	affected := rowsAffectedOf(res)
	d.record("exec", query, time.Since(start), err, affected)
	return res, err
}

func (d *DB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	res, err := d.DB.ExecContext(ctx, query, args...)
	affected := rowsAffectedOf(res)
	d.record("exec", query, time.Since(start), err, affected)
	return res, err
}

func rowsAffectedOf(res sql.Result) int64 {
	if res == nil {
		return 0
	}
	if n, err := res.RowsAffected(); err == nil {
		return n
	}
	return 0
}

func (d *DB) record(operation, query string, dur time.Duration, err error, rowsAffected int64) {
	RecordDBQuery(operation, query, dur.Milliseconds(), rowsAffected, err)
}

// RecordDBQuery is a convenience helper for databases/drivers not covered by
// WrapDB. Call it after running a query to emit a db_query auto-event without
// wrapping *sql.DB.
func RecordDBQuery(operation, query string, durationMs int64, rowsAffected int64, err error) {
	payload := map[string]interface{}{
		"operation":     operation,
		"query":         query,
		"duration_ms":   durationMs,
		"rows_affected": rowsAffected,
		"error":         errString(err),
	}
	AgentLog.RecordAuto(string(EventTypeDBQuery),
		fmt.Sprintf("%s: %s", operation, query),
		WithPayload(payload))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

package agentlog

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"io"
	"regexp"
	"testing"
)

func init() {
	sql.Register("agentlog_mock", mockDriver{})
}

type mockDriver struct{}

func (mockDriver) Open(name string) (driver.Conn, error) { return mockConn{}, nil }

type mockConn struct{}

func (mockConn) Prepare(query string) (driver.Stmt, error) { return mockStmt{}, nil }
func (mockConn) Close() error                              { return nil }
func (mockConn) Begin() (driver.Tx, error)                 { return mockTx{}, nil }

type mockStmt struct{}

func (mockStmt) Close() error  { return nil }
func (mockStmt) NumInput() int { return -1 }
func (mockStmt) Exec(args []driver.Value) (driver.Result, error) {
	return mockResult{}, nil
}
func (mockStmt) Query(args []driver.Value) (driver.Rows, error) {
	return mockRows{}, nil
}

type mockTx struct{}

func (mockTx) Commit() error   { return nil }
func (mockTx) Rollback() error { return nil }

type mockResult struct{}

func (mockResult) LastInsertId() (int64, error) { return 0, nil }
func (mockResult) RowsAffected() (int64, error) { return 3, nil }

type mockRows struct{}

func (mockRows) Columns() []string              { return []string{"id"} }
func (mockRows) Close() error                   { return nil }
func (mockRows) Next(dest []driver.Value) error { return io.EOF }

func TestDBQueryAuto(t *testing.T) {
	mem := NewMemorySink()
	shared = NewLogger("test-db", mem)
	shared.SetLevel(LevelInfo)

	db, err := sql.Open("agentlog_mock", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	wrapped := WrapDB(db)

	if _, err := wrapped.Exec("INSERT INTO t(id) VALUES (1)"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if _, err := wrapped.Query("SELECT id FROM t"); err != nil {
		t.Fatalf("query: %v", err)
	}

	lines := mem.Lines()
	if len(lines) != 2 {
		t.Fatalf("expected 2 db_query events, got %d: %#v", len(lines), lines)
	}

	eventTypeRe := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	allowedKeys := map[string]bool{
		"ts": true, "level": true, "service": true, "event_type": true,
		"session_id": true, "agent_auto": true, "message": true,
		"subsystem": true, "category": true, "env": true, "user_id": true,
		"trace_id": true, "span_id": true, "payload": true,
	}
	for i, line := range lines {
		var L map[string]interface{}
		if err := json.Unmarshal([]byte(line), &L); err != nil {
			t.Fatalf("line %d: not JSON: %v", i, err)
		}
		for k := range L {
			if !allowedKeys[k] {
				t.Fatalf("line %d: unexpected top-level key %q", i, k)
			}
		}
		if L["event_type"] != "db_query" {
			t.Fatalf("line %d: expected event_type db_query, got %v", i, L["event_type"])
		}
		if L["agent_auto"] != true {
			t.Fatalf("line %d: expected agent_auto true", i)
		}
		if !eventTypeRe.MatchString(L["event_type"].(string)) {
			t.Fatalf("line %d: bad event_type pattern", i)
		}
		p, ok := L["payload"].(map[string]interface{})
		if !ok {
			t.Fatalf("line %d: payload missing or not an object", i)
		}
		for _, key := range []string{"operation", "statement", "duration_ms", "rows_affected"} {
			if _, present := p[key]; !present {
				t.Fatalf("line %d: payload missing %q: %v", i, key, p)
			}
		}
	}
}

func TestRecordDBQueryHelper(t *testing.T) {
	mem := NewMemorySink()
	shared = NewLogger("test-db-helper", mem)
	shared.SetLevel(LevelInfo)

	RecordDBQuery("exec", "UPDATE t SET x = 1", 7, 2, nil)

	lines := mem.Lines()
	if len(lines) != 1 {
		t.Fatalf("expected 1 event, got %d", len(lines))
	}
	var L map[string]interface{}
	_ = json.Unmarshal([]byte(lines[0]), &L)
	if L["event_type"] != "db_query" {
		t.Fatalf("expected db_query, got %v", L["event_type"])
	}
}

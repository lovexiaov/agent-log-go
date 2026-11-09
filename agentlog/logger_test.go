package agentlog

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChildIsolation(t *testing.T) {
	mem := NewMemorySink()
	parent := NewLogger("svc", mem)
	parent.SetLevel(LevelInfo)

	child := parent.Child(map[string]interface{}{"user_id": "u7"})

	// Mutating child must not affect parent.
	child.SetLevel(LevelTrace)
	child.Redact([]string{"custom_sensitive"})

	if parent.minLevel != LevelInfo {
		t.Fatalf("parent minLevel mutated by child: %v", parent.minLevel)
	}
	if child.minLevel != LevelTrace {
		t.Fatalf("child minLevel not applied: %v", child.minLevel)
	}
	if parent.redactor.IsSensitive("custom_sensitive") {
		t.Fatalf("parent redactor inherited child's appended key")
	}

	// Events: parent should not carry user_id, child should.
	parent.Info("parent message")
	child.Info("child message")

	lines := mem.Lines()
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	var pLine, cLine string
	for _, l := range lines {
		if contains(l, "parent message") {
			pLine = l
		}
		if contains(l, "child message") {
			cLine = l
		}
	}
	if contains(pLine, "user_id") {
		t.Fatalf("parent event should not contain user_id: %s", pLine)
	}
	if !contains(cLine, `"user_id":"u7"`) {
		t.Fatalf("child event should contain user_id=u7: %s", cLine)
	}
}

func TestSetLevelFiltering(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.SetLevel(LevelWarn)

	logger.Debug("d")
	logger.Trace("t")
	logger.Info("i")
	logger.Warn("w")
	logger.Error("e")

	lines := mem.Lines()
	// only warn + error should pass
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (warn,error), got %d: %#v", len(lines), lines)
	}
}

func TestChildContextPropagation(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.SetLevel(LevelTrace)

	// nested children: context must accumulate, parent untouched.
	base := logger.Child(map[string]interface{}{"user_id": "alice"})
	derived := base.Child(map[string]interface{}{"subsystem": "billing"})

	derived.Info("nested", WithPayload(map[string]interface{}{"amount": 10}))

	lines := mem.Lines()
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	// derived carries both user_id and subsystem
	if !contains(lines[0], `"user_id":"alice"`) {
		t.Fatalf("derived missing accumulated user_id: %s", lines[0])
	}
	if !contains(lines[0], `"subsystem":"billing"`) {
		t.Fatalf("derived missing subsystem: %s", lines[0])
	}

	// base still does not carry subsystem
	base.Info("base only")
	last := mem.Lines()[len(mem.Lines())-1]
	if contains(last, "subsystem") {
		t.Fatalf("base should not carry derived's subsystem: %s", last)
	}
}

func TestAutoAlwaysLogged(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.SetLevel(LevelError) // very high threshold

	// manual debug dropped
	logger.Debug("manual debug")
	// auto always logged
	logger.RecordAuto(string(EventTypeCrash), "panic!", WithPayload(map[string]interface{}{"thread": "main"}))

	lines := mem.Lines()
	if len(lines) != 1 {
		t.Fatalf("auto event must always log; expected 1 line, got %d", len(lines))
	}
	if !contains(lines[0], `"agent_auto":true`) {
		t.Fatalf("auto event missing agent_auto=true: %s", lines[0])
	}
}

func TestLevelWarningIsNotCanonical(t *testing.T) {
	if LevelFromWire("warning") != LevelInfo {
		t.Fatalf("warning is not canonical; should degrade to info")
	}
	if LevelFromName("WARNING") != LevelInfo {
		t.Fatalf("WARNING is not canonical; should degrade to info")
	}
}

func TestHTTPMiddlewareAuto(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("web", mem)
	shared = logger             // route facade auto events to our memory sink
	logger.SetLevel(LevelError) // high threshold; auto must still log

	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "http://example.com/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	lines := mem.Lines()
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 auto network_request line, got %d", len(lines))
	}
	if !contains(lines[0], `"agent_auto":true`) {
		t.Fatalf("middleware event must be auto: %s", lines[0])
	}
	if !contains(lines[0], `"event_type":"network_request"`) {
		t.Fatalf("middleware event must be network_request: %s", lines[0])
	}
}

func TestInstallPanicHandler(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	shared = logger // route facade auto events to our memory sink

	recovered := false
	InstallPanicHandler(func() {
		panic("boom")
	})
	// After recovery, a crash auto-event must have been recorded.
	if mem.LineCount() != 1 {
		t.Fatalf("expected 1 crash auto-event, got %d", mem.LineCount())
	}
	line := mem.Lines()[0]
	if !contains(line, `"event_type":"crash"`) {
		t.Fatalf("expected crash event_type: %s", line)
	}
	if !contains(line, `"agent_auto":true`) {
		t.Fatalf("crash must be auto: %s", line)
	}
	if !contains(line, "boom") {
		t.Fatalf("crash payload should contain exception: %s", line)
	}
	if !contains(line, `"mechanism":"uncaught"`) {
		t.Fatalf("crash payload must carry mechanism=uncaught: %s", line)
	}
	if !contains(line, `"exception_message":"boom"`) {
		t.Fatalf("crash payload must carry exception_message: %s", line)
	}
	_ = recovered
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

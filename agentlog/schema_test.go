package agentlog

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// C7 — SchemaTests. Verifies every emitted JSONL line validates against Schema v1.
func TestSchemaConformance(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("test-service", mem)
	logger.SetLevel(LevelInfo)

	// manual Info with payload containing a non-sensitive key
	logger.Info("user signed in", WithPayload(map[string]interface{}{"host": "api.example.com"}))

	// manual Warn
	logger.Warn("disk almost full")

	// manual Debug — should be DROPPED when minLevel=info
	logger.Debug("verbose trace detail")

	// child logger bound with user_id
	child := logger.Child(map[string]interface{}{"user_id": "u1"})
	child.Info("child scoped event", WithPayload(map[string]interface{}{"host": "child.example.com"}))

	// auto event (network_request) — always logged regardless of minLevel
	logger.RecordAuto(string(EventTypeNetworkRequest), "GET https://api.example.com/orders -> 200",
		WithPayload(map[string]interface{}{"host": "api.example.com", "status": 200}))

	// a log whose payload contains a sensitive key
	logger.Info("auth attempt", WithPayload(map[string]interface{}{"password": "hunter2", "host": "auth.example.com"}))

	lines := mem.Lines()

	// With minLevel=info the debug line is dropped, so we expect 5 lines.
	if len(lines) != 5 {
		t.Fatalf("expected 5 emitted lines (debug dropped), got %d: %#v", len(lines), lines)
	}

	eventTypeRe := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	allowedKeys := map[string]bool{
		"ts": true, "level": true, "service": true, "event_type": true,
		"session_id": true, "agent_auto": true, "message": true,
		"subsystem": true, "category": true, "env": true, "release": true, "user_id": true,
		"trace_id": true, "span_id": true, "payload": true,
	}

	for i, line := range lines {
		var L map[string]interface{}
		if err := json.Unmarshal([]byte(line), &L); err != nil {
			t.Fatalf("line %d: not valid JSON: %v\n%s", i, err, line)
		}

		// 1. L is a map
		if L == nil {
			t.Fatalf("line %d: expected object", i)
		}

		// 2. keys(L) subset of allowed
		for k := range L {
			if !allowedKeys[k] {
				t.Fatalf("line %d: unexpected top-level key %q (additionalProperties:false)", i, k)
			}
		}

		// 3. required keys present
		for _, req := range []string{"ts", "level", "service", "event_type", "session_id", "agent_auto", "message"} {
			if _, ok := L[req]; !ok {
				t.Fatalf("line %d: missing required key %q", i, req)
			}
		}

		// 4. level enum
		level, _ := L["level"].(string)
		switch level {
		case "trace", "debug", "info", "warn", "error", "critical":
		default:
			t.Fatalf("line %d: invalid level %q", i, level)
		}

		// 5. event_type regex
		eventType, _ := L["event_type"].(string)
		if !eventTypeRe.MatchString(eventType) {
			t.Fatalf("line %d: invalid event_type %q", i, eventType)
		}

		// 6. session_id non-empty string
		sid, ok := L["session_id"].(string)
		if !ok || sid == "" {
			t.Fatalf("line %d: session_id must be non-empty string", i)
		}

		// 7. agent_auto bool
		if _, ok := L["agent_auto"].(bool); !ok {
			t.Fatalf("line %d: agent_auto must be bool", i)
		}

		// 8. message string
		if _, ok := L["message"].(string); !ok {
			t.Fatalf("line %d: message must be string", i)
		}

		// 9. payload is object
		if p, ok := L["payload"]; ok {
			if _, isObj := p.(map[string]interface{}); !isObj {
				t.Fatalf("line %d: payload must be object", i)
			}
		}

		// 10. ts looks like RFC3339
		ts, _ := L["ts"].(string)
		if !strings.Contains(ts, "T") || !(strings.HasSuffix(ts, "Z") || strings.ContainsAny(ts[len(ts)-6:], "+-")) {
			t.Fatalf("line %d: ts not RFC3339 (%q)", i, ts)
		}
	}

	// --- semantic assertions ---

	// sensitive password masked
	for _, line := range lines {
		var L map[string]interface{}
		_ = json.Unmarshal([]byte(line), &L)
		if p, ok := L["payload"].(map[string]interface{}); ok {
			if v, exists := p["password"]; exists {
				if v != MaskString {
					t.Fatalf("password payload value not redacted: %v", v)
				}
			}
		}
	}

	// non-sensitive host passes through unchanged on the auth line
	foundHost := false
	for _, line := range lines {
		var L map[string]interface{}
		_ = json.Unmarshal([]byte(line), &L)
		if p, ok := L["payload"].(map[string]interface{}); ok {
			if h, ok := p["host"].(string); ok && h == "auth.example.com" {
				foundHost = true
			}
		}
	}
	if !foundHost {
		t.Fatalf("non-sensitive host key not preserved")
	}

	// child logger event carries user_id=u1
	var childLine string
	for _, line := range lines {
		if strings.Contains(line, "child scoped event") {
			childLine = line
		}
	}
	if childLine == "" {
		t.Fatalf("child event not found")
	}
	var childEvent map[string]interface{}
	_ = json.Unmarshal([]byte(childLine), &childEvent)
	if childEvent["user_id"] != "u1" {
		t.Fatalf("child event missing user_id=u1: %#v", childEvent)
	}
	// parent unchanged: parent's Info did not carry user_id
	var parentLine string
	for _, line := range lines {
		if strings.Contains(line, "user signed in") {
			parentLine = line
		}
	}
	var parentEvent map[string]interface{}
	_ = json.Unmarshal([]byte(parentLine), &parentEvent)
	if _, ok := parentEvent["user_id"]; ok {
		t.Fatalf("parent logger should NOT carry child's user_id: %#v", parentEvent)
	}

	// auto event appears even with minLevel=info (always logged)
	var autoLine string
	for _, line := range lines {
		if strings.Contains(line, "network_request") || strings.Contains(line, "orders -> 200") {
			autoLine = line
		}
	}
	if autoLine == "" {
		t.Fatalf("auto network_request event missing")
	}
	var autoEvent map[string]interface{}
	_ = json.Unmarshal([]byte(autoLine), &autoEvent)
	if autoEvent["agent_auto"] != true {
		t.Fatalf("auto event agent_auto should be true")
	}

	// Debug dropped at info level -> line count already asserted as 5.
}

// TestSchemaConformanceDebugVisible verifies a debug manual event appears when
// minLevel is trace.
func TestSchemaConformanceDebugVisible(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.SetLevel(LevelTrace)

	logger.Info("info msg")
	logger.Debug("debug msg")

	lines := mem.Lines()
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines at trace level, got %d", len(lines))
	}
}

// TestReleaseStampedOnEvents verifies Schema v1.3 release stamping at the
// logger level: both manual and auto events carry it.
func TestReleaseStampedOnEvents(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.SetRelease("1.4.2+sha.abc")

	logger.Info("manual with release")
	logger.RecordAuto(string(EventTypeCrash), "auto with release",
		WithPayload(map[string]interface{}{"mechanism": "uncaught"}))

	lines := mem.Lines()
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	for i, line := range lines {
		var L map[string]interface{}
		if err := json.Unmarshal([]byte(line), &L); err != nil {
			t.Fatalf("line %d: invalid JSON: %v", i, err)
		}
		if L["release"] != "1.4.2+sha.abc" {
			t.Fatalf("line %d: release not stamped: %#v", i, L)
		}
	}
}

func TestReleaseAbsentWhenNotConfigured(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.Info("no release")

	var L map[string]interface{}
	if err := json.Unmarshal([]byte(mem.Lines()[0]), &L); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := L["release"]; ok {
		t.Fatalf("release must be absent when not configured: %#v", L)
	}
}

func TestReleaseTooLongDropped(t *testing.T) {
	mem := NewMemorySink()
	logger := NewLogger("svc", mem)
	logger.SetRelease(strings.Repeat("x", 200))
	logger.Info("long release")

	var L map[string]interface{}
	if err := json.Unmarshal([]byte(mem.Lines()[0]), &L); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := L["release"]; ok {
		t.Fatalf("too-long release must be dropped: %#v", L)
	}
}

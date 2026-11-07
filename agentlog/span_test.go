package agentlog

import (
	"encoding/json"
	"testing"
)

// decodeLine parses one emitted JSONL line.
func decodeLine(t *testing.T, line string) map[string]interface{} {
	t.Helper()
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, line)
	}
	return event
}

func TestSpanIDIsSerializedLowercase(t *testing.T) {
	sink := NewMemorySink()
	logger := NewLogger("svc", sink)
	logger.Info("bound", WithTraceID("0123456789abcdef0123456789abcdef"), WithSpanID("A1B2C3D4E5F60718"))

	lines := sink.Lines()
	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	event := decodeLine(t, lines[0])
	if event["span_id"] != "a1b2c3d4e5f60718" {
		t.Errorf("span_id = %v, want lowercase hex", event["span_id"])
	}
	if event["trace_id"] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("trace_id = %v", event["trace_id"])
	}
}

func TestInvalidSpanIDIsDropped(t *testing.T) {
	for _, value := range []string{"not-a-span", "a1b2c3d4e5f6071", "a1b2c3d4e5f607189", "zzzzzzzzzzzzzzzz"} {
		sink := NewMemorySink()
		logger := NewLogger("svc", sink)
		logger.Info("bad span", WithSpanID(value))

		event := decodeLine(t, sink.Lines()[0])
		if _, ok := event["span_id"]; ok {
			t.Errorf("span_id %q should have been dropped, got %v", value, event["span_id"])
		}
	}
}

func TestChildBindsSpanID(t *testing.T) {
	sink := NewMemorySink()
	logger := NewLogger("svc", sink)
	span := logger.Child(map[string]interface{}{
		"trace_id": "0123456789abcdef0123456789abcdef",
		"spanId":   "a1b2c3d4e5f60718", // camelCase is normalized at the boundary
	})

	span.Info("inside span")
	logger.Info("outside span")

	events := sink.Lines()
	inside := decodeLine(t, events[0])
	outside := decodeLine(t, events[1])

	if inside["span_id"] != "a1b2c3d4e5f60718" {
		t.Errorf("child span_id = %v", inside["span_id"])
	}
	if _, ok := outside["span_id"]; ok {
		t.Errorf("parent must not inherit the child's span_id: %v", outside["span_id"])
	}
	if _, ok := outside["trace_id"]; ok {
		t.Errorf("parent must not inherit the child's trace_id: %v", outside["trace_id"])
	}
}

func TestSpanIDDoesNotLeakIntoPayload(t *testing.T) {
	sink := NewMemorySink()
	logger := NewLogger("svc", sink)
	logger.Child(map[string]interface{}{"span_id": "a1b2c3d4e5f60718"}).Info("x")

	event := decodeLine(t, sink.Lines()[0])
	if payload, ok := event["payload"].(map[string]interface{}); ok {
		if _, leaked := payload["span_id"]; leaked {
			t.Errorf("span_id leaked into payload: %v", payload)
		}
	}
}

func TestSpanIDFlowsIntoOTLP(t *testing.T) {
	sink := NewOTLPSink(nil)
	logger := NewLogger("svc", sink)
	child := logger.Child(map[string]interface{}{
		"trace_id": "0123456789abcdef0123456789abcdef",
		"span_id":  "a1b2c3d4e5f60718",
	})
	child.Info("plain log in span")
	child.RecordAuto(string(EventTypeNetworkRequest), "GET https://api.example.com/x -> 200",
		WithPayload(map[string]interface{}{
			"method": "GET", "url": "https://api.example.com/x", "status": 200, "duration_ms": 5,
		}))

	batch, err := sink.Drain()
	if err != nil {
		t.Fatal(err)
	}
	records := batch["logs"].(map[string]interface{})["resourceLogs"].([]interface{})[0].(map[string]interface{})["scopeLogs"].([]interface{})[0].(map[string]interface{})["logRecords"].([]interface{})
	spans := batch["traces"].(map[string]interface{})["resourceSpans"].([]interface{})[0].(map[string]interface{})["scopeSpans"].([]interface{})[0].(map[string]interface{})["spans"].([]interface{})

	if len(records) != 2 || len(spans) != 1 {
		t.Fatalf("records=%d spans=%d, want 2/1", len(records), len(spans))
	}
	for i, item := range records {
		record := item.(map[string]interface{})
		if record["spanId"] != "a1b2c3d4e5f60718" {
			t.Errorf("record %d spanId = %v (the bound span must be preserved)", i, record["spanId"])
		}
		if record["traceId"] != "0123456789abcdef0123456789abcdef" {
			t.Errorf("record %d traceId = %v", i, record["traceId"])
		}
	}
	span := spans[0].(map[string]interface{})
	if span["spanId"] != "a1b2c3d4e5f60718" {
		t.Errorf("span spanId = %v, want the bound id", span["spanId"])
	}
}

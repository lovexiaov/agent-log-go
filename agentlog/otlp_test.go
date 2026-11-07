package agentlog

import (
	"encoding/json"
	"strconv"
	"testing"
)

// --- helpers ------------------------------------------------------------- //

// baseEvent returns a minimal valid Schema v1 event object (post-parse shape).
func baseEvent(eventType string, level Level) map[string]interface{} {
	return map[string]interface{}{
		"ts":         "2026-11-07T10:51:00.123456789Z",
		"level":      string(level),
		"service":    "com.example.demo",
		"event_type": eventType,
		"session_id": "session-1",
		"agent_auto": eventType != string(EventTypeLog),
		"message":    "hello",
	}
}

// attributeValue finds one attribute's AnyValue in an OTLP attribute list.
func attributeValue(t *testing.T, attrs interface{}, key string) map[string]interface{} {
	t.Helper()
	list, ok := attrs.([]interface{})
	if !ok {
		t.Fatalf("attributes is not a list: %T", attrs)
	}
	for _, item := range list {
		kv, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("attribute entry is not an object: %T", item)
		}
		if kv["key"] != key {
			continue
		}
		value, ok := kv["value"].(map[string]interface{})
		if !ok {
			t.Fatalf("attribute %q value is not an object: %T", key, kv["value"])
		}
		return value
	}
	return nil
}

// stringAttr returns a stringValue attribute, or "" when absent.
func stringAttr(t *testing.T, attrs interface{}, key string) string {
	t.Helper()
	value := attributeValue(t, attrs, key)
	if value == nil {
		return ""
	}
	text, _ := value["stringValue"].(string)
	return text
}

// --- level ---------------------------------------------------------------- //

func TestLevelToSeverity(t *testing.T) {
	cases := []struct {
		level string
		text  string
		num   int
	}{
		{"trace", "TRACE", 1},
		{"debug", "DEBUG", 5},
		{"info", "INFO", 9},
		{"warn", "WARN", 13},
		{"error", "ERROR", 17},
		{"critical", "FATAL", 21},
		{"warning", "WARN", 13}, // read-side alias
		{"WARN", "WARN", 13},    // case-insensitive
		{"", "INFO", 9},         // unknown degrades to INFO
		{"nonsense", "INFO", 9},
	}
	for _, tc := range cases {
		text, num := LevelToSeverity(tc.level)
		if text != tc.text || num != tc.num {
			t.Errorf("level %q -> (%s,%d), want (%s,%d)", tc.level, text, num, tc.text, tc.num)
		}
	}
}

// --- timestamps ----------------------------------------------------------- //

func TestParseTimestampNanos(t *testing.T) {
	cases := []struct {
		ts   string
		want int64
	}{
		{"1970-01-01T00:00:01Z", 1000000000},
		{"1970-01-01T08:00:00+08:00", 0},
		{"1970-01-01T00:00:00.000000001Z", 1},
		{"1970-01-01 00:00:02Z", 2000000000},     // space separator
		{"1970-01-01T00:00:03+0000", 3000000000}, // compact offset
		{"1970-01-01T00:00:04", 4000000000},      // no offset -> UTC
		// sub-nanosecond digits are truncated, not rejected
		{"1970-01-01T00:00:00.1234567891234Z", 123456789},
	}
	for _, tc := range cases {
		got, err := ParseTimestampNanos(tc.ts)
		if err != nil {
			t.Fatalf("ParseTimestampNanos(%q): %v", tc.ts, err)
		}
		if got != tc.want {
			t.Errorf("ParseTimestampNanos(%q) = %d, want %d", tc.ts, got, tc.want)
		}
	}

	for _, bad := range []string{"", "not-a-date", "2026-11-07"} {
		if _, err := ParseTimestampNanos(bad); err == nil {
			t.Errorf("ParseTimestampNanos(%q) should fail", bad)
		}
	}
}

// --- AnyValue ------------------------------------------------------------- //

func TestToOTLPAnyValue(t *testing.T) {
	// bool must win over the integer branch.
	if got := ToOTLPAnyValue(true); got["boolValue"] != true {
		t.Errorf("bool -> %v", got)
	}
	// int64 is a string: JSON numbers are doubles.
	if got := ToOTLPAnyValue(int64(42)); got["intValue"] != "42" {
		t.Errorf("int64 -> %v", got)
	}
	if got := ToOTLPAnyValue(42); got["intValue"] != "42" {
		t.Errorf("int -> %v", got)
	}
	if got := ToOTLPAnyValue(1.5); got["doubleValue"] != 1.5 {
		t.Errorf("float -> %v", got)
	}
	if got := ToOTLPAnyValue("x"); got["stringValue"] != "x" {
		t.Errorf("string -> %v", got)
	}

	array, ok := ToOTLPAnyValue([]interface{}{1, "two"})["arrayValue"].(map[string]interface{})
	if !ok {
		t.Fatalf("array -> %v", ToOTLPAnyValue([]interface{}{1}))
	}
	values := array["values"].([]interface{})
	if len(values) != 2 {
		t.Fatalf("array length = %d", len(values))
	}
	if values[0].(map[string]interface{})["intValue"] != "1" {
		t.Errorf("array[0] = %v", values[0])
	}
	if values[1].(map[string]interface{})["stringValue"] != "two" {
		t.Errorf("array[1] = %v", values[1])
	}

	nested, ok := ToOTLPAnyValue(map[string]interface{}{"a": "b"})["kvlistValue"].(map[string]interface{})
	if !ok {
		t.Fatalf("map -> %v", ToOTLPAnyValue(map[string]interface{}{}))
	}
	if len(nested["values"].([]interface{})) != 1 {
		t.Errorf("kvlist = %v", nested)
	}
}

// --- ids ------------------------------------------------------------------ //

func TestTraceIDHex(t *testing.T) {
	// A 32-hex trace id passes through, lowercased.
	event := baseEvent("log", LevelInfo)
	event["trace_id"] = "0123456789ABCDEF0123456789ABCDEF"
	if got := TraceIDHex(event); got != "0123456789abcdef0123456789abcdef" {
		t.Errorf("hex trace id = %q", got)
	}

	// Anything else is hashed deterministically into a 32-hex id.
	event["trace_id"] = "trace_abc"
	first := TraceIDHex(event)
	second := TraceIDHex(event)
	if first != second {
		t.Fatalf("hashed trace id is not deterministic: %q vs %q", first, second)
	}
	if len(first) != 32 {
		t.Errorf("hashed trace id length = %d", len(first))
	}

	// Two events sharing a logical trace id land in the same OTLP trace.
	other := baseEvent("log", LevelInfo)
	other["trace_id"] = "trace_abc"
	if TraceIDHex(other) != first {
		t.Errorf("same trace_id produced different ids")
	}

	delete(event, "trace_id")
	if got := TraceIDHex(event); got != "" {
		t.Errorf("missing trace_id -> %q, want empty", got)
	}
}

func TestSpanIDExplicitWins(t *testing.T) {
	event := baseEvent("network_request", LevelInfo)
	event["span_id"] = "A1B2C3D4E5F60718" // uppercase is normalized
	if got := SpanIDHex(event); got != "a1b2c3d4e5f60718" {
		t.Errorf("explicit span id = %q", got)
	}

	// An invalid span_id is ignored and the derived id is used instead.
	event["span_id"] = "not-a-span-id"
	derived := SpanIDHex(event)
	if derived == "" || derived == "not-a-span-id" || len(derived) != 16 {
		t.Errorf("derived span id = %q", derived)
	}
	if derived != SpanIDHex(event) {
		t.Errorf("derived span id is not deterministic")
	}
}

func TestNormalizeSpanID(t *testing.T) {
	cases := map[string]string{
		"a1b2c3d4e5f60718":  "a1b2c3d4e5f60718",
		"A1B2C3D4E5F60718":  "a1b2c3d4e5f60718",
		"a1b2c3d4e5f6071":   "",
		"a1b2c3d4e5f607189": "",
		"g1b2c3d4e5f60718":  "",
		"":                  "",
	}
	for input, want := range cases {
		if got := NormalizeSpanID(input); got != want {
			t.Errorf("NormalizeSpanID(%q) = %q, want %q", input, got, want)
		}
	}
}

// --- LogRecord ------------------------------------------------------------ //

func TestLogRecordWithoutTraceHasNoIds(t *testing.T) {
	record, err := EventToLogRecord(baseEvent("log", LevelInfo))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := record["traceId"]; ok {
		t.Errorf("record should not carry traceId: %v", record)
	}
	if _, ok := record["spanId"]; ok {
		t.Errorf("record should not carry spanId: %v", record)
	}
}

func TestLogRecordWithTraceButNoSpanForPlainLog(t *testing.T) {
	event := baseEvent("log", LevelInfo)
	event["trace_id"] = "0123456789abcdef0123456789abcdef"
	record, err := EventToLogRecord(event)
	if err != nil {
		t.Fatal(err)
	}
	if record["traceId"] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("traceId = %v", record["traceId"])
	}
	if _, ok := record["spanId"]; ok {
		t.Errorf("a plain log must not reference a span: %v", record)
	}
}

func TestOperationLogRecordSharesSpanId(t *testing.T) {
	event := baseEvent(string(EventTypeNetworkRequest), LevelInfo)
	event["trace_id"] = "0123456789abcdef0123456789abcdef"
	event["span_id"] = "a1b2c3d4e5f60718"

	record, err := EventToLogRecord(event)
	if err != nil {
		t.Fatal(err)
	}
	span, ok, err := EventToSpan(event)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a span")
	}
	if record["spanId"] != "a1b2c3d4e5f60718" {
		t.Errorf("record spanId = %v", record["spanId"])
	}
	if record["spanId"] != span["spanId"] {
		t.Errorf("record spanId %v != span spanId %v", record["spanId"], span["spanId"])
	}
	if record["traceId"] != span["traceId"] {
		t.Errorf("record/span traceId mismatch: %v vs %v", record["traceId"], span["traceId"])
	}
}

func TestLogRecordFields(t *testing.T) {
	event := baseEvent("log", LevelCritical)
	event["message"] = "boom"
	event["subsystem"] = "auth"
	event["category"] = "app"
	event["user_id"] = "u-1"
	event["agent_auto"] = false
	event["payload"] = map[string]interface{}{"amount": 199, "ok": true}

	record, err := EventToLogRecord(event)
	if err != nil {
		t.Fatal(err)
	}
	if record["severityText"] != "FATAL" || record["severityNumber"] != 21 {
		t.Errorf("severity = %v/%v", record["severityText"], record["severityNumber"])
	}
	if body := record["body"].(map[string]interface{}); body["stringValue"] != "boom" {
		t.Errorf("body = %v", body)
	}
	if _, ok := record["timeUnixNano"].(string); !ok {
		t.Errorf("timeUnixNano must be a string (int64 JSON rule): %T", record["timeUnixNano"])
	}
	if record["timeUnixNano"] != record["observedTimeUnixNano"] {
		t.Errorf("observedTimeUnixNano mismatch")
	}
	if got := stringAttr(t, record["attributes"], "agent.event.type"); got != "log" {
		t.Errorf("agent.event.type = %q", got)
	}
	if got := stringAttr(t, record["attributes"], "agent.subsystem"); got != "auth" {
		t.Errorf("agent.subsystem = %q", got)
	}
	if got := stringAttr(t, record["attributes"], "agent.category"); got != "app" {
		t.Errorf("agent.category = %q", got)
	}
	if got := stringAttr(t, record["attributes"], "enduser.id"); got != "u-1" {
		t.Errorf("enduser.id = %q", got)
	}
	if value := attributeValue(t, record["attributes"], "payload.amount"); value["intValue"] != "199" {
		t.Errorf("payload.amount = %v", value)
	}
	if value := attributeValue(t, record["attributes"], "payload.ok"); value["boolValue"] != true {
		t.Errorf("payload.ok = %v", value)
	}
}

func TestCrashRecordCarriesExceptionAttributes(t *testing.T) {
	event := baseEvent(string(EventTypeCrash), LevelCritical)
	event["payload"] = map[string]interface{}{
		"exception": "runtime error: boom",
		"stack":     "goroutine 1 [running]: ...",
	}
	record, err := EventToLogRecord(event)
	if err != nil {
		t.Fatal(err)
	}
	if got := stringAttr(t, record["attributes"], "exception.type"); got != "runtime error: boom" {
		t.Errorf("exception.type = %q", got)
	}
	if got := stringAttr(t, record["attributes"], "exception.stacktrace"); got == "" {
		t.Error("exception.stacktrace missing")
	}
}

// --- spans ---------------------------------------------------------------- //

func TestHTTPSpan(t *testing.T) {
	event := baseEvent(string(EventTypeNetworkRequest), LevelInfo)
	event["payload"] = map[string]interface{}{
		"method":      "GET",
		"url":         "https://api.example.com:8443/orders?page=2",
		"status":      503,
		"duration_ms": 14,
	}
	span, ok, err := EventToSpan(event)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a span")
	}
	if span["name"] != "GET https://api.example.com:8443/orders?page=2" {
		t.Errorf("name = %v", span["name"])
	}
	if span["kind"] != SpanKindClient {
		t.Errorf("kind = %v", span["kind"])
	}
	if got := stringAttr(t, span["attributes"], "http.request.method"); got != "GET" {
		t.Errorf("http.request.method = %q", got)
	}
	if got := stringAttr(t, span["attributes"], "server.address"); got != "api.example.com" {
		t.Errorf("server.address = %q", got)
	}
	if value := attributeValue(t, span["attributes"], "http.response.status_code"); value["intValue"] != "503" {
		t.Errorf("status_code = %v", value)
	}
	if got := stringAttr(t, span["attributes"], "error.type"); got != "503" {
		t.Errorf("error.type = %q", got)
	}
	if status, ok := span["status"].(map[string]interface{}); !ok || status["code"] != StatusCodeError {
		t.Errorf("status = %v", span["status"])
	}

	// start/end: 14ms window.
	start, _ := ParseTimestampNanos(event["ts"].(string))
	if span["startTimeUnixNano"] != strconv.FormatInt(start, 10) {
		t.Errorf("start = %v, want %d", span["startTimeUnixNano"], start)
	}
	if span["endTimeUnixNano"] != strconv.FormatInt(start+14*1000000, 10) {
		t.Errorf("end = %v", span["endTimeUnixNano"])
	}
}

func TestHTTPSpanSuccessHasNoErrorStatus(t *testing.T) {
	event := baseEvent(string(EventTypeNetworkRequest), LevelInfo)
	event["payload"] = map[string]interface{}{
		"method": "GET", "url": "https://api.example.com/x", "status": 200,
	}
	span, _, err := EventToSpan(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := span["status"]; ok {
		t.Errorf("successful span must not set status: %v", span["status"])
	}
	if got := stringAttr(t, span["attributes"], "error.type"); got != "" {
		t.Errorf("error.type = %q", got)
	}
}

func TestDBSpanUsesStatement(t *testing.T) {
	event := baseEvent(string(EventTypeDBQuery), LevelWarn)
	event["payload"] = map[string]interface{}{
		"operation":   "SELECT",
		"statement":   "SELECT * FROM users WHERE id = ?",
		"db_system":   "postgresql",
		"duration_ms": 3,
	}
	span, ok, err := EventToSpan(event)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a span")
	}
	if span["name"] != "SELECT SELECT * FROM users WHERE id = ?" {
		t.Errorf("name = %v", span["name"])
	}
	if got := stringAttr(t, span["attributes"], "db.system"); got != "postgresql" {
		t.Errorf("db.system = %q", got)
	}
	if got := stringAttr(t, span["attributes"], "db.operation"); got != "SELECT" {
		t.Errorf("db.operation = %q", got)
	}
	if got := stringAttr(t, span["attributes"], "db.statement"); got != "SELECT * FROM users WHERE id = ?" {
		t.Errorf("db.statement = %q", got)
	}
}

// Legacy JSONL written before the Schema v1.1 key unification still maps.
func TestDBSpanAcceptsLegacyKeys(t *testing.T) {
	for _, key := range []string{"statement", "sql", "query"} {
		event := baseEvent(string(EventTypeDBQuery), LevelWarn)
		event["payload"] = map[string]interface{}{
			"operation": "INSERT",
			key:         "INSERT INTO t VALUES (1)",
		}
		span, _, err := EventToSpan(event)
		if err != nil {
			t.Fatal(err)
		}
		if got := stringAttr(t, span["attributes"], "db.statement"); got != "INSERT INTO t VALUES (1)" {
			t.Errorf("key %q: db.statement = %q", key, got)
		}
	}
}

func TestNonOperationEventsProduceNoSpan(t *testing.T) {
	for _, eventType := range []string{"log", "ui_interaction", "screen_view", "crash", "hang"} {
		event := baseEvent(eventType, LevelInfo)
		span, ok, err := EventToSpan(event)
		if err != nil {
			t.Fatalf("%s: %v", eventType, err)
		}
		if ok || span != nil {
			t.Errorf("%s should not produce a span", eventType)
		}
	}
}

func TestSpanWithoutTraceGetsSessionTrace(t *testing.T) {
	event := baseEvent(string(EventTypeDBQuery), LevelInfo)
	event["payload"] = map[string]interface{}{"statement": "SELECT 1"}
	first, _, err := EventToSpan(event)
	if err != nil {
		t.Fatal(err)
	}
	traceID, _ := first["traceId"].(string)
	if len(traceID) != 32 {
		t.Fatalf("synthetic traceId = %q", traceID)
	}

	// A second span in the same session joins the same synthetic trace.
	second := baseEvent(string(EventTypeDBQuery), LevelInfo)
	second["message"] = "another"
	second["payload"] = map[string]interface{}{"statement": "SELECT 2"}
	other, _, err := EventToSpan(second)
	if err != nil {
		t.Fatal(err)
	}
	if other["traceId"] != traceID {
		t.Errorf("same session should share the synthetic trace: %v vs %v", other["traceId"], traceID)
	}
	if other["spanId"] == first["spanId"] {
		t.Errorf("different events must not share a span id")
	}
}

// --- batches -------------------------------------------------------------- //

func TestBatchGroupsByResource(t *testing.T) {
	first := baseEvent("log", LevelInfo)
	second := baseEvent("log", LevelInfo)
	second["service"] = "other-service"
	third := baseEvent("log", LevelInfo)

	logs, err := ToOTLPLogs([]interface{}{first, second, third}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resourceLogs := logs["resourceLogs"].([]interface{})
	if len(resourceLogs) != 2 {
		t.Fatalf("expected 2 resource groups, got %d", len(resourceLogs))
	}
	// First group keeps input order: first + third.
	firstGroup := resourceLogs[0].(map[string]interface{})
	records := firstGroup["scopeLogs"].([]interface{})[0].(map[string]interface{})["logRecords"].([]interface{})
	if len(records) != 2 {
		t.Errorf("first group records = %d, want 2", len(records))
	}
	resource := firstGroup["resource"].(map[string]interface{})
	if got := stringAttr(t, resource["attributes"], "service.name"); got != "com.example.demo" {
		t.Errorf("service.name = %q", got)
	}
	if got := stringAttr(t, resource["attributes"], "session.id"); got != "session-1" {
		t.Errorf("session.id = %q", got)
	}
	if got := stringAttr(t, resource["attributes"], "telemetry.sdk.language"); got != "go" {
		t.Errorf("telemetry.sdk.language = %q", got)
	}
}

func TestBatchSkipsGroupsWithoutSpans(t *testing.T) {
	operation := baseEvent(string(EventTypeNetworkRequest), LevelInfo)
	operation["payload"] = map[string]interface{}{"method": "GET", "url": "https://x.example/a"}
	plain := baseEvent("log", LevelInfo)
	plain["service"] = "plain-service"

	traces, err := ToOTLPSpans([]interface{}{operation, plain}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resourceSpans := traces["resourceSpans"].([]interface{})
	if len(resourceSpans) != 1 {
		t.Fatalf("groups without spans must be skipped, got %d groups", len(resourceSpans))
	}
}

func TestExtraResourceDoesNotOverrideBuiltins(t *testing.T) {
	event := baseEvent("log", LevelInfo)
	logs, err := ToOTLPLogs([]interface{}{event}, map[string]interface{}{
		"service.name":    "impostor",
		"service.version": "9.9.9",
	})
	if err != nil {
		t.Fatal(err)
	}
	resource := logs["resourceLogs"].([]interface{})[0].(map[string]interface{})["resource"].(map[string]interface{})
	if got := stringAttr(t, resource["attributes"], "service.name"); got != "com.example.demo" {
		t.Errorf("service.name was overridden: %q", got)
	}
	if got := stringAttr(t, resource["attributes"], "service.version"); got != "9.9.9" {
		t.Errorf("service.version not added: %q", got)
	}
	// No duplicate keys in the resource attribute list.
	seen := map[string]int{}
	for _, item := range resource["attributes"].([]interface{}) {
		seen[item.(map[string]interface{})["key"].(string)]++
	}
	for key, count := range seen {
		if count > 1 {
			t.Errorf("duplicate resource attribute %q (%d)", key, count)
		}
	}
}

func TestEmptyBatch(t *testing.T) {
	logs, err := ToOTLPLogs(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs["resourceLogs"].([]interface{})) != 0 {
		t.Errorf("expected empty resourceLogs")
	}
	traces, err := ToOTLPSpans(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces["resourceSpans"].([]interface{})) != 0 {
		t.Errorf("expected empty resourceSpans")
	}
}

func TestToOTLPJSONIsValidJSON(t *testing.T) {
	event := baseEvent(string(EventTypeNetworkRequest), LevelInfo)
	event["payload"] = map[string]interface{}{"method": "GET", "url": "https://x.example/a", "n": int64(1)}
	batch, err := ToOTLP([]interface{}{event}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := ToOTLPJSON(batch)
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("OTLP body is not valid JSON: %v\n%s", err, body)
	}
	if _, ok := decoded["logs"]; !ok {
		t.Errorf("missing logs signal")
	}
	if _, ok := decoded["traces"]; !ok {
		t.Errorf("missing traces signal")
	}
}

// --- sink ----------------------------------------------------------------- //

func TestOTLPSinkDrain(t *testing.T) {
	sink := NewOTLPSink(map[string]interface{}{"service.version": "1.0.0"})
	logger := NewLogger("svc", sink)
	logger.SetLevel(LevelInfo)

	logger.Info("manual line", WithPayload(map[string]interface{}{"password": "hunter2"}))
	logger.Debug("dropped by min level")
	logger.RecordAuto(string(EventTypeNetworkRequest), "GET https://api.example.com/x -> 200",
		WithPayload(map[string]interface{}{
			"method": "GET", "url": "https://api.example.com/x", "status": 200, "duration_ms": 7,
		}))

	if sink.Len() != 2 {
		t.Fatalf("buffered %d lines, want 2 (debug dropped)", sink.Len())
	}
	if sink.Path() != "<otlp>" {
		t.Errorf("Path() = %q", sink.Path())
	}

	batch, err := sink.Drain()
	if err != nil {
		t.Fatal(err)
	}
	if sink.Len() != 0 {
		t.Errorf("drain must clear the buffer, %d left", sink.Len())
	}

	records := batch["logs"].(map[string]interface{})["resourceLogs"].([]interface{})[0].(map[string]interface{})["scopeLogs"].([]interface{})[0].(map[string]interface{})["logRecords"].([]interface{})
	if len(records) != 2 {
		t.Fatalf("log records = %d, want 2", len(records))
	}
	// Redaction happened in the logger, before the sink saw the line.
	if got := stringAttr(t, records[0].(map[string]interface{})["attributes"], "payload.password"); got != MaskString {
		t.Errorf("password not redacted: %q", got)
	}

	spans := batch["traces"].(map[string]interface{})["resourceSpans"].([]interface{})[0].(map[string]interface{})["scopeSpans"].([]interface{})[0].(map[string]interface{})["spans"].([]interface{})
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	span := spans[0].(map[string]interface{})
	if span["name"] != "GET https://api.example.com/x" {
		t.Errorf("span name = %v", span["name"])
	}
}

func TestOTLPSinkAcceptsLogEventValue(t *testing.T) {
	event := LogEvent{
		TS:        "2026-11-07T10:51:00Z",
		Level:     LevelInfo,
		Service:   "svc",
		EventType: EventTypeLog,
		SessionID: "s-1",
		Message:   "from struct",
	}
	record, err := EventToLogRecord(event)
	if err != nil {
		t.Fatal(err)
	}
	if body := record["body"].(map[string]interface{}); body["stringValue"] != "from struct" {
		t.Errorf("body = %v", body)
	}
}

func TestUnsupportedEventInput(t *testing.T) {
	if _, err := EventToLogRecord(42); err == nil {
		t.Error("unsupported input should fail")
	}
	if _, err := EventToLogRecord("{not json}"); err == nil {
		t.Error("malformed JSON should fail")
	}
}

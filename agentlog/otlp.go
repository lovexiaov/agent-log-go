package agentlog

// This file implements the D3 ("OTel alignment") capability for Go: it
// *translates* already-emitted Schema v1 events into OTLP/JSON so they can be
// shipped to any OTLP-compatible Collector (Jaeger / Tempo / Loki / Datadog /
// Grafana ...).
//
// It is an export-only layer: the wire format is unchanged, no OTel SDK is
// pulled in, and no OTel concept other than the (Schema v1.1) `span_id` leaks
// back into the JSONL format.
//
// Mapping (authoritative spec: docs/logging-framework/OTLP-MAPPING.md):
//
//	every event          -> one OTLP LogRecord
//	network_request      -> additionally one SPAN_KIND_CLIENT span (HTTP semconv)
//	db_query             -> additionally one SPAN_KIND_CLIENT span (DB semconv)
//	crash / hang         -> LogRecord only, plus exception.* attributes
//	other event types    -> LogRecord only
//
// Ids are *deterministic* (SHA-256 derived), never random: exporting the same
// event twice yields the same traceId/spanId, so retries and re-exports stay
// idempotent. An event carrying its own Schema v1.1 `span_id` wins over the
// derived one, which is what lets a log line point at a real span.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SDKVersion is reported in the OTLP InstrumentationScope and resource.
// Keep in sync with CHANGELOG.md.
const SDKVersion = "1.0.0"

// OTLP enum values in their protobuf numeric form (what OTLP/JSON expects).
const (
	// SpanKindClient marks an outbound call span.
	SpanKindClient = 3
	// StatusCodeError marks a span as failed.
	StatusCodeError = 2
)

// otlpScope is the InstrumentationScope reported on every batch.
const otlpScopeName = "agentlog.otlp"

// OTel semantic-convention keys. Some are still marked Experimental upstream;
// they are centralized here so a semconv revision only touches one place.
const (
	semconvServiceName     = "service.name"
	semconvDeploymentEnv   = "deployment.environment"
	semconvSessionID       = "session.id"
	semconvEnduserID       = "enduser.id"
	semconvHTTPMethod      = "http.request.method"
	semconvURLFull         = "url.full"
	semconvHTTPStatus      = "http.response.status_code"
	semconvServerAddress   = "server.address"
	semconvDBSystem        = "db.system"
	semconvDBOperation     = "db.operation"
	semconvDBStatement     = "db.statement"
	semconvErrorType       = "error.type"
	semconvExceptionType   = "exception.type"
	semconvExceptionMsg    = "exception.message"
	semconvExceptionStack  = "exception.stacktrace"
	attrEventType          = "agent.event.type"
	attrAuto               = "agent.auto"
	attrSubsystem          = "agent.subsystem"
	attrCategory           = "agent.category"
	semconvTelemetrySDK    = "telemetry.sdk.name"
	semconvTelemetryLang   = "telemetry.sdk.language"
	semconvTelemetryVer    = "telemetry.sdk.version"
	payloadAttributePrefix = "payload."
)

// Payload key candidates, checked in order. The canonical SQL key is
// "statement" (unified across the six SDKs in Schema v1.1); "sql" and "query"
// are accepted on read only, so JSONL written by older builds still maps.
var (
	httpMethodKeys  = []string{"method", "http_method", "http.method"}
	urlKeys         = []string{"url", "url_full", "url.full"}
	statusKeys      = []string{"status_code", "status", "statusCode", "http.status_code"}
	durationKeys    = []string{"duration_ms", "durationMs", "duration"}
	sqlKeys         = []string{"statement", "sql", "query"}
	dbSystemKeys    = []string{"db_system", "db.system", "system"}
	dbOperationKeys = []string{"operation", "op"}
)

// severity is the OTel logs severity pair for one agent-log level.
type severity struct {
	text   string
	number int
}

// severityByLevel maps the six canonical wire levels. OTel numbers step by 4
// to leave room for intermediates; only the canonical values are used here.
var severityByLevel = map[Level]severity{
	LevelTrace:    {"TRACE", 1},
	LevelDebug:    {"DEBUG", 5},
	LevelInfo:     {"INFO", 9},
	LevelWarn:     {"WARN", 13},
	LevelError:    {"ERROR", 17},
	LevelCritical: {"FATAL", 21},
}

// --------------------------------------------------------------------------- //
// Input normalization
// --------------------------------------------------------------------------- //

// normalizeOTLPEvent accepts a decoded event object, a JSONL line, or a
// LogEvent value.
func normalizeOTLPEvent(raw interface{}) (map[string]interface{}, error) {
	switch v := raw.(type) {
	case map[string]interface{}:
		return v, nil
	case string:
		return parseEventLine(v)
	case LogEvent:
		return parseEventLine(v.ToJSONLine())
	case *LogEvent:
		if v == nil {
			return nil, fmt.Errorf("agentlog: nil *LogEvent")
		}
		return parseEventLine(v.ToJSONLine())
	}
	return nil, fmt.Errorf("agentlog: unsupported event type %T", raw)
}

func parseEventLine(line string) (map[string]interface{}, error) {
	parsed, err := ParseJSON(line)
	if err != nil {
		return nil, fmt.Errorf("agentlog: cannot decode event: %w", err)
	}
	obj, ok := parsed.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("agentlog: event line is not a JSON object")
	}
	return obj, nil
}

// --------------------------------------------------------------------------- //
// Value / attribute conversion
// --------------------------------------------------------------------------- //

// ToOTLPAnyValue converts a Go value into an OTLP AnyValue (proto3 JSON form).
//
// int64 is rendered as a string, as mandated by the protobuf JSON mapping: JSON
// numbers are doubles and cannot represent 64-bit integers exactly.
func ToOTLPAnyValue(value interface{}) map[string]interface{} {
	switch v := value.(type) {
	case nil:
		// A nil inside an array still needs a value; an empty string is the
		// least surprising representation.
		return map[string]interface{}{"stringValue": ""}
	case bool:
		return map[string]interface{}{"boolValue": v}
	case string:
		return map[string]interface{}{"stringValue": v}
	case int:
		return map[string]interface{}{"intValue": strconv.FormatInt(int64(v), 10)}
	case int8:
		return map[string]interface{}{"intValue": strconv.FormatInt(int64(v), 10)}
	case int16:
		return map[string]interface{}{"intValue": strconv.FormatInt(int64(v), 10)}
	case int32:
		return map[string]interface{}{"intValue": strconv.FormatInt(int64(v), 10)}
	case int64:
		return map[string]interface{}{"intValue": strconv.FormatInt(v, 10)}
	case uint:
		return map[string]interface{}{"intValue": strconv.FormatUint(uint64(v), 10)}
	case uint8:
		return map[string]interface{}{"intValue": strconv.FormatUint(uint64(v), 10)}
	case uint16:
		return map[string]interface{}{"intValue": strconv.FormatUint(uint64(v), 10)}
	case uint32:
		return map[string]interface{}{"intValue": strconv.FormatUint(uint64(v), 10)}
	case uint64:
		return map[string]interface{}{"intValue": strconv.FormatUint(v, 10)}
	case float32:
		return map[string]interface{}{"doubleValue": float64(v)}
	case float64:
		return map[string]interface{}{"doubleValue": v}
	case []interface{}:
		values := make([]interface{}, 0, len(v))
		for _, item := range v {
			values = append(values, ToOTLPAnyValue(item))
		}
		return map[string]interface{}{"arrayValue": map[string]interface{}{"values": values}}
	case []string:
		values := make([]interface{}, 0, len(v))
		for _, item := range v {
			values = append(values, ToOTLPAnyValue(item))
		}
		return map[string]interface{}{"arrayValue": map[string]interface{}{"values": values}}
	case map[string]interface{}:
		return map[string]interface{}{"kvlistValue": map[string]interface{}{"values": otlpKeyValues(v, "")}}
	case map[string]string:
		nested := make(map[string]interface{}, len(v))
		for k, item := range v {
			nested[k] = item
		}
		return map[string]interface{}{"kvlistValue": map[string]interface{}{"values": otlpKeyValues(nested, "")}}
	}
	// bytes / structs / anything else: fall back to the string form so a stray
	// payload value never breaks an export.
	return map[string]interface{}{"stringValue": fmt.Sprintf("%v", value)}
}

// ToOTLPKeyValue builds one OTLP KeyValue.
func ToOTLPKeyValue(key string, value interface{}) map[string]interface{} {
	return map[string]interface{}{"key": key, "value": ToOTLPAnyValue(value)}
}

// otlpKeyValues flattens a mapping into KeyValues, skipping nil values.
func otlpKeyValues(mapping map[string]interface{}, prefix string) []interface{} {
	out := make([]interface{}, 0, len(mapping))
	// Deterministic order keeps golden-file style assertions stable.
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		value := mapping[key]
		if value == nil {
			continue
		}
		out = append(out, ToOTLPKeyValue(prefix+key, value))
	}
	return out
}

// --------------------------------------------------------------------------- //
// Time
// --------------------------------------------------------------------------- //

// ParseTimestampNanos parses an RFC3339 / ISO8601 timestamp into epoch
// nanoseconds. Sub-nanosecond precision is truncated; a timestamp without an
// explicit offset is interpreted as UTC.
func ParseTimestampNanos(ts string) (int64, error) {
	normalized, err := normalizeTimestamp(ts)
	if err != nil {
		return 0, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, normalized)
	if err != nil {
		return 0, fmt.Errorf("agentlog: not an RFC3339 timestamp: %q", ts)
	}
	return parsed.UnixNano(), nil
}

// normalizeTimestamp rewrites a timestamp into the shape time.RFC3339Nano
// accepts: 'T' separator, at most nine fractional digits, explicit offset.
func normalizeTimestamp(ts string) (string, error) {
	s := strings.TrimSpace(ts)
	if len(s) < 19 {
		return "", fmt.Errorf("agentlog: not an RFC3339 timestamp: %q", ts)
	}
	if s[10] == ' ' {
		s = s[:10] + "T" + s[11:]
	}
	if s[10] != 'T' {
		return "", fmt.Errorf("agentlog: not an RFC3339 timestamp: %q", ts)
	}

	base := s
	offset := "Z"
	for i := len(s) - 1; i >= 19; i-- {
		switch s[i] {
		case 'Z', 'z':
			base, offset = s[:i], "Z"
			i = -1 // stop the loop
		case '+', '-':
			base, offset = s[:i], strings.ToUpper(s[i:])
			i = -1 // stop the loop
		}
	}
	// "+0800" -> "+08:00"
	if offset != "Z" && len(offset) == 5 && offset[3] != ':' {
		offset = offset[:3] + ":" + offset[3:]
	}
	// Truncate sub-nanosecond digits instead of failing the export.
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		frac := base[dot+1:]
		if len(frac) > 9 {
			frac = frac[:9]
		}
		base = base[:dot+1] + frac
	}
	return base + offset, nil
}

// --------------------------------------------------------------------------- //
// Ids
// --------------------------------------------------------------------------- //

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// NormalizeSpanID returns the lowercase 16-hex span id, or "" when the input is
// not a valid Schema v1.1 span_id (8 bytes, W3C traceparent shape).
func NormalizeSpanID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) != 16 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return ""
		}
	}
	return strings.ToLower(s)
}

// NormalizeTraceID returns the lowercase 32-hex trace id, or "" when the input
// is not a valid Schema v1.2 trace_id (8 bytes, W3C traceparent trace-id shape).
func NormalizeTraceID(s string) string {
	s = strings.TrimSpace(s)
	if len(s) != 32 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return ""
		}
	}
	return strings.ToLower(s)
}

// TraceIDHex returns the 32-hex OTLP traceId for an event, or "".
//
// Schema v1.2 constrains trace_id to 32 lowercase hex chars (uppercase is
// normalized). Non-conforming values yield "" — no hashing: since v1.2 the
// write boundary drops them, so reaching this adapter they can only come from
// hand-built maps; those are treated as trace-less too.
func TraceIDHex(event map[string]interface{}) string {
	return NormalizeTraceID(asString(event["trace_id"]))
}

// SpanIDHex returns the 16-hex OTLP spanId for an event.
//
// Since Schema v1.1 an event may carry its own span_id; a valid one wins so the
// log record and the span point at the same real span. Otherwise the id is
// derived deterministically from the event, keeping retries idempotent.
func SpanIDHex(event map[string]interface{}) string {
	if explicit := NormalizeSpanID(asString(event["span_id"])); explicit != "" {
		return explicit
	}
	seed := strings.Join([]string{
		"agentlog:span",
		asString(event["session_id"]),
		asString(event["trace_id"]),
		asString(event["event_type"]),
		asString(event["ts"]),
		asString(event["message"]),
	}, "|")
	return sha256Hex(seed)[:16]
}

// spanTraceID resolves the trace id for a span; events without trace_id fall
// back to a per-session id so one process yields one synthetic trace instead of
// a trace per span.
func spanTraceID(event map[string]interface{}) string {
	if resolved := TraceIDHex(event); resolved != "" {
		return resolved
	}
	return sha256Hex("agentlog:session:" + asString(event["session_id"]))[:32]
}

// --------------------------------------------------------------------------- //
// Level
// --------------------------------------------------------------------------- //

// LevelToSeverity maps an agent-log level to (severityText, severityNumber).
//
// The read-side alias "warning" is normalized to "warn"; an unknown level
// degrades to INFO rather than failing the whole export.
func LevelToSeverity(level string) (string, int) {
	canonical := LevelFromWire(strings.ToLower(strings.TrimSpace(level)))
	if sev, ok := severityByLevel[canonical]; ok {
		return sev.text, sev.number
	}
	fallback := severityByLevel[LevelInfo]
	return fallback.text, fallback.number
}

// --------------------------------------------------------------------------- //
// Resource / grouping
// --------------------------------------------------------------------------- //

// Resource attributes for one group. extraResource is appended after the
// built-in ones; keys already present are skipped so agent-log's own values win
// on conflict (as the spec requires).
func resourceAttributes(event map[string]interface{}, extraResource map[string]interface{}) []interface{} {
	service := asString(event["service"])
	if service == "" {
		service = "app"
	}
	attrs := []interface{}{ToOTLPKeyValue(semconvServiceName, service)}
	seen := map[string]bool{semconvServiceName: true}
	if env := asString(event["env"]); env != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvDeploymentEnv, env))
		seen[semconvDeploymentEnv] = true
	}
	if sessionID := asString(event["session_id"]); sessionID != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvSessionID, sessionID))
		seen[semconvSessionID] = true
	}
	for _, pair := range []struct{ key, value string }{
		{semconvTelemetrySDK, "agent-log"},
		{semconvTelemetryLang, "go"},
		{semconvTelemetryVer, SDKVersion},
	} {
		attrs = append(attrs, ToOTLPKeyValue(pair.key, pair.value))
		seen[pair.key] = true
	}
	if len(extraResource) > 0 {
		keys := make([]string, 0, len(extraResource))
		for key := range extraResource {
			if seen[key] || extraResource[key] == nil {
				continue
			}
			keys = append(keys, key)
		}
		sortStrings(keys)
		for _, key := range keys {
			attrs = append(attrs, ToOTLPKeyValue(key, extraResource[key]))
		}
	}
	return attrs
}

// otlpGroup is one (service, env, session_id) bucket, preserving input order.
type otlpGroup struct {
	events []map[string]interface{}
}

func groupByResource(events []interface{}) ([]otlpGroup, error) {
	groups := make([]otlpGroup, 0, len(events))
	index := map[string]int{}
	for _, raw := range events {
		event, err := normalizeOTLPEvent(raw)
		if err != nil {
			return nil, err
		}
		key := asString(event["service"]) + "\x00" + asString(event["env"]) + "\x00" + asString(event["session_id"])
		at, ok := index[key]
		if !ok {
			at = len(groups)
			index[key] = at
			groups = append(groups, otlpGroup{})
		}
		groups[at].events = append(groups[at].events, event)
	}
	return groups, nil
}

// --------------------------------------------------------------------------- //
// LogRecord
// --------------------------------------------------------------------------- //

// EventToLogRecord converts one Schema v1 event into an OTLP LogRecord.
func EventToLogRecord(raw interface{}) (map[string]interface{}, error) {
	event, err := normalizeOTLPEvent(raw)
	if err != nil {
		return nil, err
	}
	nanos, err := ParseTimestampNanos(asString(event["ts"]))
	if err != nil {
		return nil, err
	}
	severityText, severityNumber := LevelToSeverity(asString(event["level"]))
	timeNano := strconv.FormatInt(nanos, 10)
	record := map[string]interface{}{
		"timeUnixNano":         timeNano,
		"observedTimeUnixNano": timeNano,
		"severityNumber":       severityNumber,
		"severityText":         severityText,
		"body":                 map[string]interface{}{"stringValue": asString(event["message"])},
		"attributes":           eventAttributes(event),
	}
	if traceID := TraceIDHex(event); traceID != "" {
		record["traceId"] = traceID
		// Prefer the event's own span id: it points at a real span. Otherwise
		// only operation events get one, matching the span emitted for them, so
		// a log record never references a missing span.
		if explicit := NormalizeSpanID(asString(event["span_id"])); explicit != "" {
			record["spanId"] = explicit
		} else if spanKindOf(event) != 0 {
			record["spanId"] = SpanIDHex(event)
		}
	}
	return record, nil
}

// eventAttributes translates the non-resource event fields into attributes.
func eventAttributes(event map[string]interface{}) []interface{} {
	attrs := make([]interface{}, 0, 8)
	if eventType, ok := event["event_type"]; ok && eventType != nil {
		attrs = append(attrs, ToOTLPKeyValue(attrEventType, eventType))
	}
	attrs = append(attrs, ToOTLPKeyValue(attrAuto, asBool(event["agent_auto"])))
	if subsystem := asString(event["subsystem"]); subsystem != "" {
		attrs = append(attrs, ToOTLPKeyValue(attrSubsystem, subsystem))
	}
	if category := asString(event["category"]); category != "" {
		attrs = append(attrs, ToOTLPKeyValue(attrCategory, category))
	}
	if userID := asString(event["user_id"]); userID != "" {
		// Indirect identifier only — Schema v1 forbids direct PII here.
		attrs = append(attrs, ToOTLPKeyValue(semconvEnduserID, userID))
	}
	payload := asObject(event["payload"])
	attrs = append(attrs, otlpKeyValues(payload, payloadAttributePrefix)...)
	switch asString(event["event_type"]) {
	case string(EventTypeCrash), string(EventTypeHang):
		attrs = append(attrs, exceptionAttributes(payload)...)
	}
	return attrs
}

// exceptionAttributes maps crash/hang payload onto OTel exception.* attributes.
func exceptionAttributes(payload map[string]interface{}) []interface{} {
	attrs := make([]interface{}, 0, 3)
	if excType := firstString(payload, "exception", "exception_type"); excType != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvExceptionType, excType))
	}
	if excMessage := firstString(payload, "exception_message", "message"); excMessage != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvExceptionMsg, excMessage))
	}
	if stack := firstString(payload, "stack", "stacktrace", "trace"); stack != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvExceptionStack, stack))
	}
	return attrs
}

// --------------------------------------------------------------------------- //
// Spans
// --------------------------------------------------------------------------- //

// spanKindOf returns SpanKindClient for operation events, else 0.
func spanKindOf(event map[string]interface{}) int {
	switch asString(event["event_type"]) {
	case string(EventTypeNetworkRequest), string(EventTypeDBQuery):
		return SpanKindClient
	}
	return 0
}

// EventToSpan converts an operation event into an OTLP Span.
//
// The boolean is false for events that are not call spans (plain logs, UI
// events, crash/hang), which stay in the logs signal only.
func EventToSpan(raw interface{}) (map[string]interface{}, bool, error) {
	event, err := normalizeOTLPEvent(raw)
	if err != nil {
		return nil, false, err
	}
	kind := spanKindOf(event)
	if kind == 0 {
		return nil, false, nil
	}
	payload := asObject(event["payload"])

	var name string
	var attrs []interface{}
	var failed bool
	if asString(event["event_type"]) == string(EventTypeNetworkRequest) {
		name, attrs, failed = httpSpan(payload)
	} else {
		name, attrs, failed = dbSpan(payload)
	}

	start, err := ParseTimestampNanos(asString(event["ts"]))
	if err != nil {
		return nil, false, err
	}
	end := start + int64(durationMs(payload)*1e6)

	span := map[string]interface{}{
		"traceId":           spanTraceID(event),
		"spanId":            SpanIDHex(event),
		"name":              name,
		"kind":              kind,
		"startTimeUnixNano": strconv.FormatInt(start, 10),
		"endTimeUnixNano":   strconv.FormatInt(end, 10),
		"attributes":        attrs,
	}
	if failed {
		span["status"] = map[string]interface{}{"code": StatusCodeError, "message": name}
	}
	return span, true, nil
}

// httpSpan builds the HTTP-semconv span name/attributes and reports failure.
func httpSpan(payload map[string]interface{}) (string, []interface{}, bool) {
	method := firstString(payload, httpMethodKeys...)
	url := firstString(payload, urlKeys...)
	status, hasStatus := firstNumber(payload, statusKeys...)

	name := "HTTP " + url
	if method != "" {
		name = method + " " + url
	}

	attrs := make([]interface{}, 0, 4)
	if method != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvHTTPMethod, method))
	}
	if url != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvURLFull, url))
		if host := hostOf(url); host != "" {
			attrs = append(attrs, ToOTLPKeyValue(semconvServerAddress, host))
		}
	}
	if hasStatus {
		attrs = append(attrs, ToOTLPKeyValue(semconvHTTPStatus, int64(status)))
	}

	errorType := ""
	switch {
	case hasStatus && status >= 400:
		errorType = strconv.FormatInt(int64(status), 10)
	default:
		if raw, ok := payload["error"]; ok && raw != nil && asString(raw) != "" {
			errorType = asString(raw)
		}
	}
	if errorType != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvErrorType, errorType))
	}
	return name, attrs, errorType != ""
}

// dbSpan builds the Database-semconv span name/attributes and reports failure.
func dbSpan(payload map[string]interface{}) (string, []interface{}, bool) {
	operation := firstString(payload, dbOperationKeys...)
	statement := firstString(payload, sqlKeys...)
	system := firstString(payload, dbSystemKeys...)

	name := "SQL " + statement
	if operation != "" {
		name = operation + " " + statement
	}

	attrs := make([]interface{}, 0, 4)
	if system != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvDBSystem, system))
	}
	if operation != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvDBOperation, operation))
	}
	if statement != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvDBStatement, statement))
	}

	errorType := firstString(payload, "error", "error_type")
	if errorType != "" {
		attrs = append(attrs, ToOTLPKeyValue(semconvErrorType, errorType))
	}
	return name, attrs, errorType != ""
}

// hostOf extracts the host portion of an absolute URL.
func hostOf(url string) string {
	schemeEnd := strings.Index(url, "://")
	if schemeEnd <= 0 {
		return ""
	}
	rest := url[schemeEnd+3:]
	if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
		rest = rest[:cut]
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	if strings.HasPrefix(rest, "[") { // IPv6 literal
		if end := strings.Index(rest, "]"); end > 0 {
			return rest[1:end]
		}
	}
	if colon := strings.Index(rest, ":"); colon >= 0 {
		rest = rest[:colon]
	}
	return rest
}

// --------------------------------------------------------------------------- //
// Batch builders
// --------------------------------------------------------------------------- //

// ToOTLPLogs builds an OTLP/JSON ExportLogsServiceRequest body.
//
// extraResource adds resource attributes to every group (e.g.
// {"service.version": "2.1.0"}); agent-log's own fields win on conflict.
func ToOTLPLogs(events []interface{}, extraResource map[string]interface{}) (map[string]interface{}, error) {
	groups, err := groupByResource(events)
	if err != nil {
		return nil, err
	}
	resourceLogs := make([]interface{}, 0, len(groups))
	for _, group := range groups {
		records := make([]interface{}, 0, len(group.events))
		for _, event := range group.events {
			record, err := EventToLogRecord(event)
			if err != nil {
				return nil, err
			}
			records = append(records, record)
		}
		resourceLogs = append(resourceLogs, map[string]interface{}{
			"resource": map[string]interface{}{
				"attributes": resourceAttributes(group.events[0], extraResource),
			},
			"scopeLogs": []interface{}{
				map[string]interface{}{
					"scope":      map[string]interface{}{"name": otlpScopeName, "version": SDKVersion},
					"logRecords": records,
				},
			},
		})
	}
	return map[string]interface{}{"resourceLogs": resourceLogs}, nil
}

// ToOTLPSpans builds an OTLP/JSON ExportTraceServiceRequest body.
func ToOTLPSpans(events []interface{}, extraResource map[string]interface{}) (map[string]interface{}, error) {
	groups, err := groupByResource(events)
	if err != nil {
		return nil, err
	}
	resourceSpans := make([]interface{}, 0, len(groups))
	for _, group := range groups {
		spans := make([]interface{}, 0, len(group.events))
		for _, event := range group.events {
			span, ok, err := EventToSpan(event)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			spans = append(spans, span)
		}
		if len(spans) == 0 {
			continue
		}
		resourceSpans = append(resourceSpans, map[string]interface{}{
			"resource": map[string]interface{}{
				"attributes": resourceAttributes(group.events[0], extraResource),
			},
			"scopeSpans": []interface{}{
				map[string]interface{}{
					"scope": map[string]interface{}{"name": otlpScopeName, "version": SDKVersion},
					"spans": spans,
				},
			},
		})
	}
	return map[string]interface{}{"resourceSpans": resourceSpans}, nil
}

// ToOTLP builds both signals for one batch of events.
//
// The result is {"logs": <ExportLogsServiceRequest>, "traces": <...>}: OTLP
// transports one signal per request, so POST "logs" to /v1/logs and "traces" to
// /v1/traces.
func ToOTLP(events []interface{}, extraResource map[string]interface{}) (map[string]interface{}, error) {
	batch := make(map[string]interface{}, 2)
	logs, err := ToOTLPLogs(events, extraResource)
	if err != nil {
		return nil, err
	}
	traces, err := ToOTLPSpans(events, extraResource)
	if err != nil {
		return nil, err
	}
	batch["logs"] = logs
	batch["traces"] = traces
	return batch, nil
}

// ToOTLPJSON serializes an OTLP payload to compact JSON.
func ToOTLPJSON(payload interface{}) string {
	return MarshalJSON(payload)
}

// --------------------------------------------------------------------------- //
// Sink
// --------------------------------------------------------------------------- //

// OTLPSink collects serialized JSONL lines and renders them as OTLP batches.
//
//	otlp := NewOTLPSink(map[string]interface{}{"service.version": "1.2.3"})
//	logger := NewLogger("demo", otlp)   // or AgentLog.Shared().SetSink(otlp)
//	logger.Info("hello")
//	batch, err := otlp.Drain()
//	// POST ToOTLPJSON(batch["logs"]) to <collector>/v1/logs
//
// The sink stores lines exactly as written, so redaction and the min-level
// filter have already been applied by the logger.
type OTLPSink struct {
	mu            sync.Mutex
	lines         []string
	extraResource map[string]interface{}
}

// NewOTLPSink creates a buffering OTLP sink. extraResource may be nil.
func NewOTLPSink(extraResource map[string]interface{}) *OTLPSink {
	extra := make(map[string]interface{}, len(extraResource))
	for key, value := range extraResource {
		extra[key] = value
	}
	return &OTLPSink{extraResource: extra}
}

// Write buffers one JSONL line.
func (s *OTLPSink) Write(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, line)
	return nil
}

// Path returns a placeholder path (this sink writes no file).
func (s *OTLPSink) Path() string { return "<otlp>" }

// Lines returns a copy of the buffered lines.
func (s *OTLPSink) Lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.lines))
	copy(out, s.lines)
	return out
}

// Len returns the number of buffered lines.
func (s *OTLPSink) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.lines)
}

// Drain returns the OTLP batch for the buffered events and clears the buffer.
func (s *OTLPSink) Drain() (map[string]interface{}, error) {
	s.mu.Lock()
	lines := s.lines
	s.lines = nil
	extra := s.extraResource
	s.mu.Unlock()

	events := make([]interface{}, 0, len(lines))
	for _, line := range lines {
		events = append(events, line)
	}
	return ToOTLP(events, extra)
}

// --------------------------------------------------------------------------- //
// Small helpers
// --------------------------------------------------------------------------- //

func asString(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	}
	return fmt.Sprintf("%v", value)
}

func asBool(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	}
	return false
}

func asObject(value interface{}) map[string]interface{} {
	if obj, ok := value.(map[string]interface{}); ok {
		return obj
	}
	return map[string]interface{}{}
}

// firstString returns the first non-empty candidate among keys.
func firstString(payload map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if raw, ok := payload[key]; ok {
			if text := asString(raw); text != "" {
				return text
			}
		}
	}
	return ""
}

// firstNumber returns the first numeric candidate among keys.
func firstNumber(payload map[string]interface{}, keys ...string) (float64, bool) {
	for _, key := range keys {
		raw, ok := payload[key]
		if !ok || raw == nil {
			continue
		}
		if text, isText := raw.(string); isText && text == "" {
			continue
		}
		if number, ok := toFloat(raw); ok {
			return number, true
		}
	}
	return 0, false
}

func toFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return float64(parsed), true
		}
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func durationMs(payload map[string]interface{}) float64 {
	if value, ok := firstNumber(payload, durationKeys...); ok {
		return value
	}
	return 0
}

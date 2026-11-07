package agentlog

import "time"

// LogEvent is the internal representation of a structured log line.
// Serialization to the Schema v1 wire format happens in ToJSONLine.
type LogEvent struct {
	TS        string
	Level     Level
	Service   string
	EventType EventType
	SessionID string
	AgentAuto bool
	Message   string

	// Optional top-level fields (only emitted when non-empty).
	Subsystem string
	Category  string
	Env       string
	UserID    string
	TraceID   string
	SpanID    string

	// Payload holds all non-reserved fields, already redacted.
	Payload map[string]interface{}
}

// ToJSONLine serializes the event to a single JSONL line conforming to Schema v1.
// Required keys are always present; optional keys are emitted only when set.
// additionalProperties is false on the schema, so no other top-level keys exist.
func (e LogEvent) ToJSONLine() string {
	m := map[string]interface{}{
		"ts":         e.TS,
		"level":      string(e.Level),
		"service":    e.Service,
		"event_type": string(e.EventType),
		"session_id": e.SessionID,
		"agent_auto": e.AgentAuto,
		"message":    e.Message,
	}
	if e.Subsystem != "" {
		m["subsystem"] = e.Subsystem
	}
	if e.Category != "" {
		m["category"] = e.Category
	}
	if e.Env != "" {
		m["env"] = e.Env
	}
	if e.UserID != "" {
		m["user_id"] = e.UserID
	}
	// Schema v1.2: an invalid trace_id is dropped rather than written (the
	// event itself is kept), same policy as span_id below. Uppercase hex is
	// normalized to lowercase.
	if normalized := NormalizeTraceID(e.TraceID); normalized != "" {
		m["trace_id"] = normalized
	}
	// Schema v1.1: an invalid span_id is dropped rather than written, so a bad
	// value can never produce a line that violates the schema.
	if normalized := NormalizeSpanID(e.SpanID); normalized != "" {
		m["span_id"] = normalized
	}
	if len(e.Payload) > 0 {
		m["payload"] = e.Payload
	}
	return MarshalJSON(m)
}

// newTimestamp returns the current time formatted as RFC3339 / ISO8601 with
// nanosecond precision and a 'Z' timezone designator.
func newTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

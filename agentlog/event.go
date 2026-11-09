package agentlog

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime"
	"time"
)

// EventType is the semantic name of an event (snake_case).
type EventType string

const (
	EventTypeScreenView     EventType = "screen_view"
	EventTypeUIInteraction  EventType = "ui_interaction"
	EventTypeNetworkRequest EventType = "network_request"
	EventTypeCrash          EventType = "crash"
	EventTypeHang           EventType = "hang"
	EventTypeDBQuery        EventType = "db_query"
	EventTypeLog            EventType = "log"
)

// eventTypePattern matches the schema regex ^[a-z][a-z0-9_]*$
func isValidEventType(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// globalSessionID is a stable per-process session identifier, generated once
// at package init (see init() below).
var globalSessionID string

func init() {
	globalSessionID = newHex32()
}

// SessionID returns the process-wide stable session id.
//
// Generated ids are 32 lowercase hex chars, so the natural binding
// trace_id = session_id is always a Schema v1.2-valid trace id.
func SessionID() string {
	return globalSessionID
}

// NewTraceID returns a fresh Schema v1.2 trace id: 32 lowercase hex chars
// (crypto/rand, uuid4-shaped without hyphens).
func NewTraceID() string {
	return newHex32()
}

// newHex32 returns 16 random bytes as 32 lowercase hex chars.
func newHex32() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// extremely unlikely fallback
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// InstallPanicHandler runs fn and, should it panic, records a crash auto-event
// via the shared logger's RecordAuto before re-panicking is avoided (the panic is
// recovered and reported, not re-raised, so the surrounding program keeps running).
func InstallPanicHandler(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 8192)
			n := runtime.Stack(buf, false)
			RecordAutoCrash(r, buf[:n])
		}
	}()
	fn()
}

// RecordAutoCrash emits a "crash" auto-event describing a recovered panic.
func RecordAutoCrash(recovered interface{}, stack []byte) {
	AgentLog.RecordAuto(string(EventTypeCrash), "panic recovered",
		WithPayload(map[string]interface{}{
			"thread":            "main",
			"exception":         fmt.Sprintf("%v", recovered),
			"exception_message": fmt.Sprintf("%v", recovered),
			"stack":             string(stack),
			"mechanism":         "uncaught",
		}),
	)
}

// InstallCrashHandler installs a panic wrapper note. Go cannot globally intercept
// all panics, but wrapping entrypoints with InstallPanicHandler covers the common
// case. This helper is provided for API symmetry with sibling implementations.
func InstallCrashHandler() {
	// Note: Go has no global panic hook. Wrap your main/goroutine entrypoints
	// with InstallPanicHandler to capture crashes as auto-events.
	_ = runtime.Stack
}

// statusRecorder captures the HTTP response status code for auto-instrumentation.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	return s.ResponseWriter.Write(b)
}

// HTTPMiddleware is a net/http middleware that records a network_request auto-event
// for every handled request. This is the most natural Go auto-instrumentation.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		dur := time.Since(start)
		AgentLog.RecordAuto(string(EventTypeNetworkRequest),
			fmt.Sprintf("%s %s -> %d", r.Method, r.URL.String(), rec.status),
			WithPayload(map[string]interface{}{
				"method":      r.Method,
				"path":        r.URL.Path,
				"host":        r.Host,
				"status":      rec.status,
				"duration_ms": dur.Milliseconds(),
			}),
		)
	})
}

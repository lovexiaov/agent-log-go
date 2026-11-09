package agentlog

// Logger is the core logging type. It holds service identity, the minimum level,
// the sink, the redactor, and a bound (scope) context.
type Logger struct {
	service       string
	minLevel      Level
	redactEnabled bool
	sink          Sink
	redactor      *Redactor
	sessionID     string
	subsystem     string
	env           string
	release       string
	category      string
	traceID       string
	spanID        string
	userID        string
	boundPayload  map[string]interface{}
}

// NewLogger builds a Logger writing to the given sink. minLevel defaults to info
// and redaction is enabled.
func NewLogger(service string, sink Sink) *Logger {
	if service == "" {
		service = "app"
	}
	if sink == nil {
		sink = NewMemorySink()
	}
	return &Logger{
		service:       service,
		minLevel:      LevelInfo,
		redactEnabled: true,
		sink:          sink,
		redactor:      NewRedactor(true, nil),
		sessionID:     globalSessionID,
		boundPayload:  map[string]interface{}{},
	}
}

// emitInput is the per-call description of an event before the bound context is
// applied. Options populate it.
type emitInput struct {
	level     Level
	eventType EventType
	agentAuto bool
	message   string
	category  string
	traceID   string
	spanID    string
	userID    string
	subsystem string
	env       string
	payload   map[string]interface{}
}

// Option customizes a single emitted event.
type Option func(*emitInput)

// WithCategory sets the business category (defaults: "app" manual, "auto" auto).
func WithCategory(c string) Option { return func(e *emitInput) { e.category = c } }

// WithTraceID sets a cross-request trace id.
func WithTraceID(t string) Option { return func(e *emitInput) { e.traceID = t } }

// WithSpanID sets the span this event belongs to (Schema v1.1: 16 hex chars).
// Invalid values are ignored at serialization time.
func WithSpanID(id string) Option { return func(e *emitInput) { e.spanID = id } }

// WithUserID binds a user id to the event (lifted to user_id at serialization).
func WithUserID(u string) Option { return func(e *emitInput) { e.userID = u } }

// WithSubsystem sets the subsystem identifier.
func WithSubsystem(s string) Option { return func(e *emitInput) { e.subsystem = s } }

// WithEnv sets the running environment.
func WithEnv(env string) Option { return func(e *emitInput) { e.env = env } }

// WithPayload attaches a payload object (all non-reserved fields).
func WithPayload(p map[string]interface{}) Option { return func(e *emitInput) { e.payload = p } }

// emit builds the LogEvent, applies minLevel filtering and redaction, then writes.
func (l *Logger) emit(in emitInput) {
	// Resolve effective fields: per-call options override bound context.
	userID := firstNonEmpty(in.userID, l.userID)
	subsystem := firstNonEmpty(in.subsystem, l.subsystem)
	env := firstNonEmpty(in.env, l.env)
	category := firstNonEmpty(in.category, l.category)
	traceID := firstNonEmpty(in.traceID, l.traceID)
	spanID := firstNonEmpty(in.spanID, l.spanID)

	// Merge payloads: bound context first, per-call overrides.
	payload := map[string]interface{}{}
	for k, v := range l.boundPayload {
		payload[k] = v
	}
	if in.payload != nil {
		for k, v := range in.payload {
			payload[k] = v
		}
	}

	// minLevel filtering applies only to manual (agent_auto=false) events.
	if !in.agentAuto && in.level.Weight() < l.minLevel.Weight() {
		return
	}

	ev := LogEvent{
		TS:        newTimestamp(),
		Level:     in.level,
		Service:   l.service,
		EventType: in.eventType,
		SessionID: l.sessionID,
		AgentAuto: in.agentAuto,
		Message:   in.message,
		Subsystem: subsystem,
		Category:  category,
		Env:       env,
		Release:   l.release,
		UserID:    userID,
		TraceID:   traceID,
		SpanID:    spanID,
		Payload:   payload,
	}

	if l.redactEnabled {
		l.redactor.RedactEvent(&ev)
	}

	_ = l.sink.Write(ev.ToJSONLine())
}

// Child returns a NEW logger that merges ctx into the bound scope context.
// The parent logger is never mutated.
func (l *Logger) Child(ctx map[string]interface{}) *Logger {
	child := *l
	child.redactor = l.redactor.Clone() // isolate redact paths from parent
	child.boundPayload = make(map[string]interface{}, len(l.boundPayload))
	for k, v := range l.boundPayload {
		child.boundPayload[k] = v
	}
	for k, v := range ctx {
		sk := normalizeReservedKey(k)
		switch sk {
		case "user_id":
			child.userID = toString(v)
		case "subsystem":
			child.subsystem = toString(v)
		case "env":
			child.env = toString(v)
		case "category":
			child.category = toString(v)
		case "trace_id":
			child.traceID = toString(v)
		case "span_id":
			child.spanID = toString(v)
		default:
			child.boundPayload[sk] = v
		}
	}
	return &child
}

// SetLevel sets the minimum output level (manual events only; auto always logged).
func (l *Logger) SetLevel(level Level) {
	l.minLevel = level
}

// SetRelease sets the release identifier stamped on every subsequent event
// (Schema v1.3). An empty value disables the release field.
func (l *Logger) SetRelease(release string) {
	l.release = release
}

// Redact appends sensitive paths to the redactor. Default keys are never removed.
func (l *Logger) Redact(paths []string) {
	l.redactor.Append(paths)
}

// SessionID returns the logger's (process-wide) session id.
func (l *Logger) SessionID() string {
	return l.sessionID
}

// SetSink swaps the destination of already-redacted JSONL lines, e.g. to route
// the shared logger through an OTLPSink. A nil sink is ignored.
func (l *Logger) SetSink(sink Sink) {
	if sink != nil {
		l.sink = sink
	}
}

// ExportPath returns the sink's path.
func (l *Logger) ExportPath() string {
	return l.sink.Path()
}

// --- Convenience manual helpers (agent_auto=false) ---

func (l *Logger) Trace(msg string, opts ...Option)    { l.log(LevelTrace, msg, opts...) }
func (l *Logger) Debug(msg string, opts ...Option)    { l.log(LevelDebug, msg, opts...) }
func (l *Logger) Info(msg string, opts ...Option)     { l.log(LevelInfo, msg, opts...) }
func (l *Logger) Warn(msg string, opts ...Option)     { l.log(LevelWarn, msg, opts...) }
func (l *Logger) Error(msg string, opts ...Option)    { l.log(LevelError, msg, opts...) }
func (l *Logger) Critical(msg string, opts ...Option) { l.log(LevelCritical, msg, opts...) }

func (l *Logger) log(level Level, msg string, opts ...Option) {
	in := emitInput{
		level:     level,
		eventType: EventTypeLog,
		agentAuto: false,
		message:   msg,
		category:  "app",
	}
	for _, o := range opts {
		o(&in)
	}
	l.emit(in)
}

// RecordAuto emits an auto-instrumented event (agent_auto=true, always logged).
func (l *Logger) RecordAuto(eventType, message string, opts ...Option) {
	in := emitInput{
		level:     LevelInfo,
		eventType: EventType(eventType),
		agentAuto: true,
		message:   message,
		category:  "auto",
	}
	for _, o := range opts {
		o(&in)
	}
	l.emit(in)
}

// --- helpers ---

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// normalizeReservedKey maps camelCase reserved keys to snake_case, passing other
// keys through unchanged (assumed already snake_case).
func normalizeReservedKey(k string) string {
	switch k {
	case "userId":
		return "user_id"
	case "sessionId":
		return "session_id"
	case "traceId":
		return "trace_id"
	case "spanId":
		return "span_id"
	case "event":
		return "event_type"
	case "category":
		return "category"
	case "subsystem":
		return "subsystem"
	case "env":
		return "env"
	}
	return k
}

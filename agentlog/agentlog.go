// Package agentlog exposes a language-neutral logging facade (AgentLog) plus the
// underlying Logger type. The functional surface matches the sibling Kotlin,
// Swift, TypeScript, Java and Python implementations.
package agentlog

// shared is the process-wide logger set by Bootstrap.
var shared *Logger

// ensureShared lazily initializes the shared logger with defaults.
func ensureShared() *Logger {
	if shared == nil {
		Bootstrap(DefaultLoggingConfig())
	}
	return shared
}

// AgentLog is the package-level facade matching the contract:
// Bootstrap, Info/Warn/Error/Debug/Trace/Critical, Child, SetLevel, Redact,
// RecordAuto, RecordAI* (Schema v1.4), ExportPath.
type AgentLogFacade struct{}

// AgentLog is the singleton facade instance.
var AgentLog AgentLogFacade

// Bootstrap initializes the shared logger from a LoggingConfig.
func (AgentLogFacade) Bootstrap(config LoggingConfig) {
	Bootstrap(config)
}

// Bootstrap initializes the process-wide shared logger.
func Bootstrap(config LoggingConfig) {
	if config.Service == "" {
		config.Service = "app"
	}
	if config.ExportDirectory == "" {
		config.ExportDirectory = defaultExportDir()
	}
	if config.Level == "" {
		config.Level = LevelInfo
	}
	redactor := NewRedactor(config.RedactEnabled, config.RedactPaths)
	sink := NewFileSink(config.ExportDirectory)
	logger := &Logger{
		service:       config.Service,
		minLevel:      config.Level,
		redactEnabled: config.RedactEnabled,
		sink:          sink,
		redactor:      redactor,
		sessionID:     globalSessionID,
		boundPayload:  map[string]interface{}{},
	}
	if config.Env != "" {
		logger.env = config.Env
	}
	if config.Release != "" {
		logger.release = config.Release
	}
	shared = logger
}

// Shared returns the underlying shared logger (initializing it if needed).
func (AgentLogFacade) Shared() *Logger {
	return ensureShared()
}

func (AgentLogFacade) Trace(msg string, opts ...Option)    { ensureShared().Trace(msg, opts...) }
func (AgentLogFacade) Debug(msg string, opts ...Option)    { ensureShared().Debug(msg, opts...) }
func (AgentLogFacade) Info(msg string, opts ...Option)     { ensureShared().Info(msg, opts...) }
func (AgentLogFacade) Warn(msg string, opts ...Option)     { ensureShared().Warn(msg, opts...) }
func (AgentLogFacade) Error(msg string, opts ...Option)    { ensureShared().Error(msg, opts...) }
func (AgentLogFacade) Critical(msg string, opts ...Option) { ensureShared().Critical(msg, opts...) }

// Child returns a child logger that merges ctx into the bound scope context.
func (AgentLogFacade) Child(ctx map[string]interface{}) *Logger {
	return ensureShared().Child(ctx)
}

// SetLevel sets the minimum output level on the shared logger.
func (AgentLogFacade) SetLevel(level Level) {
	ensureShared().SetLevel(level)
}

// Redact appends sensitive paths to the shared logger's redactor.
func (AgentLogFacade) Redact(paths []string) {
	ensureShared().Redact(paths)
}

// RecordAuto emits an auto-instrumented event on the shared logger.
func (AgentLogFacade) RecordAuto(eventType, message string, opts ...Option) {
	ensureShared().RecordAuto(eventType, message, opts...)
}

// ---------------------------------------------------------------- AI events
//
// Schema v1.4: the six standardized AI interaction events. These are explicit,
// business-side calls (agent_auto=false); the framework only prepares the data.

func (AgentLogFacade) RecordAIRequest(model, promptText string, extra map[string]interface{}) {
	ensureShared().RecordAIRequest(model, promptText, extra)
}

func (AgentLogFacade) RecordAIResponse(model string, totalTokens int, extra map[string]interface{}) {
	ensureShared().RecordAIResponse(model, totalTokens, extra)
}

func (AgentLogFacade) RecordAIToolCall(toolName string, toolArguments interface{}, extra map[string]interface{}) {
	ensureShared().RecordAIToolCall(toolName, toolArguments, extra)
}

func (AgentLogFacade) RecordAIToolResult(toolName string, ok bool, extra map[string]interface{}) {
	ensureShared().RecordAIToolResult(toolName, ok, extra)
}

func (AgentLogFacade) RecordAIGuardrail(guardrailType, reason string, extra map[string]interface{}) {
	ensureShared().RecordAIGuardrail(guardrailType, reason, extra)
}

func (AgentLogFacade) RecordAIError(errorType, message string, extra map[string]interface{}) {
	ensureShared().RecordAIError(errorType, message, extra)
}

// ExportPath returns the shared logger's sink path.
func (AgentLogFacade) ExportPath() string {
	return ensureShared().ExportPath()
}

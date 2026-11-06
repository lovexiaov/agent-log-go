package agentlog

// Level is a logging severity. Wire values are always lowercase.
type Level string

const (
	LevelTrace    Level = "trace"
	LevelDebug    Level = "debug"
	LevelInfo     Level = "info"
	LevelWarn     Level = "warn"
	LevelError    Level = "error"
	LevelCritical Level = "critical"
)

// levelWeights assigns a numeric weight to each level for minLevel filtering.
var levelWeights = map[Level]int{
	LevelTrace:    0,
	LevelDebug:    1,
	LevelInfo:     2,
	LevelWarn:     3,
	LevelError:    4,
	LevelCritical: 5,
}

// Weight returns the numeric weight of the level. Unknown levels sort last.
func (l Level) Weight() int {
	if w, ok := levelWeights[l]; ok {
		return w
	}
	return 99
}

// LevelFromName normalizes a (possibly mixed-case) level name to a wire level.
// "warning" normalizes to "warn". Unknown input defaults to "info".
func LevelFromName(name string) Level {
	switch name {
	case "trace", "TRACE", "Trace":
		return LevelTrace
	case "debug", "DEBUG", "Debug":
		return LevelDebug
	case "info", "INFO", "Info":
		return LevelInfo
	case "warn", "warning", "WARN", "WARNING", "Warn", "Warning":
		return LevelWarn
	case "error", "ERROR", "Error":
		return LevelError
	case "critical", "CRITICAL", "Critical":
		return LevelCritical
	}
	return LevelInfo
}

// LevelFromWire maps a known wire level value. "warning" (a read-only alias)
// normalizes to "warn". Unknown input defaults to "info".
func LevelFromWire(name string) Level {
	switch name {
	case "trace":
		return LevelTrace
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	case "critical":
		return LevelCritical
	}
	return LevelInfo
}

// IsValidWire reports whether l is one of the six canonical wire levels.
func (l Level) IsValidWire() bool {
	switch l {
	case LevelTrace, LevelDebug, LevelInfo, LevelWarn, LevelError, LevelCritical:
		return true
	}
	return false
}

package agentlog

import (
	"fmt"
	"os"
)

// LoggingConfig is the language-neutral configuration. It is parsed from a subset
// of config/logging.config.json using the hand-rolled JSON parser.
type LoggingConfig struct {
	Service         string
	Env             string
	Level           Level
	RedactEnabled   bool
	RedactPaths     []string
	ExportDirectory string
}

// DefaultLoggingConfig returns the configuration with all defaults applied.
func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{
		Service:         "app",
		Level:           LevelInfo,
		RedactEnabled:   true,
		RedactPaths:     []string{},
		ExportDirectory: defaultExportDir(),
	}
}

// FromJSON parses a JSON string and merges recognized keys over the receiver's
// current values. It returns the resulting config (the receiver is also updated).
// This implements the language-neutral config contract without a third-party
// JSON library.
func (c LoggingConfig) FromJSON(s string) (LoggingConfig, error) {
	v, err := ParseJSON(s)
	if err != nil {
		return c, err
	}
	obj, ok := v.(map[string]interface{})
	if !ok {
		return c, fmt.Errorf("agentlog: config root must be a JSON object")
	}

	cfg := DefaultLoggingConfig()
	// Seed from the receiver so callers can pre-set values.
	if c.Service != "" {
		cfg.Service = c.Service
	}
	if c.Env != "" {
		cfg.Env = c.Env
	}
	if c.Level != "" {
		cfg.Level = c.Level
	}
	if c.ExportDirectory != "" {
		cfg.ExportDirectory = c.ExportDirectory
	}

	if sv, ok := obj["service"].(string); ok && sv != "" {
		cfg.Service = sv
	}
	if ev, ok := obj["env"].(string); ok {
		cfg.Env = ev
	}
	if lv, ok := obj["level"].(string); ok && lv != "" {
		cfg.Level = LevelFromName(lv)
	}
	if rv, ok := obj["redactEnabled"].(bool); ok {
		cfg.RedactEnabled = rv
	}
	if ed, ok := obj["exportDirectory"].(string); ok && ed != "" {
		cfg.ExportDirectory = ed
	}
	if rp, ok := obj["redactPaths"].([]interface{}); ok {
		for _, p := range rp {
			if ps, ok := p.(string); ok {
				cfg.RedactPaths = append(cfg.RedactPaths, ps)
			}
		}
	}
	return cfg, nil
}

// LoadConfigFile reads a JSON config file and returns the parsed LoggingConfig.
func LoadConfigFile(path string) (LoggingConfig, error) {
	data, err := readFile(path)
	if err != nil {
		return LoggingConfig{}, err
	}
	return DefaultLoggingConfig().FromJSON(string(data))
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

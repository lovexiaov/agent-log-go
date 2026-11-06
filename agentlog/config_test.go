package agentlog

import "testing"

func TestConfigDefaults(t *testing.T) {
	cfg := DefaultLoggingConfig()
	if cfg.Service != "app" {
		t.Fatalf("default service = %q", cfg.Service)
	}
	if cfg.Level != LevelInfo {
		t.Fatalf("default level = %v", cfg.Level)
	}
	if !cfg.RedactEnabled {
		t.Fatalf("default redactEnabled should be true")
	}
	if cfg.ExportDirectory == "" {
		t.Fatalf("default exportDirectory should not be empty")
	}
}

func TestConfigFromJSON(t *testing.T) {
	jsonStr := `{
		"service": "com.example.agent",
		"env": "prod",
		"level": "debug",
		"redactEnabled": true,
		"redactPaths": ["custom_key", "sessionToken"],
		"exportDirectory": "/tmp/agent-log-test"
	}`
	cfg, err := DefaultLoggingConfig().FromJSON(jsonStr)
	if err != nil {
		t.Fatalf("FromJSON error: %v", err)
	}
	if cfg.Service != "com.example.agent" {
		t.Fatalf("service = %q", cfg.Service)
	}
	if cfg.Env != "prod" {
		t.Fatalf("env = %q", cfg.Env)
	}
	if cfg.Level != LevelDebug {
		t.Fatalf("level = %v", cfg.Level)
	}
	if !cfg.RedactEnabled {
		t.Fatalf("redactEnabled = %v", cfg.RedactEnabled)
	}
	if cfg.ExportDirectory != "/tmp/agent-log-test" {
		t.Fatalf("exportDirectory = %q", cfg.ExportDirectory)
	}
	if len(cfg.RedactPaths) != 2 {
		t.Fatalf("redactPaths = %#v", cfg.RedactPaths)
	}
}

func TestConfigFromJSONPartialOverride(t *testing.T) {
	// Only override service; everything else should fall back to defaults.
	cfg, err := DefaultLoggingConfig().FromJSON(`{"service":"only-service"}`)
	if err != nil {
		t.Fatalf("FromJSON error: %v", err)
	}
	if cfg.Service != "only-service" {
		t.Fatalf("service = %q", cfg.Service)
	}
	if cfg.Level != LevelInfo {
		t.Fatalf("level default not applied: %v", cfg.Level)
	}
	if !cfg.RedactEnabled {
		t.Fatalf("redactEnabled default not applied")
	}
	// warning alias should normalize to warn
	cfg2, err := DefaultLoggingConfig().FromJSON(`{"level":"warning"}`)
	if err != nil {
		t.Fatalf("FromJSON error: %v", err)
	}
	if cfg2.Level != LevelWarn {
		t.Fatalf("warning should normalize to warn, got %v", cfg2.Level)
	}
}

func TestConfigFromJSONInvalid(t *testing.T) {
	if _, err := DefaultLoggingConfig().FromJSON(`not json`); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestParserRoundTrip(t *testing.T) {
	// Exercise the hand-rolled parser with nested structures.
	s := `{"a":1,"b":2.5,"c":true,"d":null,"e":["x",{"f":3}],"g":"he said \"hi\""}`
	v, err := ParseJSON(s)
	if err != nil {
		t.Fatalf("ParseJSON error: %v", err)
	}
	obj := v.(map[string]interface{})
	if obj["a"].(int64) != 1 {
		t.Fatalf("a = %v", obj["a"])
	}
	if obj["b"].(float64) != 2.5 {
		t.Fatalf("b = %v", obj["b"])
	}
	if obj["c"].(bool) != true {
		t.Fatalf("c = %v", obj["c"])
	}
	if obj["d"] != nil {
		t.Fatalf("d = %v", obj["d"])
	}
	arr := obj["e"].([]interface{})
	if arr[0].(string) != "x" {
		t.Fatalf("e[0] = %v", arr[0])
	}
	if obj["g"].(string) != `he said "hi"` {
		t.Fatalf("g = %v", obj["g"])
	}
}

// Command example demonstrates the AgentLog Go logging framework: bootstrap,
// manual logs, child loggers, redaction, and HTTP middleware auto-instrumentation.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	agentlog "github.com/lovexiaov/agent-log-go/agentlog"
)

func main() {
	// 1) Bootstrap from config (or defaults).
	cfg := agentlog.DefaultLoggingConfig()
	cfg.Service = "com.example.demo"
	cfg.Env = "dev"
	cfg.Level = agentlog.LevelDebug
	agentlog.AgentLog.Bootstrap(cfg)

	fmt.Println("export path:", agentlog.AgentLog.ExportPath())

	// 2) Manual logs at various levels.
	agentlog.AgentLog.Info("application started")
	agentlog.AgentLog.Debug("debug detail", agentlog.WithTraceID("trace_abc"))
	agentlog.AgentLog.Warn("low disk space", agentlog.WithPayload(map[string]interface{}{"free_mb": 42}))
	agentlog.AgentLog.Error("failed to connect", agentlog.WithPayload(map[string]interface{}{"host": "db.example.com"}))

	// 3) Child logger: merges scope context (user_id) without mutating parent.
	userLogger := agentlog.AgentLog.Child(map[string]interface{}{"user_id": "u-1234", "subsystem": "auth"})
	userLogger.Info("user performed login", agentlog.WithPayload(map[string]interface{}{"method": "oauth"}))

	// 4) Redaction: append-only. Sensitive keys are masked automatically.
	agentlog.AgentLog.Redact([]string{"sessionToken"})
	agentlog.AgentLog.Info("login payload", agentlog.WithPayload(map[string]interface{}{
		"password":     "hunter2",
		"sessionToken": "topsecret",
		"username":     "alice",
	}))

	// 5) Auto-instrumentation: net/http middleware records network_request events.
	mw := agentlog.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest("GET", "http://example.com/api/orders", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	fmt.Println("handled request, status:", rec.Code)

	// 6) Crash auto-instrumentation via panic handler.
	agentlog.InstallPanicHandler(func() {
		panic("demo boom")
	})

	fmt.Println("done; see", agentlog.AgentLog.ExportPath())
}

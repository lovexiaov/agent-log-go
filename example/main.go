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

	// 7) DB auto-instrumentation (event_type = db_query). In production wrap your
	//    *sql.DB with agentlog.WrapDB(db) so every Query/Exec is recorded; here we
	//    emit one directly with no real database.
	agentlog.RecordDBQuery("exec", "INSERT INTO orders(user_id) VALUES ('u-1234')", 3, 1, nil)

	// 8) OTLP export (D3 / OTel alignment): the adapter translates Schema v1
	//    events into OTLP/JSON — one LogRecord each, plus a CLIENT span for
	//    network_request / db_query. Wire it as a sink to capture events in
	//    memory, then drain the batch and POST it to an OTLP collector.
	otlpSink := agentlog.NewOTLPSink(map[string]interface{}{"service.version": "1.0.0"})
	otlpLogger := agentlog.NewLogger("com.example.demo", otlpSink)
	otlpLogger.Info("invoice generated", agentlog.WithPayload(map[string]interface{}{"invoice_id": "INV-9"}))
	otlpLogger.RecordAuto(string(agentlog.EventTypeNetworkRequest), "GET https://api.example.com/orders -> 200",
		agentlog.WithPayload(map[string]interface{}{
			"method": "GET", "url": "https://api.example.com/orders", "status": 200, "duration_ms": 14,
		}))
	otlpLogger.RecordAuto(string(agentlog.EventTypeDBQuery), "SELECT: orders",
		agentlog.WithPayload(map[string]interface{}{
			"operation": "SELECT", "statement": "SELECT * FROM orders WHERE id = ?",
			"db_system": "postgresql", "duration_ms": 3,
		}))

	batch, err := otlpSink.Drain()
	if err != nil {
		fmt.Println("otlp drain error:", err)
		return
	}
	logsBody := agentlog.ToOTLPJSON(batch["logs"])
	tracesBody := agentlog.ToOTLPJSON(batch["traces"])
	fmt.Printf("OTLP logs body (%d bytes): %.160s...\n", len(logsBody), logsBody)
	fmt.Printf("OTLP traces body (%d bytes): %.160s...\n", len(tracesBody), tracesBody)

	fmt.Println("done; see", agentlog.AgentLog.ExportPath())
}

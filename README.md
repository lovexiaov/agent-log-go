# agent-log-go

Language-neutral **AgentLog** logging framework for Go — conforming to **Schema v1** (JSONL wire format).
Standard-library only (no third-party dependencies). A sibling of the Kotlin (`agent-log-android`),
Swift (`agent-log-macos`), TypeScript (`agent-log-web`) and Java/Python implementations.

Every emitted line validates against [`docs/schema/log-event.schema.json`](docs/schema/log-event.schema.json)
(`additionalProperties: false`, snake_case, required keys present).

## Install

```sh
go get github.com/lovexiaov/agent-log-go
```

## Quickstart

```go
package main

import (
    agentlog "github.com/lovexiaov/agent-log-go/agentlog"
)

func main() {
    // Bootstrap once with a config (defaults applied for omitted fields).
    cfg := agentlog.DefaultLoggingConfig()
    cfg.Service = "com.example.app"
    cfg.Env = "prod"
    cfg.Level = agentlog.LevelInfo
    agentlog.AgentLog.Bootstrap(cfg)

    // Manual logs (agent_auto = false, filtered by minLevel).
    agentlog.AgentLog.Info("app started")
    agentlog.AgentLog.Warn("low memory", agentlog.WithPayload(map[string]interface{}{"free_mb": 12}))
    agentlog.AgentLog.Error("boom", agentlog.WithTraceID("trace_9"))

    // Child logger: merges scope context, never mutates the parent.
    user := agentlog.AgentLog.Child(map[string]interface{}{"user_id": "u-1", "subsystem": "auth"})
    user.Info("login", agentlog.WithPayload(map[string]interface{}{"method": "oauth"}))

    // Redaction: APPEND-only; default sensitive keys can never be removed.
    agentlog.AgentLog.Redact([]string{"sessionToken"})
    agentlog.AgentLog.Info("creds", agentlog.WithPayload(map[string]interface{}{
        "password": "hunter2", // -> "***REDACTED***"
        "username": "alice",   // untouched
    }))

    // Where the JSONL is written.
    _ = agentlog.AgentLog.ExportPath()
}
```

### Levels

`trace | debug | info | warn | error | critical` (6, lowercase). `warning` is a read-only
alias normalized to `warn`; it is **never** written to the wire.

### Field mapping

Reserved keys lifted from context/event: `userId→user_id`, `sessionId→session_id`,
`traceId→trace_id`, `event→event_type`, `category`, `subsystem`, `env`. Every other key goes
into `payload`. Internal code may use camelCase; mapping to snake_case happens only at the
serialization boundary.

## Auto-instrumentation

`agent_auto=true` events are **always** logged (never filtered by `minLevel`).

### Database (`WrapDB`)

Wrap `*sql.DB` so every `Query`/`QueryRow`/`Exec` (and their `*Context` variants)
is recorded as a `db_query` auto-event — no instrumentation code at call sites:

```go
db := agentlog.WrapDB(rawDB) // use db in place of rawDB everywhere
rows, _ := db.Query("SELECT 1")
```

### Crash / panic handling

```go
agentlog.InstallPanicHandler(func() {
    // risky work; a panic records a `crash` auto-event (thread/exception/stack)
})
```

### Manual auto event

```go
agentlog.AgentLog.RecordAuto(agentlog.EventTypeNetworkRequest, "GET /x -> 200",
    agentlog.WithPayload(map[string]interface{}{"host": "api", "status": 200}))
```

## Sinks

- `FileSink` — appends JSONL to `<exportDirectory>/agent-log.jsonl` (default: `<tmpdir>/agent-log`).
- `MemorySink` — in-memory slice, for tests (`Lines()`, `LineCount()`).
- `OTLPSink` — buffers lines and renders them as OTLP/JSON batches (see below).

## OTLP export (OpenTelemetry)

`agentlog/otlp.go` translates Schema v1 events into **OTLP/JSON**, so any
OTLP-compatible backend (Jaeger / Tempo / Loki / Grafana / Datadog …) can ingest
agent-log output through a Collector. It is export-only: the wire format is
unchanged and no OTel SDK is pulled in — the package stays standard-library only.

```go
sink := agentlog.NewOTLPSink(map[string]interface{}{"service.version": "1.2.3"})
logger := agentlog.NewLogger("com.example.demo", sink)
// (or route the shared logger: agentlog.AgentLog.Shared().SetSink(sink))

logger.Info("invoice generated", agentlog.WithPayload(map[string]interface{}{"invoice_id": "INV-9"}))
logger.RecordAuto(string(agentlog.EventTypeNetworkRequest), "GET https://api.example.com/orders -> 200",
    agentlog.WithPayload(map[string]interface{}{
        "method": "GET", "url": "https://api.example.com/orders", "status": 200, "duration_ms": 14,
    }))

batch, err := sink.Drain()                    // {"logs": ..., "traces": ...}
// POST agentlog.ToOTLPJSON(batch["logs"])   to <collector>/v1/logs
// POST agentlog.ToOTLPJSON(batch["traces"]) to <collector>/v1/traces
// Content-Type: application/json
```

Sending the batch is one call — the exporter handles one-signal-per-request,
empty-signal skipping, and retries:

```go
exporter := agentlog.NewOTLPExporter("http://localhost:4318") // OTLP/HTTP default
if err := exporter.Export(ctx, batch); err != nil {
    // permanent (4xx) or exhausted retries — surface it, don't swallow it
}
// or drain-and-send in one step: exporter.ExportSink(ctx, sink)
```

Retry policy: `2xx` success; `408`/`429`/`5xx` and transport errors retry with
exponential backoff (100ms base, doubling, capped at 64×); other `4xx` are
permanent and fail fast. Defaults: 2 retries, 10s timeout — override via the
struct fields.

> Tip: the exporter is a transport, not a queue. In production point it at a
> local Collector, which owns durability and batching.

Mapping summary (authoritative spec: `docs/OTLP-MAPPING.md`):

| agent-log | OTLP |
| --- | --- |
| every event | one `LogRecord` in the logs signal |
| `network_request` | + one `SPAN_KIND_CLIENT` span (HTTP semconv) |
| `db_query` | + one `SPAN_KIND_CLIENT` span (DB semconv) |
| `crash` / `hang` | `LogRecord` with `exception.*` attributes, no span |
| `level` | `severityText` / `severityNumber` (`critical` → `FATAL`/21) |
| `payload.<k>` | attribute `payload.<k>` (flattened one level) |
| `trace_id` / `span_id` | `traceId` / `spanId` (deterministic derivation when absent) |

`OTLPSink` stores lines exactly as written, so redaction (C6) and the min-level
filter have already been applied by the logger — the exporter never re-processes
privacy-sensitive data.

## AI interaction events (Schema v1.4)

All six ends share one AI event contract (`INTERFACE.md` §4.4): six standard
`event_type` values, a snake_case payload vocabulary, and `agent_auto=false`
(explicit business-side instrumentation — the framework never hooks an LLM SDK).

```go
agentlog.AgentLog.RecordAIRequest("gpt-4o", "summarize this",
    map[string]interface{}{"request_id": "req_001", "provider": "openai"})
agentlog.AgentLog.RecordAIResponse("gpt-4o", 530,
    map[string]interface{}{"request_id": "req_001", "prompt_tokens": 320})
agentlog.AgentLog.RecordAIError("RateLimitError", "429 too many requests",
    map[string]interface{}{"request_id": "req_001"})
```

The same six methods exist on `*Logger` (`RecordAIRequest` / `RecordAIResponse` /
`RecordAIToolCall` / `RecordAIToolResult` / `RecordAIGuardrail` / `RecordAIError`);
`extra` carries standard-vocabulary or business keys and never overwrites the core
fields. Levels: request / response / tool = info, guardrail = warn, error = error.

`request_id` links request → response → tool → error within one turn; it is optional
and never generated/validated by the framework. `ai_error` reuses the v1.3 exception
vocabulary. OTLP export emits a LogRecord only — **no span**; attach `span_id`
explicitly if a downstream system needs to build one.

## API surface (facade `agentlog.AgentLog`)

| Method | Description |
| --- | --- |
| `Bootstrap(config)` | Initialize the shared logger. |
| `Info/Warn/Error/Debug/Trace/Critical(msg, opts...)` | Manual logs. |
| `Child(ctx)` | New logger merging `ctx` into bound scope (parent unchanged). |
| `SetLevel(level)` | Set minimum output level. |
| `Redact(paths)` | Append sensitive paths (defaults never removed). |
| `RecordAuto(eventType, msg, opts...)` | Emit an auto event. |
| `ExportPath()` | Path of the underlying sink file. |

Options: `WithCategory`, `WithTraceID`, `WithUserID`, `WithSubsystem`, `WithEnv`, `WithPayload`.

## Config

`LoggingConfig` (parse via hand-rolled JSON parser, no third-party lib):

| Key | Default | Notes |
| --- | --- | --- |
| `service` | `"app"` | required-ish, non-empty |
| `env` | `""` | |
| `release` | `""` | Schema v1.3；发布标识（semver / git sha / build number），≤128 字符，盖章到每条事件；OTLP 侧映射为 `service.version` |
| `level` | `"info"` | `warning`→`warn` |
| `redactEnabled` | `true` | |
| `redactPaths` | `[]` | appended to defaults |
| `exportDirectory` | `<tmpdir>/agent-log` | |

## Run the tests

```sh
go test ./...      # green
gofmt -l .         # no output = clean
go vet ./...       # clean
```

## Documentation

- [docs/INTERFACE.md](docs/INTERFACE.md) — language-neutral interface contract (verbatim).
- [docs/schema/log-event.schema.json](docs/schema/log-event.schema.json) — Schema v1.
- [docs/schema/example.json](docs/schema/example.json) — example wire line.

## License

MIT — see [LICENSE](LICENSE).

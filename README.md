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

### HTTP middleware

The most natural Go auto-instrumentation — record a `network_request` event per request:

```go
http.ListenAndServe(":8080", agentlog.HTTPMiddleware(myHandler))
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

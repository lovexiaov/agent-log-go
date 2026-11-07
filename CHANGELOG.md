# Changelog

All notable changes to this project are documented in this file.

## [1.0.0] - 2026-11-07

### Added
- Schema v1 JSONL wire-format logging conforming to the language-neutral contract.
- `Logger` with `emit`, `child` (non-mutating scope merge), `setLevel`, `redact` (append-only).
- Six levels (`trace|debug|info|warn|error|critical`); `warning`→`warn` normalization.
- Hand-rolled JSON serializer and parser (no third-party dependencies).
- `Redactor` with `DEFAULT_SENSITIVE_KEYS` (key masking, free-text regex, recursive masking).
- Sinks: `FileSink` and `MemorySink`.
- `LoggingConfig` with `FromJSON` (hand-rolled parser) and `LoadConfigFile`.
- Process-wide stable `session_id` generated at package init.
- Package-level facade `AgentLog`: `Bootstrap`, `Info/Warn/Error/Debug/Trace/Critical`,
  `Child`, `SetLevel`, `Redact`, `RecordAuto`, `ExportPath`.
- Auto-instrumentation: `HTTPMiddleware` (net/http `network_request`), `InstallPanicHandler`
  / `RecordAutoCrash` (`crash`), `InstallCrashHandler` note.
- C7 `TestSchemaConformance` plus logger/redactor/config/middleware/panic tests.
- **OTLP export adapter** (`agentlog/otlp.go`) — D3 / OpenTelemetry alignment:
  `ToOTLP`, `ToOTLPLogs`, `ToOTLPSpans`, `EventToLogRecord`, `EventToSpan`,
  `LevelToSeverity`, `ParseTimestampNanos`, `TraceIDHex`, `SpanIDHex`,
  `ToOTLPAnyValue`, `ToOTLPJSON`, plus the buffering `OTLPSink`.
  - every event → one `LogRecord`; `network_request` / `db_query` → one extra
    `SPAN_KIND_CLIENT` span (HTTP / Database semantic conventions); `crash` /
    `hang` carry `exception.*` attributes and stay in the logs signal.
  - ids are deterministic (SHA-256), so re-exports and retries are idempotent;
    a Schema v1.1 `span_id` on the event wins over the derived one.
  - `int64` values (nanos, counters) are emitted as strings per the protobuf
    JSON mapping; `payload` is flattened one level to `payload.<key>`.
  - no third-party dependency: the adapter reuses the package's own serializer.
- `Logger.SetSink` — swap the destination of an existing logger (e.g. route the
  shared logger into an `OTLPSink`).
- **OTLP/HTTP exporter** (`agentlog/otlp_http.go`): `OTLPExporter` puts those
  batches on the wire (one signal per request, empty signals skipped). Retries
  `408`/`429`/`5xx` and transport errors with exponential backoff; fails fast on
  other `4xx`; honours context cancellation.
- **Schema v1.1 `span_id`**: `LogEvent.SpanID`, `WithSpanID(...)` and
  `Child({"span_id": ...})`. Malformed values are dropped instead of written, so a
  bad value can never produce a schema-violating line; the OTLP adapter prefers the
  bound id over its derived one.
- `docs/OTLP-MAPPING.md` (spec copy) and an OTLP section in the README.
- 42 new tests: `agentlog/otlp_test.go` (25 — levels, timestamps/offsets, AnyValue
  typing, id derivation, HTTP/DB spans, resource grouping, sink drain, JSON
  validity), `agentlog/otlp_http_test.go` (11 — request shape, retry matrix,
  backoff, headers, cancellation) and `agentlog/span_test.go` (6 — span_id
  serialization, invalid-value dropping, child binding, OTLP passthrough).
- Docs (INTERFACE.md, schema), README, CONTRIBUTING, LICENSE (MIT), CI workflow.

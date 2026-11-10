# Changelog

All notable changes to this project are documented in this file.

## [1.3.0] - 2026-11-10

Schema v1.3（向后兼容增量）。

### Added

- 顶层可选 `release`：`Config{Release: "1.4.2"}` 配置后盖章到**每条**事件
  （含 `RecordAuto` 与崩溃事件），并可用 `Logger.SetRelease` 运行时覆盖；
  不校验格式、仅限非空且 ≤128 字符；未配置则不写该键。
- OTLP：`release` 映射为 resource 的 `service.version`。

### Changed（异常 payload 标准词表）

- 未捕获异常 / 崩溃事件统一按标准词表写 `payload`：`exception`（类型名）、
  `exception_message`（原始消息）、`stack`（单字符串）、`mechanism`
  （`uncaught` / `handled` / `reported`）。`InstallPanicHandler` 的 payload 由 `thread` + 标准键组成。
- OTLP 适配器读取侧继续兼容历史键（`message` / `stacktrace` / `trace`），老日志
  映射行为不变。

### Docs

- 与 `ai-dev-spec` 的 Schema v1.3 / `INTERFACE.md` §4「异常与版本」对齐；
  说明框架只产出数据，不做上报 / APM 集成 / 崩溃落盘。

## [1.2.0] - 2026-11-07

### Changed（Schema v1.2，破坏性）

- `trace_id` 收紧为 **32 位小写 hex**（8 字节，与 OTLP `traceId` / W3C traceparent
  trace-id 同构）。大写自动归一；非法值在序列化边界**丢弃该字段、事件照写**，与
  `span_id` 策略一致。适配器不再对非 hex 值做 `sha256("agentlog:trace:"+raw)` 派生。
  发布时尚无生产使用方（Schema v1.2 描述内已注明）。
- session id 默认生成值改为 32 位小写 hex：`trace_id = session_id` 这个最自然的
  绑定永远合法。

### Added

- `newTraceId()`：生成 Schema 合法的 trace id（uuid4 去横线）。

### Docs

- README 自动埋点小节删除并不存在的 `HTTPMiddleware`，改为 `WrapDB`（包装 `*sql.DB`
  → `db_query`）；更正网络事件描述（Go 无 HTTP 客户端中间件自动埋点，需手动 `RecordAuto`）。

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

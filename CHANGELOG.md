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
- Docs (INTERFACE.md, schema), README, CONTRIBUTING, LICENSE (MIT), CI workflow.

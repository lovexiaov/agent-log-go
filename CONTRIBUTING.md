# Contributing

Thanks for contributing to **agent-log-go**!

## Prerequisites

- Go 1.21+
- No third-party dependencies are allowed (standard library only).

## Build & verify

```sh
# Format (must show no files needing formatting)
gofmt -l .

# Fix formatting
gofmt -w .

# Static analysis (must be clean)
go vet ./...

# Tests (must be green)
go test ./...
```

All three (`gofmt -l .`, `go vet ./...`, `go test ./...`) run in CI on every push/PR to `main`.

## Guidelines

- **Wire format is sacred.** Every emitted line MUST validate against
  `docs/schema/log-event.schema.json` (`additionalProperties: false`). Do not add top-level keys.
- **Redaction is append-only.** Never remove a default sensitive key.
- **`child()` must not mutate the parent.**
- **`agent_auto=true` events are always logged**, regardless of `minLevel`.
- **Map camelCase → snake_case only at the serialization boundary.**
- Mirror behavior with the sibling implementations (Kotlin/Swift/TypeScript/Java/Python).

## Commit messages

Use conventional, descriptive commits, e.g. `feat: ...`, `fix: ...`.

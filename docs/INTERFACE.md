# Logging Interface（语言中立的日志接口规范）

> **语言无关**。定义「一个合规 logger 必须实现什么」，是统一日志框架的契约真源。
> 各语言实现（`web/Observability` 的 TypeScript、`macos/AgentLog` 的 Swift …）都必须 conform 本规范。
> 线格式（wire format）由 `schema/log-event.schema.json` 定义，本文件定义**方法面**。

## 1. 必实现方法

| 方法 | 签名（伪代码） | 语义 |
| --- | --- | --- |
| `emit` | `emit(event: LogEvent): void` | 接收结构化事件 → 映射为 Schema v1（snake_case）→ 脱敏 → 写出。 |
| `child` | `child(context: Partial<LogEvent>): Logger` | 返回一个合并了 `session_id / user_id / subsystem / env` 等作用域 logger。 |
| `setLevel` | `setLevel(level: Level): void` | 设置最低输出级别（见 §3 词表）。 |
| `redact` | `redact(paths: string[]): void` | **追加**脱敏路径；默认脱敏键**不可关闭**。 |

## 2. 一致性约束（conformance）

- 每条经 `emit` 写出的记录 MUST 能通过 `schema/log-event.schema.json` 校验（字段集封闭、snake_case、必填齐全）。
- 调用侧仍可用 camelCase 书写（`event` / `traceId` / `userId`），由 logger 在**序列化边界**映射为 snake_case 线格式。
- 非保留键（如业务字段 `amount`、`orderId`）MUST 进入 `payload` 对象，不得泄漏到顶层。
- 脱敏 MUST 在 logger 层拦截，不依赖调用方自觉。
- 链路标识：`trace_id`（可选）串联一次请求链路；`span_id`（v1.1 可选）标识其中的**一次操作**（如一次 HTTP 调用 / 一次 DB 查询）。若产出 `span_id`，MUST 为 16 位小写十六进制（8 字节，与 W3C `traceparent` 的 span-id 同构），且 SHOULD 与该链路已有的 `trace_id` 一并写出；无法取得 `trace_id` 时可省略两者，由读取/导出端决定是否派生。

## 3. Level 词表（与 Schema v1 一致）

`trace | debug | info | warn | error | critical`（6 档，全小写）。

## 4. 配置（语言中立）

统一读取 `config/logging.config.json`（Schema：`config/logging.config.schema.json`）。
各语言实现解析同一份 JSON，保证「换语言不改配置」。

# Go 示例演示

演示 [agent-log-go](../README.md)（仅标准库）的端到端用法，对应契约能力：

- `Bootstrap` + `DefaultLoggingConfig`（service / env / level）
- 6 档级别：`trace / debug / info / warn / error / critical`
- `Child`：合并作用域上下文，返回新 logger，父级不变
- `Redact`：默认敏感键不可关闭，可追加自定义键
- 自动埋点：`HTTPMiddleware`（network_request）、`InstallPanicHandler`（crash）

## 运行

在仓库根目录执行：

```bash
go run ./example
```

事件落盘于系统临时目录的 `agent-log/agent-log.jsonl`，程序结束前会打印该路径。

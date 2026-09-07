# internal/logx

统一结构化日志与敏感信息打码。

## 职责边界

- 日志一律写 stderr，结构化输出（JSON 或 key=value）。
- **不做日志文件轮转**，由 systemd / OpenRC 统一收集管理。
- **敏感信息自动打码（Redact）**：密码、token、wallet 密码、主密钥、云厂商 AK/SK 等绝对禁止明文输出。
- 作为唯一允许输出配置内容的途径。

## 对外接口

- `Info(msg string, args ...any)`、`Error(msg string, args ...any)`、`Warn(msg string, args ...any)`、`Debug(msg string, args ...any)`
- `Redact(s string) string`：敏感信息打码辅助函数。
- `RedactConfig(cfg any) string`：配置对象安全脱敏序列化。

## 依赖谁

- 标准库 `log/slog`。
- 零业务内部模块依赖（基础底层包）。

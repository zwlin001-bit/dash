# internal/logx

统一结构化日志与敏感信息打码。

## 职责边界

- 日志一律写 stderr，结构化 JSON 输出。
- **不做日志文件轮转**，由 systemd / OpenRC 统一收集管理。
- **敏感信息自动打码（Redact）**：密码、token、wallet 密码、主密钥、云厂商 AK/SK 等绝对禁止明文输出。
- 底层 slog handler 会在输出层对消息文本、敏感键属性与字符串属性进行自动拦截打码，形成深度防御。
- 作为唯一允许输出配置内容的途径。

## 对外接口

- `Info(msg string, args ...any)`、`Error(msg string, args ...any)`、`Warn(msg string, args ...any)`、`Debug(msg string, args ...any)`
- `With(args ...any) *Logger`：派生带有上下文属性的 Logger。
- `New(w io.Writer, level Level) *Logger`：创建独立 Logger 实例（可用于测试捕获或指定输出目标）。
- `Default() *Logger` / `SetDefault(l *Logger)`：获取或设置全局默认 Logger。
- `Redact(s string) string`：敏感信息打码辅助函数，支持 URI/DSN 密码、Oracle EZConnect、Oracle TNS Descriptor、Key-Value 键值对、URL Query 参数及 Bearer Token。
- `RedactConfig(cfg any) string`：配置对象安全脱敏序列化（结构体反射脱敏 + 正则过滤双重防线）。

## 依赖谁

- 标准库 `log/slog`。
- 零业务内部模块依赖（基础底层包）。

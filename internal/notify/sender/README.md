# notify/sender

通知渠道驱动适配器层。

## 职责边界

- 定义统一的 `Sender` 驱动接口与不可重试错误封装。
- 提供 Telegram Bot 与通用 HTTP Webhook 驱动实现。
- 保证所有出网请求带超时、序列化、频率限制与机密清洗。
- 注册表模式：加一种新渠道只需新增一个实现文件并在 `init()` 中调用 `Register()`。

## 对外接口

- `Sender` interface
- `Register(s Sender)`
- `Get(kind string) (Sender, bool)`
- `RegisteredKinds() []string`
- `IsNonRetriable(err error) bool`
- `MarkNonRetriable(err error) error`

## 依赖谁

- 标准库 `net/http`, `crypto/hmac`, `crypto/sha256`。
- `dash/internal/logx` 机密脱敏。
- 零外部 SDK 依赖。

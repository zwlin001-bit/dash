# internal/ulid

ULID（Universally Unique Lexicographically Sortable Identifier）生成工具。

## 职责边界

- 生成 26 字符的标准 ULID 字符串。
- 48 位 UNIX 毫秒时间戳 + 80 位加密安全伪随机数，采用 Crockford Base32 编码。
- 用于全系统业务实体 ID 生成（遵守 PRINCIPLES P2: 主键由应用生成）。

## 对外接口

- `New() string`: 使用当前时间与随机数生成 ULID。
- `NewWithTime(t time.Time) string`: 使用指定时间与随机数生成 ULID。
- `IsValid(s string) bool`: 校验字符串是否为合法的 26 字符 ULID。

## 依赖谁

- 标准库 `crypto/rand`, `sync`, `time`

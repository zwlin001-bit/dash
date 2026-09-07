# internal/audit

审计日志写入模块。

## 职责边界

- 负责统一向 `audit_log` 数据表写入变更与关键操作审计记录（谁、何时、对哪个资源、做了什么、结果）。
- 严格遵循 PRINCIPLES P2，统一使用跨 Oracle / MySQL 可移植 SQL。

## 对外接口

- `Log(ctx, db, entry)`: 异步或同步写入审计日志。
- `LogTx(ctx, tx, entry)`: 在数据库事务上下文中写入审计日志。

## 依赖谁

- `internal/db`
- `internal/ulid`

# internal/events

事件总线与事件存储核心层（第一期 · 任务 20）。

## 职责边界

- 提供全工程唯一的事件发布入口 `events.Emit(ctx, e)`
- 维护内置事件类型注册表与运行期处置策略缓存（`event_types`）
- 异步队列持久化事件至 `events` 表，确保事件写入永不阻塞业务、故障不中断主流程
- 提供事件查询、批量已读、未读计数以及类型策略维护等数据层支撑

## 对外接口

- `Emit(ctx context.Context, e Event)`: 全工程唯一的事件入口，非阻塞且永不返回错误
- `RegisterType(t TypeDef)`: 注册内置事件类型元数据与默认策略
- `GetPolicy(eventType string) (severity, disposition string)`: 获取当前生效的级别与处置方式
- `NewStore(db *db.DB, bufferSize int) *Store`: 构造事件存储与消费器
- `NewModule() *Module`: 实现 `app.Module` 注册契约

## 依赖关系

- 依赖 `internal/db`: 数据库连接与可移植查询抽象（`QueryPage` 等）
- 依赖 `internal/logx`: 脱敏日志记录
- 依赖 `internal/app`: 服务端模块注册契约
- 依赖 `migrations/0002_events.*.sql`: `event_types` 与 `events` 数据表

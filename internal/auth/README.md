# internal/auth

用户身份认证、会话管理与安全校验。

## 职责边界

- 账号密码安全哈希与校验（基于标准 bcrypt 算法）。
- 会话管理：安全随机会话令牌签发、SHA-256 存储哈希、Cookie/Bearer 认证。
- 防暴力破解：同一用户名或客户端 IP 连续失败 5 次自动锁定 15 分钟（返回 HTTP 429）。
- 记录关键认证审计日志（登录成功/失败、注销）。

## 对外接口

- `NewService(db)`: 创建认证服务实例。
- `NewModule()`: 提供遵循 `app.Module` 规范的模块。
- `RegisterRoutes(mux)`: 注册登录/登出/用户信息路由。
- `RequireAuth(next)`: 认证拦截中间件。
- `UserFromContext(ctx)`: 从上下文提取已登录用户信息。

## 依赖谁

- `internal/db`
- `internal/audit`
- `internal/ulid`
- `golang.org/x/crypto/bcrypt`

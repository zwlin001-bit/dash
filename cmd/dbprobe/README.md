# cmd/dbprobe

数据库连通性自检工具。

## 职责边界

- 测试数据库连通性并输出数据库引擎版本与当前 Schema。
- 支持读取 `.secrets/adb.env`（`KEY=VALUE` 行格式，优先读 `ADB_DSN`）、环境变量（`ADB_DSN`, `ADB_USER`, etc.）或命令行参数（`-env`, `-driver`, `-dsn`, `-user`, `-password`）。
- 作为环境验收及部署自检的轻量独立工具，直接读取 .secrets/adb.env，不引入额外配置库或外部日志库。

## 用法

```bash
# 默认读取 .secrets/adb.env
go run ./cmd/dbprobe

# 通过参数指定环境文件或覆盖连接串
go run ./cmd/dbprobe -env .secrets/adb.env
go run ./cmd/dbprobe -driver mysql -dsn "root:root@tcp(127.0.0.1:3306)/dash"
```

## 依赖谁

- `dash/internal/db`

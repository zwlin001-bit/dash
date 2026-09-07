# cmd/dbprobe

数据库连通性自检工具。

## 职责边界

- 测试数据库连通性并输出数据库引擎版本与当前 Schema。
- 支持读取配置文件 `/etc/dash/config.toml`、环境变量（`DASH_DB_*`）或命令行参数（`-driver`, `-dsn`, `-user`, `-password`）。
- 作为环境验收及部署自检的轻量独立工具。

## 用法

```bash
# 默认读取 /etc/dash/config.toml
go run ./cmd/dbprobe

# 通过参数覆盖
go run ./cmd/dbprobe -driver mysql -dsn "root:root@tcp(127.0.0.1:3306)/dash"
```

## 依赖谁

- `dash/internal/config`
- `dash/internal/db`
- `dash/internal/logx`

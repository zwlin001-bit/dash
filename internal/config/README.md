# internal/config

服务端配置加载与管理。

## 职责边界

- 读取 `/etc/dash/config.toml` 配置文件。
- 支持 `DASH_*` 环境变量覆盖配置项（如 `DASH_DB_PASSWORD`）。
- 仅管理启动自举所必需的配置（数据库连接、主密钥路径、监听地址等）。
- **禁止存放域名等业务配置**，域名统一存储于数据库 `settings` 表并在运行时维护。
- 优先级：命令行参数 > 环境变量 > 配置文件。

## 对外接口

- `Load(path string) (*Config, error)`：加载并解析配置。
- `Config`：包含 `DBConfig`、`ServerConfig` 等自举配置结构体。

## 依赖谁

- 标准库及 TOML 解析库。
- 零业务内部模块依赖（基础底层包）。

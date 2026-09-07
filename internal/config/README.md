# internal/config

服务端配置加载与管理。

## 职责边界

- 读取 `/etc/dash/config.toml` 配置文件。
- 支持 `DASH_*` 环境变量覆盖配置项（如 `DASH_DB_PASSWORD`）。
- 支持配置文件内 `${VAR}` 占位符环境变量展开。
- 仅管理启动自举所必需的配置（数据库连接、主密钥路径、监听地址等）。
- **禁止存放域名等业务配置**，域名统一存储于数据库 `settings` 表并在运行时维护。
- 优先级：命令行参数 > 环境变量 > 配置文件 > 默认值。

## 对外接口

- `Load(path string) (*Config, error)`：加载并解析配置（默认路径 `/etc/dash/config.toml` 或 `DASH_CONFIG`）。
- `LoadWithOptions(opts Options) (*Config, error)`：按优先级加载并合并各层配置覆盖项。
- `RegisterFlags(fs *flag.FlagSet) *CLIFlags`：为命令行 flag 工具注册配置参数并提取显式覆盖项。
- `Config` / `DBConfig` / `ServerConfig`：自举配置数据结构。
- `DefaultConfig() *Config`：提供默认自举配置。

## 环境变量支持

- `DASH_CONFIG`：配置文件路径覆盖
- `DASH_DB_DRIVER`：数据库驱动（`oracle` 或 `mysql`）
- `DASH_DB_USER`：数据库用户名
- `DASH_DB_PASSWORD`：数据库密码（免落盘推荐方式）
- `DASH_DB_DSN`：数据库连接串 / DSN
- `DASH_DB_WALLET_PATH`：ADB Wallet 目录路径
- `DASH_DB_MAX_OPEN_CONNS`：连接池最大打开连接数
- `DASH_DB_MAX_IDLE_CONNS`：连接池最大空闲连接数
- `DASH_DB_CONN_MAX_LIFETIME_S`：连接池连接最大生命周期（秒）
- `DASH_SERVER_LISTEN`：服务监听地址（默认 `:443`）
- `DASH_SERVER_LISTEN_ACME`：ACME 监听地址（默认 `:80`）
- `DASH_SERVER_DATA_DIR`：数据存储目录（默认 `/var/lib/dash`）
- `DASH_SERVER_MASTER_KEY`：凭据主密钥文件路径（默认 `/etc/dash/master.key`）

## 依赖谁

- 标准库及 TOML 解析库 `github.com/pelletier/go-toml/v2`。
- 零业务内部模块依赖（基础底层包）。

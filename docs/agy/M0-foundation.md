# M0 地基

**目标：能连上 ADB、能跑迁移、有一个所有 SQL 都必须经过的可移植访问层。**

后面每一个里程碑都压在这上面。M0 不扎实，P2 的可移植性约束会在 M2 之后彻底腐化。

---

## T0.0 打通 ADB 连接【前置阻塞项】

### 已确认的事实（2026-09-07）

```
ADB 实例   : crni4g8pj5q0bq2z @ ap-tokyo-1（可用性域 FUis:AP-TOKYO-1-AD-1）
网络配置   : 相互 TLS (mTLS) = 必需
             访问控制列表 (ACL) = 已禁用
             访问类型 = 允许从任何位置进行安全访问
实测结果   : TCP + TLS 握手成功（TLSv1.3）
             python-oracledb 4.0.2 thin 模式 + TLS-only 连接串
             → DPY-6000: Listener refused connection（类似 ORA-12506）
```

**结论：拒绝的原因就是 mTLS 必需，而手上的连接串是 TLS-only 格式（不含 wallet 引用）。
与用户名密码无关。**

### 决定：保持 mTLS，使用 wallet

备选方案是「启用 ACL 白名单 + 把 mTLS 改为不需要」，**不采纳**——
dash 管理的是会变动的 VPS，服务器和出口 IP 都可能变，ACL 方案会在换机时把自己锁在外面。
wallet 是一个文件，比一份 IP 台账好维护。

### T0.0a 取得并落位 wallet

**交付物**
- wallet 压缩包解压到 `.secrets/wallet/`（已被 `.gitignore` 排除，**永不入库**）
- `.secrets/adb.env` 补上 `ADB_WALLET_DIR` 和 `ADB_WALLET_PASSWORD`
- 用 python-oracledb thin 模式 + wallet 连通一次，确认凭据与 wallet 匹配

**验收**：能查出数据库版本与当前 schema。

### T0.0b 验证 go-ora 能用这个 wallet【载荷性假设，优先做】

**这一步比 T0.0a 更重要。**

整个数据库层选 `github.com/sijms/go-ora/v2`（纯 Go）是为了保住「单二进制 + 一条命令部署」
（`PRINCIPLES.md` P3、P5）。但 go-ora 的 wallet（`cwallet.sso`）支持不如官方驱动成熟，
**存在跑不通的可能**。这个假设不验证，M0 之后所有服务端工作都建在流沙上。

**交付物**：`cmd/dbprobe` 小工具，读 `.secrets/adb.env`，用 **go-ora + wallet** 连库，
打印数据库版本、当前 schema、`user_tables` 数量。

**验收**：`go run ./cmd/dbprobe` 输出数据库版本，退出码 0，
且**二进制是 `CGO_ENABLED=0` 编译出来的**（`ldd` 显示 not a dynamic executable）。

**如果 go-ora 跑不通**，立刻停下来上报，不要自行换驱动。退路只有两条，
都需要重新决策（**换 godror + Instant Client 会直接毁掉单二进制部署目标，是最后手段**）：

1. 改用方案 B：启用 ACL 白名单 + 把 mTLS 改为「不需要」，回到 TLS-only 连接串，不用 wallet
2. 换 `godror` + Oracle Instant Client —— 违反 P3/P5，需要用户重新拍板

### 对部署的影响（写给 T5.2 的人）

- `setup.sh install` 增加 `--wallet <dir>` 参数（mTLS 模式下必填）
- wallet 目录安装到 `/etc/dash/wallet/`，`0700`，属主 `dashd`
- `config.toml` 的 `wallet_path` 指向它
- **wallet 证书有有效期，到期需要重新下载。** `setup.sh status` 要检查并提示剩余有效期
- `/etc/dash/wallet/` 加入 `05-deployment.md` §6 的必备份文件清单

---

## T0.1 工程骨架

按 `01-architecture.md` §4 建目录。

**交付物**
- `go.mod`（Go 1.22+）、`Makefile`、`cmd/dashd`、`cmd/dash-agent`、`internal/*`、`agent/*`
- `Makefile` 目标：`build` / `build-agent-all`（三架构交叉编译）/ `test` / `lint` / `migrate`
- 每个 `internal/*`、`agent/*` 目录一个 `README.md`（P1.4）
- CI 脚本（可选，但 `make lint test` 必须能本地一条命令跑通）

**约束**
- agent 与 dashd **共享协议结构体**，放 `internal/protocol/`，两边都 import，不各写一份
- `agent/` 下的包**不允许 import `internal/db`、`internal/api` 等服务端包**。
  用 `go list` 或 lint 规则强制，防止 agent 被服务端依赖污染撑大

**验收**：`make build` 产出 `bin/dashd` 与 `bin/dash-agent`；`make lint test` 通过。

---

## T0.5 协议契约 `internal/protocol`【并行的关键前置】

> 编号靠后，但**执行顺序紧跟 T0.1**，与 T0.2 并行。见 [`01-plan.md`](01-plan.md)。

**这个包一旦定稿，agent 和服务端就能完全并行开发。所以它必须排在最前面、单独交付。**

**交付物**
```
internal/protocol/
  version.go     ProtocolVersion = 1
  rpc.go         JSON-RPC 2.0 Request/Response/Error 信封
  agent2srv.go   agent.hello / agent.metrics / agent.facts / agent.result
                 / agent.ping_result / agent.speedtest_result 的 params 结构体
  srv2agent.go   server.config / server.exec / server.upgrade / server.ping_task
                 / server.speedtest / server.terminal_open / server.reload_actions
  errors.go      -32000 ~ -32004 错误码常量
  README.md
```

**约束**
- 严格按 `04-protocol.md`，字段名、可选性、单位一一对应
- **agent 与 dashd 都 import 这个包，绝不各写一份**
- 可选字段用指针或 `omitempty`，**采集失败的项是「省略」不是「填 0」**
- 这个包**不允许 import 任何其他 internal 包**，保持零依赖

**验收**
- `go test ./internal/protocol/` 覆盖每个消息的 JSON 序列化/反序列化往返
- 文档里每个示例 JSON 都有一条对应的测试用例，能反序列化成功

**变更纪律**：M0 之后任何人要改这个包，必须先改 `04-protocol.md` 并升 `ProtocolVersion`，
且**必须由调度方统一处理**——并行开发期间这个包被两个人同时改必然冲突。

---

## T0.2 可移植 SQL 访问层 `internal/db`

**这是 P2 落地的唯一位置。**

**交付物**
```
internal/db/
  db.go           连接、连接池、事务封装 (WithTx)
  batch.go        BatchInsert(ctx, table, cols, rows) 统一接口
  dialect/
    dialect.go    interface { Rebind, Paginate, Upsert, BatchInsert }
    oracle.go     :1 占位符 / OFFSET..FETCH / MERGE / 数组绑定
    mysql.go      ? 占位符 / LIMIT..OFFSET / ON DUPLICATE KEY / 多行 VALUES
  README.md
```

**约束**
- Oracle 驱动用 `github.com/sijms/go-ora/v2`（纯 Go）。**不用 godror**——它要 Instant Client，
  会毁掉单二进制部署
- 业务代码一律写 `?` 占位符，由 `Rebind` 改写。业务代码**不允许 import `dialect` 包**
- 连接池参数从配置读，默认见 `02-database.md` §1.6

**验收**
- 写一个 lint 检查（`make lint` 的一部分）：扫描 `internal/`（排除 `internal/db/dialect`）
  和 `agent/` 下的 `.go` 文件，命中 `ROWNUM|SYSDATE|NVL\(|DECODE\(|FROM dual|MERGE INTO|ON DUPLICATE|LIMIT |FETCH NEXT`
  即报错。**这个检查必须存在，它是 P2 唯一的自动化保障。**
- 同一份业务查询代码，切 `driver=mysql` 后能在本地 MySQL 8 上跑通（用 docker 起一个即可）

---

## T0.3 迁移框架 + 初始 DDL

**交付物**
- `internal/migrate`：读 `migrations/`，按编号顺序执行，记录到 `schema_migrations`
- `migrations/0001_init.oracle.sql` 和 `migrations/0001_init.mysql.sql`
- `make migrate` / `dashd migrate` 子命令

**约束**
- 严格按 `02-database.md` §1.2 的类型映射。**Oracle 侧不许出现 `BIGINT`、`BOOLEAN`、
  `TIMESTAMP`、`DOUBLE PRECISION`**（用 `NUMBER(19)` / `NUMBER(1)` / `NUMBER(19)` / `BINARY_DOUBLE`）
- 所有标识符小写、不加引号、≤30 字符，避开 `02-database.md` §1.3 的保留字黑名单
- 时间列一律 `*_at_ms` / `*_ms`，`NUMBER(19)` / `BIGINT`
- **两份 DDL 的表和列必须完全对应**。写一个测试：分别在 Oracle 和 MySQL 上跑完迁移，
  对比 `information_schema` 的表名列名集合，不一致即失败

**本次要建的表**（其余留到用到时再加迁移）
```
schema_migrations
account_users  user_sessions  api_tokens  settings  audit_log  credentials
nodes  node_groups  tags  node_tags  node_facts  node_billing
metric_defs  metric_series
sample_host    sample_host_1m    sample_host_1h    sample_host_1d
sample_dim     sample_dim_1m     sample_dim_1h     sample_dim_1d
jobs  job_steps
alert_rules  alert_events  notify_channels          -- 建表不实现逻辑
speedtest_targets  speedtest_tasks  speedtest_runs  -- 含三个内置运营商的种子数据
providers  cloud_accounts  cloud_resources          -- 建表不实现逻辑
proxy_nodes  proxy_configs  proxy_deployments       -- 建表不实现逻辑
```

**验收**
- 空库上 `make migrate` 成功建出全部表
- 重复执行幂等，不报错
- 对比测试通过（Oracle 与 MySQL 表结构一致）
- `speedtest_targets` 里有电信/联通/移动三组内置目标，`is_builtin=1`

---

## T0.4 配置与主密钥

**交付物**
- `internal/config`：读 `/etc/dash/config.toml`（格式见 `deploy/config.example.toml`），
  支持 `DASH_*` 环境变量覆盖
- `internal/crypto`：信封加密。主密钥从 `master_key` 指向的文件读（32 字节随机），
  AES-256-GCM 加密 `credentials.enc_payload`

**约束**
- **域名不在配置文件里**，在 `settings` 表的 `site.domain`（P5.4）
- 主密钥文件不存在时，`dashd migrate` 阶段自动生成（0400）
- 日志和错误信息里**永远不打印密码、token、主密钥**。写一个 redact 辅助函数并强制使用

**验收**：加密一段文本 → 重启进程 → 解密得到原文；配置文件里的密码不出现在任何日志中。

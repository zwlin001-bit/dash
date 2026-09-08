# 01 架构设计

## 1. 目标与范围

dash 管理个人 VPS，覆盖四件事：

1. **监控** —— 每台机器装 agent，秒级采集，历史留存，告警
2. **云资产** —— 阿里云、GCP 的实例清单同步、创建、销毁、计费信息
3. **代理服务器管理** —— 代理节点的创建、部署、配置下发、健康检查
4. **统一控制台** —— 一个域名、一个 443 端口、一套账号

非目标：多租户、大规模（>500 节点）、日志采集、APM。

> **本文件描述的是目标架构。当期实际要实现的范围见 [`agy/00-scope.md`](agy/00-scope.md)**
> ——第一期只做监控链路（agent 上报 + 指令下发）与三网测速，云集成和代理管理只建表、不实现。

---

## 2. komari 参考分析

通读 `komari` 与 `komari-agent` 后的结论。

### 2.1 值得直接借鉴的

| 点 | komari 的做法 | dash 采纳 |
|---|---|---|
| 传输 | 单条 WebSocket 长连接承载上报 + 下行指令，JSON-RPC 2.0 notification | 采纳。一条连接同时解决上报、心跳、在线状态、指令下发 |
| 上报回退 | WS 建不起来时退化为 HTTP POST 上报 | 采纳。NAT / 反代不支持 WS 的环境靠这个兜底 |
| 静态信息分离 | 基础信息（CPU 型号、内核、总内存）低频单独上报，不混在指标里 | 采纳，并进一步改成「变更时才报」 |
| 时序分层 | raw → 分钟 rollup → 粗粒度 rollup，各自独立保留期 | 采纳，但大幅简化（见 2.2） |
| 写入批处理 | 服务端聚合 3 秒一批落库，而不是每条上报一次 INSERT | 采纳。对 ADB 这种网络数据库尤其关键 |
| 流量计数器 | 区分累计计数器（`net_total_*`）与区间增量（`traffic_*`），服务端做 reset 感知 | 采纳。机器重启导致计数器回绕必须显式处理 |

### 2.2 明确不采纳 / 要改掉的

| komari 的做法 | 问题 | dash 的做法 |
|---|---|---|
| `pkg/metric` 归一化星型模型（definitions / labels / series / resolutions / rollups + t-digest），约 1.7 万行 | 对 30 台机器是严重过度设计；每次查询要 join 4 张表；t-digest 用 BLOB 存，不可移植 | 固定列宽表 + 窄表混合（见 `02-database.md` §5） |
| `Client.Tags` 用 `;` 拼接字符串存 | 无法索引、无法按标签查询、改一个标签要读改写 | 独立的 `tags` / `node_tags` 关联表 |
| 监控字段、计费字段、分组字段全塞进一张 `clients` 表 | 职责混杂，加字段就动核心表 | 拆成 `nodes` / `node_facts` / `node_billing` |
| agent 依赖 gopsutil（跨全平台） | 二进制 ~12MB，采集路径分配多，携带大量用不上的 Windows/Darwin 代码 | 只做 Linux，直接读 `/proc`，二进制目标 ≤ 5MB |
| agent 所有指标同频（默认 3s）采集，包含连接数、进程数 | 解析 `/proc/net/tcp` 是最贵的一项，几千条连接时每次几毫秒 CPU + 大量分配 | 采集分级，昂贵项降频（见 `03-agent.md` §3） |
| agent 从 GitHub 自更新 | 供应链风险、增加体积、内网/受限网络不可用 | 由 dash 服务端下发升级，产物自己托管 + sha256 校验 |
| 每条消息单独 gzip | 200 字节的报文压缩收益为负，纯粹是分配和 CPU 浪费 | 小报文不压缩；只对基础信息等大报文启用 |
| 远程执行是「开/关」两态 | 开了就是任意 shell，攻击面过大 | 三态：关 / 动作清单 / 任意 shell（逐台显式开启） |
| SQLite/MySQL/PG 三套方言散在各处 | 迁移成本高 | 方言差异收敛到一个包的三个模板（P2.9） |

---

## 3. 部件

```
                         ┌──────────────── :443 ────────────────┐
                         │              dashd (Go)              │
   agent (Go, 静态)      │                                      │
   ┌──────────┐   WSS    │  ┌────────────────────────────────┐  │
   │ dash-    ├──────────┼─▶│ ingest      指标接收 / 批量落库 │  │
   │ agent    │◀─────────┼──┤ control     指令下发 / 在线状态 │  │
   └──────────┘  指令    │  ├────────────────────────────────┤  │
                         │  │ jobs        异步任务引擎        │  │
   浏览器 ────HTTPS──────┼─▶│ api         REST + SSE          │  │
                         │  │ web         内嵌前端            │  │
                         │  ├────────────────────────────────┤  │
                         │  │ db          可移植 SQL 访问层   │  │
                         │  └───────┬──────────────┬─────────┘  │
                         └──────────┼──────────────┼────────────┘
                                    │              │ unix socket
                            ┌───────▼──────┐   ┌───▼──────────────┐
                            │ Oracle ADB   │   │ provider 插件进程 │
                            │ (未来 MySQL) │   │ aliyun / gcp /   │
                            └──────────────┘   │ proxy  (Go/Py)   │
                                               └──────────────────┘
```

### 3.1 dashd

单个 Go 二进制，内嵌前端。职责：

- 终结 TLS，监听 443（域名与证书见 `05-deployment.md`）
- `/api/agent/v1/rpc` —— agent WebSocket 端点
- `/api/agent/v1/report` —— HTTP 上报回退端点
- `/api/v1/**` —— 控制台 REST API
- `/**` —— 前端静态资源
- 内部跑：ingest 批处理器、rollup 调度、保留期清理、job worker、provider 进程监管

服务端允许吃资源（用户明确接受），因此设计上优先选**简单可靠**而不是极致省内存。

### 3.2 dash-agent

见 `03-agent.md`。

### 3.3 Provider 插件

**这是「加一个云厂商不用改核心代码」的落点。**

- 每个 provider 是一个独立进程，由 dashd 拉起并监管（崩溃重启、健康检查）
- 通信：Unix domain socket 上的 JSON-RPC 2.0，socket 路径由 dashd 分配
- 语言不限。**Go 和 Python 混用是被允许的**——阿里云 / GCP 的官方 SDK 在 Python 侧更成熟，用 Python 写这两个 provider 是合理选择
- provider 不碰数据库。它只做「把云厂商 API 翻译成 dash 的资源模型」，持久化由 dashd 负责

契约（v1）：

| 方法 | 方向 | 说明 |
|---|---|---|
| `provider.describe` | dashd → provider | 返回 provider 元信息：code、支持的资源类型、需要的凭据字段、能力位 |
| `provider.healthcheck` | dashd → provider | 存活与凭据可用性 |
| `resource.list` | dashd → provider | 列举某类资源（分页），返回归一化后的资源对象 |
| `resource.get` | dashd → provider | 取单个资源详情 |
| `resource.create` | dashd → provider | 创建资源，**返回 job handle，异步** |
| `resource.delete` | dashd → provider | 销毁资源，异步 |
| `resource.action` | dashd → provider | 启停 / 重启 / 改配等动作，异步 |
| `job.poll` | dashd → provider | 轮询异步操作状态 |
| `metric.list` | dashd → provider | 给定资源、指标、时间范围，返回时序点（`16-cloud-metrics.md` §3） |
| `bill.list` | dashd → provider | 给定账期，返回归一化账单 |
| `event.log` | provider → dashd | provider 主动上报日志 / 进度 |

归一化资源对象（provider 必须映射到这个形状）：

```json
{
  "provider_code": "aliyun",
  "kind": "instance",
  "ref": "i-xxxxxxxx",
  "name": "hk-proxy-01",
  "region": "cn-hongkong",
  "status": "running",
  "public_ips": ["1.2.3.4"],
  "private_ips": ["172.16.0.5"],
  "specs": { "vcpu": 2, "mem_mb": 2048, "disk_gb": 40 },
  "billing": { "currency": "CNY", "price": 24.0, "cycle_days": 30, "expires_at_ms": 1767225600000 },
  "attrs": { "任意 provider 特有字段" }
}
```

新增一个云厂商的完整步骤：写一个实现上述契约的进程 → 在 `providers` 表登记 → 完事。不改 dashd。

### 3.4 Job 引擎

所有「慢、可能失败、需要重试」的操作都是 job：建实例、装 agent、部署代理、同步云资源、下发配置。

- job 持久化在 `jobs` / `job_steps`，进程重启后可恢复
- 每个 job 是一串有序 step，step 幂等，失败可从断点重试
- 状态：`pending / running / succeeded / failed / cancelled`
- 进度和日志实时推到前端（SSE）
- **HTTP 请求里禁止同步执行这些操作**，一律建 job 后立即返回 job id

### 3.5 三个边界上下文

| 上下文 | 拥有的表 | 不允许直接访问 |
|---|---|---|
| 监控 | `nodes`、`node_facts`、`sample_*`、`alert_*`、`ping_*` | 云凭据、代理配置 |
| 资产与云 | `cloud_accounts`、`credentials`、`cloud_resources`、`providers` | 时序表 |
| 代理管理 | `proxy_nodes`、`proxy_configs`、`proxy_deployments` | 时序表、云凭据（通过 job 间接用） |

共享的只有：`users`、`settings`、`audit_log`、`jobs`、以及 `nodes` 的只读引用。
跨上下文调用走各自的 service 接口，不允许 A 上下文直接 SELECT B 上下文的表。

---

## 4. 目录结构（建议）

```
dash/
├── PRINCIPLES.md
├── setup.sh
├── docs/
├── migrations/
│   ├── 0001_init.oracle.sql
│   ├── 0001_init.mysql.sql
│   └── ...
├── cmd/
│   ├── dashd/
│   └── dash-agent/
├── internal/
│   ├── db/            # 连接、事务、占位符改写
│   │   └── dialect/   # ★ 全工程仅有的方言差异（3 个模板）
│   ├── ingest/        # 指标接收 + 批处理 + rollup
│   ├── control/       # agent 连接管理、指令下发
│   ├── jobs/          # job 引擎
│   ├── provider/      # provider 契约、进程监管
│   ├── monitor/       # 监控上下文
│   ├── cloud/         # 资产与云上下文
│   ├── proxy/         # 代理管理上下文
│   ├── auth/
│   └── api/
├── agent/             # agent 专属代码（proc 采集、传输、执行器）
├── providers/
│   ├── aliyun/        # 可以是 Python
│   └── gcp/
└── web/
```

每个 `internal/*` 目录必须有 `README.md`（P1.4）。

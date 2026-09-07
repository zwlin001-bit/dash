# 03 Agent 设计

目标：**一个静态二进制，在 Alpine / Debian / Ubuntu 上直接跑，资源占用可验收地低。**

---

## 1. 构建与兼容性

```
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=..." ./cmd/dash-agent
```

- **`CGO_ENABLED=0` 是硬要求**。纯静态、无 libc 依赖，因此 musl（Alpine）与 glibc（Debian/Ubuntu）用同一份产物。
  这也是不用 `net` 包默认解析器 cgo 分支的前提——纯 Go 解析器行为在三个发行版上一致。
- 目标：`linux/amd64`、`linux/arm64`、`linux/arm/v7`
- 只支持 Linux。**不引入 gopsutil**——它为了跨 Windows/Darwin/BSD 带来大量用不上的代码和分配，
  而我们只需要读 `/proc` 和 `statfs`，自己写约 400 行即可，二进制从 ~12 MB 降到 ≤ 5 MB。
- 采集层定义为 `Collector` 接口，Linux 实现放 `agent/collect/linux/`。将来要支持别的系统，加一个实现，不改上层。

### 1.1 发行版差异清单

实际会踩到的差异只有这些，实现时逐条处理：

| 差异 | Alpine | Debian / Ubuntu | 处理 |
|---|---|---|---|
| init 系统 | OpenRC | systemd | 安装脚本探测 `/run/systemd/system` 是否存在 |
| shell | busybox ash（**不是 bash**） | bash | 安装脚本用 POSIX sh，不用 bashism（`[[ ]]`、数组、`local -n`） |
| `/proc/meminfo` 字段 | 可能缺少部分字段 | 齐全 | 缺字段按 0 处理，不 panic |
| 用户创建命令 | `adduser -S -D` | `useradd -r -s /usr/sbin/nologin` | 安装脚本分支处理 |
| `capsh` / `setcap` | 需装 `libcap` | 常有 | agent 不需要特权，无影响；dashd 需要（见 05） |
| 时区数据 | 默认无 tzdata | 有 | agent 只用 UTC 毫秒时间戳，**不依赖 tzdata** |

---

## 2. 资源预算（验收指标）

| 指标 | 上限 | 目标 | 测量方式 |
|---|---|---|---|
| 常驻 RSS | 20 MB | 12 MB | 稳定运行 1 小时后 `ps -o rss=` |
| 稳态 CPU | 0.5%（单核，5s 间隔） | 0.2% | 1 小时内 `/proc/<pid>/stat` 的 utime+stime 增量 |
| 磁盘写入 | ≤ 1 次/分钟 | 0（无流量统计需求时） | `/proc/<pid>/io` 的 `write_bytes` |
| 二进制体积 | 6 MB | 5 MB | |
| 网络出向 | ≤ 3 KB/分钟 | | |

**超标即验收不通过。**

达成手段：

```go
runtime.GOMAXPROCS(1)          // 单核足够，避免多 P 的调度与堆开销
debug.SetGCPercent(50)         // 更积极地回收，换更低的 RSS 峰值
debug.SetMemoryLimit(24 << 20) // 软上限，触发时提前 GC 而不是让堆涨上去
```

- 采集缓冲、JSON 编码 buffer **全部复用**，稳态下每轮采集的堆分配应接近 0
- 不用 `fmt.Sprintf` 拼热路径字符串
- `/proc` 文件用固定大小缓冲 `pread` 一次读完，不用 `bufio.Scanner` 逐行分配

---

## 3. 采集分级（省资源的核心）

komari 把所有指标（包括最贵的连接数统计）都放在同一个 3 秒循环里。
`/proc/net/tcp` + `/proc/net/tcp6` 在连接数上千时每次解析要几毫秒 CPU 和上百 KB 分配——
这是它 CPU 占用的主要来源。dash 按成本分三档：

| 档 | 默认周期 | 指标 | 成本 |
|---|---|---|---|
| **fast** | 5s（可配） | CPU 使用率、内存、swap、load、网卡收发速率与累计字节、uptime | 读 4 个小文件，微秒级 |
| **slow** | 60s | 磁盘用量（`statfs` 每个挂载点）、进程数（`/proc` 目录计数）、TCP/UDP 连接数 | 毫秒级，降频 12 倍后可忽略 |
| **facts** | 变更时上报，最长 30 分钟兜底 | CPU 型号、核数、内核、发行版、总内存、总磁盘、虚拟化类型、公网 IP、GPU 型号 | 只在启动和变更时算 |

- 连接数统计支持完全关闭（`--collect-conns=false`），默认**开启但走 slow 档**
- 磁盘挂载点默认排除 `tmpfs / devtmpfs / overlay / squashfs / proc / sys / cgroup*`，可用 `--include-mounts` / `--exclude-mounts` 覆盖
- 网卡默认排除 `lo / docker* / veth* / br-* / tun* / tap* / kube*`，可覆盖
- fast 档的数据每轮立即上报（约 200 字节，一次 write 系统调用，不值得批量或压缩）
- slow 档数据搭在下一个 fast 报文里一起发，不单独建包

### 3.1 CPU 使用率计算

读 `/proc/stat` 的 `cpu` 行，相邻两次采样求差：

```
busy  = user + nice + system + irq + softirq + steal
total = busy + idle + iowait
pct   = 100 * Δbusy / Δtotal
```

**不用瞬时快照，不 sleep 采样**（komari 的 gopsutil 路径在某些配置下会阻塞 1 秒）。
`steal` 计入 busy——VPS 上被宿主机抢走的时间对用户是可感知的卡顿。

### 3.2 内存

默认 `used = MemTotal - MemAvailable`（这是用户真正关心的「还剩多少可用」）。
提供 `--mem-include-cache` 切换为 `MemTotal - MemFree` 的口径。
`MemAvailable` 在极老内核上可能缺失，缺失时回退到 `MemTotal - MemFree - Buffers - Cached`。

### 3.3 容器内运行

支持 `HOST_PROC` 环境变量指向宿主机 `/proc` 挂载点。
未设置时若检测到 cgroup 限制（`/sys/fs/cgroup/memory.max`），内存总量按 cgroup 限制上报，
并在 facts 里标记 `virt=container`。

---

## 4. 状态与落盘

**agent 不带本地数据库，不缓存历史数据。**

唯一的状态文件 `/var/lib/dash-agent/state.json`：

```json
{
  "facts_hash": "…",
  "net_baseline": { "eth0": { "rx": 0, "tx": 0, "at_ms": 0 } },
  "month_anchor_ms": 0
}
```

- 只在内容变化时写，且**最多每分钟一次**
- 写法：写临时文件 → `fsync` → `rename`（原子替换）
- 丢失该文件不影响运行，只是流量基线重新计算

**日志一律写 stderr**，交给 systemd journal / OpenRC 的 logger。agent 自己不开日志文件、不做轮转。

`facts_hash` 是所有静态信息字段拼接后的哈希：只有它变化时才上报 facts，
把「每 5 分钟报一次基础信息」压缩成「基本不报」。

---

## 5. 传输

见 `04-protocol.md`。要点：

- 主通道：`wss://<domain>/api/agent/v1/rpc?token=...`，一条长连接
- 回退：WS 连续失败 3 次后转 `POST /api/agent/v1/report`，之后每 60s 尝试恢复 WS
- 心跳：30s 一次 WS ping，服务端 90s 无消息判离线
- 重连：指数退避 1s → 2s → 4s → … → 60s 封顶，带 ±20% 抖动（避免所有 agent 同时重连）
- 断线期间**不缓存上报**——监控数据的价值随时间衰减，补报旧点不值得那份内存和复杂度

---

## 6. 远程执行（默认关闭）

三态，由服务端下发的能力位 + agent 本地配置**双向确认**才生效：

| 模式 | 说明 |
|---|---|
| `off`（默认） | 拒绝一切执行类指令 |
| `actions` | 只执行动作清单里的预定义动作。动作由服务端声明（名称 + 参数 schema），agent 按名查表执行本地内置实现，**参数以 `argv` 数组传递，绝不拼接 shell 字符串** |
| `shell` | 任意命令。必须逐台在服务端显式开启，并写审计日志 |

- 代理服务器的部署、重启、配置下发走 `actions` 模式即可满足，不需要 `shell`
- 每次执行有超时（默认 300s）、输出截断（默认 256 KB）、并发上限（默认 2）
- 交互式终端（web terminal）是独立能力位，与上面三态正交，默认关闭

---

## 7. 安装与升级

```
# 服务端界面生成，一次性 enrollment token
curl -fsSL https://<domain>/install.sh | sh -s -- --enroll <token>
```

安装脚本（POSIX sh，busybox ash 可跑）做的事：

1. 探测发行版与架构，从 `https://<domain>/dl/dash-agent-linux-<arch>` 下载（**从自己的服务端下载，不走 GitHub**）
2. 校验 sha256
3. 创建非特权用户 `dashagent`（Alpine 用 `adduser -S -D`，Debian 系用 `useradd -r`）
4. 安装到 `/usr/local/bin/dash-agent`，配置 `/etc/dash-agent/config.json`（0600，属主 `dashagent`）
5. 写 systemd unit 或 OpenRC init 脚本，enable + start
6. agent 用 enrollment token 换取长期 token，写回配置文件

**agent 以非特权用户运行**：CPU / 内存 / 磁盘 / 网络 / 连接数统计都不需要 root。
只有 `shell` 模式下需要执行特权命令时才配置 sudo 规则，且规则范围显式声明。

升级：不自更新。服务端通过 `agent.upgrade` 指令下发新版本 URL + sha256，
agent 下载 → 校验 → 原子替换 → 请求 init 系统重启自己。失败则保留旧版本继续运行。

### 7.1 服务文件

systemd（Debian / Ubuntu）：

```ini
[Unit]
Description=dash agent
After=network-online.target

[Service]
Type=simple
User=dashagent
ExecStart=/usr/local/bin/dash-agent --config /etc/dash-agent/config.json
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/dash-agent
MemoryMax=64M

[Install]
WantedBy=multi-user.target
```

OpenRC（Alpine）：`supervise-daemon` 模式，`command_user=dashagent`，
`respawn_delay=5`，输出重定向到 syslog。

---

## 8. 配置项

| 键 | 默认 | 说明 |
|---|---|---|
| `endpoint` | — | `https://<domain>` |
| `token` | — | 长期 token |
| `interval_fast_s` | 5 | fast 档周期 |
| `interval_slow_s` | 60 | slow 档周期 |
| `facts_max_interval_s` | 1800 | facts 兜底上报间隔 |
| `collect_conns` | true | 是否统计连接数 |
| `include_mounts` / `exclude_mounts` | 见 §3 | |
| `include_nics` / `exclude_nics` | 见 §3 | |
| `mem_include_cache` | false | |
| `exec_mode` | `off` | `off` / `actions` / `shell` |
| `enable_terminal` | false | |
| `insecure_skip_verify` | false | 仅调试用 |
| `prefer_ip_version` | 自动 | `4` / `6` |

命令行参数、环境变量（`DASH_AGENT_*`）、配置文件三者优先级：命令行 > 环境变量 > 配置文件。

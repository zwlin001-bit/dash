# M1 dash-agent

**这是本期的重点。** 用户对上一版最不满的就是「agent 太重、部署麻烦」。
komari 的 agent 只作为思路参考，代码不移植。

设计文档：[`../03-agent.md`](../03-agent.md)、[`../04-protocol.md`](../04-protocol.md)

---

## T1.1 采集层（Linux /proc）

**交付物**
```
agent/collect/
  collector.go     type Collector interface { Code() string; Tier() Tier; Collect(*Sample) error }
  registry.go      注册表：Register(Collector)，主循环只认注册表
  linux/
    cpu.go  mem.go  load.go  net.go  disk.go  proc.go  conns.go  uptime.go  facts.go
```

**约束（逐条验收）**
- **不引入 gopsutil 或任何 psutil 类库**。直接读 `/proc`、`syscall.Statfs`
- CPU 用 `/proc/stat` 两次采样求差，`steal` 计入 busy。**不 sleep、不阻塞**
- 内存默认 `MemTotal - MemAvailable`；`MemAvailable` 缺失时回退
  `MemTotal - MemFree - Buffers - Cached`
- 每个 `/proc` 文件用**复用的固定缓冲**一次读完，不用 `bufio.Scanner`
- 采集失败的项**留空不填 0**，由上层省略字段
- `Tier` 三档：`TierFast`(5s) / `TierSlow`(60s) / `TierFacts`(变更时)
- 网卡/挂载点的默认排除规则见 `03-agent.md` §3

**验收**
- 单元测试：把 `/proc` 样本文件（含 Alpine 精简版的 `meminfo`）喂进解析函数，断言结果
- **基准测试**：`go test -bench . -benchmem`，稳态一轮 fast 采集的 `allocs/op` 必须为个位数
- 加一个新采集项只需写一个文件 + 一行 `Register()`，**不改主循环**——这条要在 PR 说明里演示

---

## T1.2 主循环与资源控制

**交付物**：`agent/runtime/`，三档定时器 + 复用的编码缓冲。

**约束**
```go
runtime.GOMAXPROCS(1)
debug.SetGCPercent(50)
debug.SetMemoryLimit(24 << 20)
```
- slow 档数据搭在下一个 fast 报文里发，**不单独建包**
- facts 只在 `facts_hash` 变化或服务端 `need_facts` 时上报，兜底 30 分钟

**验收（硬指标，超标即打回）**

| 指标 | 上限 | 测量 |
|---|---|---|
| RSS | 20 MB | 连跑 1 小时后 `ps -o rss= -p <pid>` |
| CPU | 0.5%（5s 间隔） | 1 小时内 `/proc/<pid>/stat` 的 utime+stime 增量 / 3600 |
| 磁盘写 | ≤ 1 次/分钟 | `/proc/<pid>/io` 的 `write_bytes` 增长 |
| 二进制 | ≤ 6 MB | `ls -l bin/dash-agent` |

**交付一个 `scripts/agent-bench.sh`**，跑完直接打印这四项和是否达标。验收时会直接跑它。

---

## T1.3 传输层

**交付物**：`agent/transport/`，WebSocket 主通道 + HTTP 回退。

**约束**
- 协议严格按 `04-protocol.md`，结构体来自 `internal/protocol/`（与服务端共享）
- token 放 `Authorization: Bearer`，**不放 query string**
- 重连退避 1s→2s→4s→…→60s，**带 ±20% 抖动**
- WS 连续失败 3 次转 HTTP 回退，之后每 60s 尝试恢复 WS
- **断线期间不缓存上报**——不补报旧点
- 小于 1 KB 的报文不压缩
- 收到未知 method 返回 `-32601` 并继续运行，**不崩溃**（滚动升级的前提）

**验收**
- 断网 2 分钟再恢复，agent 自动重连且内存无增长
- 服务端返回 `-32000`（token 吊销）后 agent 停止重连
- 服务端下发一个 agent 不认识的 method，agent 正常继续上报

---

## T1.4 静态构建与多架构

**交付物**：`make build-agent-all` 产出三份二进制 + `sha256sums.txt`。

```
CGO_ENABLED=0 GOOS=linux GOARCH={amd64,arm64,arm GOARM=7} \
  go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
```

**验收（三个系统都要实测，这是硬要求）**

| 系统 | 验证 |
|---|---|
| Alpine（musl） | `docker run --rm -v ...:/x alpine /x/dash-agent --version` 正常输出 |
| Debian | 同上 |
| Ubuntu | 同上 |

`ldd bin/dash-agent` 必须输出 `not a dynamic executable`。

---

## T1.5 安装脚本

**交付物**：`scripts/install-agent.sh`，由 dashd 在 `https://<domain>/install.sh` 提供。

**约束**
- **POSIX sh，busybox ash 能跑**。禁止 `[[ ]]`、数组、`local -n`、`declare` 等 bashism
- 探测发行版与架构，从 `https://<domain>/dl/...` 下载（**不走 GitHub**），校验 sha256
- 建非特权用户：Alpine `adduser -S -D dashagent`，Debian 系 `useradd -r -s /usr/sbin/nologin dashagent`
- 探测 `/run/systemd/system` 决定写 systemd unit 还是 OpenRC 脚本
- 配置文件 `0600`、属主 `dashagent`
- **幂等**：重复执行不报错，等价于升级
- 用 enrollment token 换长期 token 后写回配置

**验收**：三个系统的干净容器里各跑一次，装完 `systemctl status` / `rc-service status` 正常，
服务端能看到该节点上线；再跑一次不报错。

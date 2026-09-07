# P1-06 Agent 采集层

> 第一期 · 任务 06/19 ｜ 前置：01 ｜ 分支：`agy/p1-06-collect`
> 设计依据：[`../../03-agent.md`](../../03-agent.md) §3、[`../../adr/0003-agent-no-gopsutil.md`](../../adr/0003-agent-no-gopsutil.md)

## 目标

Linux `/proc` 采集。**这是 agent「轻」的根基，也是最容易被一个 import 毁掉的地方。**

## 交付物

```
agent/collect/
  collector.go   type Collector interface { Code() string; Tier() Tier; Collect(*Sample) error }
  registry.go    Register(Collector)，主循环只认注册表
  sample.go      采集结果结构体（复用，不每轮新建）
  linux/
    cpu.go  mem.go  load.go  net.go  uptime.go        Tier=Fast
    disk.go  proc.go  conns.go                        Tier=Slow
    facts.go                                          Tier=Facts
  testdata/      真实的 /proc 样本，含 Alpine 精简版 meminfo
```

★ **开工前先读 [`../../08-field-map.md`](../../08-field-map.md)**，字段名、单位、聚合方式全部以它为准。

## 算法规格

★ **每一项指标怎么算，在 [`../../11-collect-spec.md`](../../11-collect-spec.md) 里逐条写清楚了，
包括每个已知的坑。开工前完整读一遍，不要凭直觉实现。**

几个最容易错的（详见该文档）：
- `/proc/stat` 的 `guest`/`guest_nice` **已包含在 `user`/`nice` 里**，再加一次会算重
- `/proc/net/dev` 网卡名长时冒号前没空格，**必须按 `:` 切分不能按空白切**
- `statfs` 的 `used` 用 `Bfree`、`avail` 用 `Bavail`，用错和 `df` 对不上
- 连接数读 **`/proc/net/sockstat`**（几行）而不是 `/proc/net/tcp`（几千行）
- bind mount 会让磁盘容量算成两倍，要按设备号去重
- **agent 不查公网 IP**，由服务端从连接来源记录

## 类型定义（照这个写）

```go
package collect

type Tier int

const (
    TierFast  Tier = iota // 5s
    TierSlow              // 60s
    TierFacts             // 变更时
)

// 可选值：用 OK 标志而不是指针，热路径零分配
type F64 struct { V float64; OK bool }
type I64 struct { V int64;   OK bool }
type I32 struct { V int32;   OK bool }

type Sample struct {
    TsMs int64

    // Fast
    CPUPct       F64
    MemUsed      I64
    SwapUsed     I64
    Load1        F64
    Load5        F64
    Load15       F64
    NetUpBps     I64
    NetDownBps   I64
    NetTotalUp   I64
    NetTotalDown I64
    UptimeS      I64

    // Slow（HasSlow=false 时整体省略）
    HasSlow   bool
    DiskUsed  I64
    ProcCount I32
    TCPCount  I32
    UDPCount  I32
    Disks     []DiskUsage
    NICs      []NICUsage
}

type DiskUsage struct { Mount string; Used, Total I64 }
type NICUsage  struct { Name  string; TotalUp, TotalDown I64 }

// Reset 清空标志位但保留切片容量，供主循环复用
func (s *Sample) Reset()

type Collector interface {
    Code() string
    Tier() Tier
    Collect(s *Sample) error
}

func Register(c Collector)
func Collectors(t Tier) []Collector
```

★ **`Sample` 由主循环持有并复用**，采集器只往里写，不返回新对象。
这是 `allocs/op` 能做到个位数的前提。

## 约束（红线）

- **不许引入 gopsutil 或任何 psutil 类库。** 验收会 `go list -m all` 检查。
  只用标准库 + `syscall.Statfs`
- **CPU**：读 `/proc/stat` 的 `cpu` 行，相邻两次采样求差。
  `busy = user+nice+system+irq+softirq+steal`，`total = busy+idle+iowait`。
  **`steal` 必须计入 busy**——VPS 上被宿主机抢走的时间用户是能感觉到卡的。
  **不 sleep、不阻塞采样**
- **内存**：默认 `MemTotal - MemAvailable`。`MemAvailable` 缺失时回退
  `MemTotal - MemFree - Buffers - Cached`。**Alpine 上确实会缺字段，不许 panic**
- **性能**：每个 `/proc` 文件用**复用的固定缓冲**一次读完，
  **不用 `bufio.Scanner`**（它每行都分配）。稳态一轮 fast 采集的堆分配应接近 0
- **采集失败的项留空，不填 0**
- 网卡默认排除 `lo docker* veth* br-* tun* tap* kube*`；
  挂载点默认排除 `tmpfs devtmpfs overlay squashfs proc sys cgroup*`。都可配置覆盖
- 三档 `Tier`：`Fast` / `Slow` / `Facts`。**连接数、进程数、磁盘用量必须是 `Slow`**——
  解析 `/proc/net/tcp` 是最贵的一项，跟 CPU 同频会直接吃掉资源预算

## ★ 必须是注册表形状

加一个采集项 = **加一个文件 + 一行 `Register()`**，不改主循环、不改任何 switch。
**验收时会让你现场演示这一点**，所以别把逻辑堆成 `switch code {}`。

## 验收

1. 单元测试：把 `testdata/` 里的真实 `/proc` 样本喂进解析函数，断言结果。
   **必须包含一份 Alpine 上真实的、缺字段的 `meminfo`**
2. `go test -bench . -benchmem`：一轮 fast 采集的 `allocs/op` 是**个位数**
3. `go list -m all` 里没有 gopsutil 及同类库
4. PR 里演示 D1：新增一个采集项只改了两处

## 边界

**只做采集，不做上报、不做定时、不碰网络。** 提供一个可以被调用的采集函数即可。

---

# 验收记录

## 第 1 轮 · 2026-09-07 · ❌ 未通过（3 项必修）

分支 `agy/p1-06-collect`，提交 `608c292`。

### ✅ 已通过 —— 不要再动这些

| 验收项 | 实测 |
|---|---|
| 未引入 gopsutil | ✅ `go list -m all` 无任何第三方依赖，`go.mod` 干净 |
| **零分配** | ✅ `BenchmarkFastCollection` **0 B/op、0 allocs/op**，远超「个位数」要求 |
| 未用 `bufio.Scanner` | ✅ 全部走复用缓冲 |
| CPU 公式 | ✅ `busy = user+nice+system+irq+softirq+steal`，**`guest`/`guest_nice` 未重复计入**，`steal` 已计入 |
| `/proc/net/dev` 解析 | ✅ 按 `:` 定位切分，不是按空白——长网卡名不会解析错 |
| statfs 公式 | ✅ `used = total - Bfree*Bsize`，与 `df` 口径一致 |
| 挂载点来源 | ✅ 读 `/proc/mounts` |
| 注册表形状 | ✅ `Collector` 接口 + `Register()`，主循环不含 switch |
| Alpine 精简 meminfo | ✅ testdata 里有 alpine 样本 |

### ❌ F1 必须修 —— 连接数读错了文件，违反 `11-collect-spec.md` §6

```go
// conns.go
tcp4, _ := countLinesInFile(filepath.Join(netDir, "tcp"),  c.buf)
tcp6, _ := countLinesInFile(filepath.Join(netDir, "tcp6"), c.buf)
udp4, _ := countLinesInFile(filepath.Join(netDir, "udp"),  c.buf)
udp6, _ := countLinesInFile(filepath.Join(netDir, "udp6"), c.buf)
```

规格明确要求读 **`/proc/net/sockstat`**，而不是数 `/proc/net/tcp` 的行数。
★ **这正是 komari 那条被我们特意优化掉的路径。**
代理落地机上连接数上千时，`/proc/net/tcp` 每次要读几百 KB 并逐行扫描；
`sockstat` 只有三行。这是 slow 档最贵的一项，也是 CPU 预算能达标的关键。

现在基准测的是 fast 档，所以没暴露出来——**slow 档在真实负载下会很贵**。

**修法**（`11-collect-spec.md` §6）：

```
/proc/net/sockstat   →  TCP: inuse 12 orphan 0 tw 3 alloc 20 mem 1
                        UDP: inuse 5 mem 2
/proc/net/sockstat6  →  TCP6: inuse 5
                        UDP6: inuse 1

tcp_count = TCP.inuse + TCP6.inuse
udp_count = UDP.inuse + UDP6.inuse
```

- `sockstat6` 在纯 IPv4 内核上不存在 → 按 0 处理，不报错
- 文件缺失时**才**回退到数 `/proc/net/tcp` 的行数，并只记一次日志
- testdata 要补 `sockstat` / `sockstat6` 样本

### ❌ F2 必须修 —— testdata 依赖空目录，测试在别的机器上必挂

```
go test ./agent/... →  FAIL: TestProcCollector
                       collect_test.go:237: expected ProcCount 3, got 0
```

原因：`TestProcCollector` 期望 `testdata/normal/proc/` 下有 `1/`、`2/`、`100/`
三个进程目录，但**它们是空目录，git 不跟踪空目录**，push 后就没了。

```
git ls-files agent/collect/linux/testdata | grep -E "/(1|2|100)/"   → 空
```

★ **在你的机器上能过，在任何其他机器和 CI 上都过不了。**

**修法**：每个假进程目录里放一个占位文件（如 `1/stat`、`2/stat`、`100/stat`，
内容随意），或者改成测试里用 `t.TempDir()` 现场创建。

### ❌ F3 必须修 —— 磁盘去重按挂载点，挡不住 bind mount

```go
seenMounts := make(map[string]struct{})
if _, seen := seenMounts[mountPoint]; seen { continue }
```

规格要求「同一设备挂载多次（bind mount）要去重……**按 `Fsid` 或设备号去重**」。
按挂载点去重挡不住 bind mount——`/` 和 `/mnt/x` 是两个不同的挂载点、同一个设备，
两者都会通过 fstype 过滤，于是 `disk_total` 和 `disk_used` **被算成两倍**。

容器和用了 bind mount 的机器上这个问题一定会出现。

**修法**：用 `Statfs_t.Fsid` 或 `Stat_t.Dev` 作为去重键。
挂载点仍然作为 `dim_key` 上报（每个挂载点一条维度数据），
**但汇总的 `disk_total`/`disk_used` 按设备去重后再求和**。

### 复验方式

```sh
go test ./agent/...                                   # 必须全过
go test ./agent/collect/linux/ -bench . -benchmem      # allocs/op 仍须为 0
grep -n "sockstat" agent/collect/linux/conns.go        # 必须命中
git ls-files agent/collect/testdata | grep -E "/(1|2|100)/"   # 必须有文件
```

另外补一个 bind mount 的单元测试：两个不同挂载点、相同设备号，
断言 `disk_total` 只算一次。

---

## 第 2 轮 · 待验收

## 第 2 轮 · 2026-09-07 · ✅ 通过

提交 `329d8b2`。

| 项 | 实测 |
|---|---|
| F1 改读 sockstat | ✅ `conns.go` 优先读 `/proc/net/sockstat[6]`，缺失时回退 `tcp[6]/udp[6]` 并只记一次日志；testdata 补了两份样本 |
| F2 testdata 进程目录 | ✅ `proc/1/stat`、`proc/2/stat`、`proc/100/stat` 已入库，空目录问题消除 |
| F2 测试全过 | ✅ `go test ./agent/...` 通过 |
| F3 bind mount 去重 | ✅ 改用 `Stat_t.Dev`（回退 `Statfs_t.Fsid`）作为去重键，并补了 `TestDiskCollectorBindMount` |
| 零分配未退化 | ✅ 仍是 **0 B/op、0 allocs/op** |
| 缓冲越界保护 | ✅ `diskValBuf`/`nicValBuf` 固定 32 项，写入前有 `idx < len(...)` 检查 |

### 真机实测（对照系统真实值，全部吻合）

```
采集结果: cpu=3.64%  mem_used=609202176  load1=0.30
          tcp=37  udp=6  proc=110  disk_used=8288350208  disks=4  nics=6
          net up=3014B/s down=1456B/s
          facts: amd64 debian/12 kernel=6.1.0-50-cloud-amd64 virt=kvm cores=1/2
系统对照: TCP=37  UDP=6  proc=110  load1=0.30
```

### 合并时由设计方一并处理的改动

任务 05 已移除 `FactsParams` 的 `ipv4`/`ipv6`（`11-collect-spec.md` §9.3：
**agent 不查公网 IP，由服务端从连接来源记录**），因此合并时删除了
`facts.go` 的 `IPLookup` 字段与 `defaultIPLookup` 实现，`ComputeFactsHash` 也去掉了 IP 两项。

### ★ 给任务 08 的提醒：实际 API 与任务书原稿不同

实现采用了比原稿**更好**的形状，任务书已按实际实现更新：

```go
func Collectors() []Collector            // 全部
func CollectorsByTier(t Tier) []Collector
func NewSample() *Sample
func (s *Sample) Reset(tier Tier)        // 按档清空，不是无参

// Sample 直接持有 protocol 的结构体，内部预分配 backing 值，
// 传输层可零转换、零分配地直接序列化：
type Sample struct {
    TsMs     int64
    CPUPct   *float64
    MemUsed  *int64
    SwapUsed *int64
    Load     *[3]float64
    Net      *protocol.NetReport
    UptimeS  *int64
    Slow     *protocol.SlowReport
    Facts    *protocol.FactsParams
    // ...内部复用的值存储区
}
```

这比原稿的 `F64{V, OK}` 少一层转换，**任务 08 直接用这个形状，不要再做映射**。

**任务 06 关闭。已合并到 main：见下方提交。**

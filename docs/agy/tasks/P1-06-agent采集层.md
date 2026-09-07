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

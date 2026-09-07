# agent/collect

Agent 系统指标与硬件信息采集层。

## 职责边界

- **纯 Linux `/proc` 与 `/sys` 读取采集**（CPU、内存、网络、系统负载、运行时间、磁盘、进程数、连接数、静态 Facts 等）。
- **绝对禁止引入 gopsutil 或任何 psutil 类库**，以满足体积 ≤ 5MB 与常驻 RSS ≤ 20MB 的硬性约束。
- 采用采集项注册表模式，新增采集项只需新增文件并在 `init()` 调用一次 `Register()`，不改动主循环或任何 `switch`。
- 实现分级采集策略（`Tier`：`Fast` / `Slow` / `Facts`）。昂贵采集项（连接数、进程数、磁盘用量）必须为 `Slow`。
- 保证高效率、低内存分配（复用固定缓冲读取，通过 pread/固定缓冲零堆分配，不用 `bufio.Scanner`），稳态下 fast 采集堆分配为 0 allocs/op。
- 采集失败或首轮无基线的项留空（指针为 `nil`），绝不填 0。

## 文件结构

```
agent/collect/
  collector.go   type Collector interface { Code() string; Tier() Tier; Collect(*Sample) error }
  registry.go    注册表接口（Register / Collectors / CollectorsByTier / CollectTier）
  sample.go      可复用采集结果结构体 Sample，预分配存储区避免临时指针逃逸
  linux/
    util.go      pread 读缓冲与无分配数值解析工具
    cpu.go       CPU 使用率采集（两次采样求差，steal 计入 busy）            [TierFast]
    mem.go       内存与 Swap 采集（支持 Alpine 缺少 MemAvailable 回退）       [TierFast]
    load.go      系统负载采集 (1m, 5m, 15m)                               [TierFast]
    net.go       网络聚合流量与单网卡累计采集（自动排除 lo, docker*, veth* 等）   [TierFast]
    uptime.go    开机运行秒数采集                                          [TierFast]
    disk.go      各有效挂载点已用与总量采集 (statfs，排除伪文件系统)            [TierSlow]
    proc.go      系统进程数采集 (/proc 数字目录计数)                         [TierSlow]
    conns.go     TCP/UDP 连接数采集 (/proc/net/tcp[6], udp[6])             [TierSlow]
    facts.go     静态硬件与系统信息采集，计算 facts_hash (除 boot_at_ms)     [TierFacts]
  testdata/      真实 /proc 样本（含 Alpine 缺字段 meminfo）
```

## 对外接口

- `Tier` 枚举：`TierFast`、`TierSlow`、`TierFacts`。
- `Collector` 契约接口：
  ```go
  type Collector interface {
      Code() string
      Tier() Tier
      Collect(*Sample) error
  }
  ```
- 注册表接口：`Register(c Collector)`、`Collectors() []Collector`、`CollectorsByTier(t Tier) []Collector`、`CollectTier(t Tier, sample *Sample) error`。
- 样本载体：`Sample` 结构体（支持 `Reset`、`Set*`、`AddDisk`、`AddNIC`、`ToMetricsParams`）。

## 依赖谁

- 标准库（`os`, `syscall`, `math`, `crypto/sha256` 等）。
- `internal/protocol`（用于数据结构契约）。
- **严禁依赖任何服务端 internal 包（如 internal/db、api、control 等）。**

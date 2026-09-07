# agent/collect

Agent 系统指标与硬件信息采集。

## 职责边界

- **纯 Linux `/proc` 与 `/sys` 读取采集**（CPU、内存、网络、磁盘、系统负载等）。
- **绝对禁止引入 gopsutil 或任何 psutil 类库**，以满足体积 ≤ 5MB 与常驻 RSS ≤ 20MB 的硬性约束。
- 采用采集项注册表模式，新增采集项只需新增文件并调用一次 `Register()`，不改动主循环。
- 实现分级采集策略（`Tier`：`Fast` / `Slow` / `Facts`）。昂贵采集项（连接数、进程数、磁盘用量）必须为 `Slow`。
- 保证高效率、低内存分配（复用固定缓冲读取，不用 `bufio.Scanner`），不落盘本地历史数据。
- 采集失败的项留空，绝不填 0。

## 对外接口

- `Collector` 契约接口：
  ```go
  type Collector interface {
      Code() string
      Tier() Tier
      Collect(*Sample) error
  }
  ```
- 注册表接口：`Register(c Collector)`
- 样本载体：`Sample` 结构体（复用，避免每轮分配）

## 依赖谁

- 标准库（及 `syscall.Statfs`）。
- `internal/protocol`（用于数据结构契约）。
- **严禁依赖任何服务端 internal 包（如 internal/db、api、control 等）。**

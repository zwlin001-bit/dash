# agent/collect

Agent 系统指标与硬件信息采集。

## 职责边界

- **纯 Linux `/proc` 与 `/sys` 读取采集**（CPU、内存、网络、磁盘、系统负载等）。
- **绝对禁止引入 gopsutil 等重量级跨平台库**，以满足体积 ≤ 5MB 与常驻 RSS ≤ 20MB 的硬性约束。
- 实现分级采集策略（快速档 5s 采集轻量指标，慢速档 60s 采集进程数/连接数/磁盘占用等昂贵指标）。
- 保证高效率、低内存分配，不写任何本地历史数据缓存。

## 对外接口

- `Collector` 接口。
- `CollectFast() (*protocol.FastMetrics, error)`
- `CollectSlow() (*protocol.SlowMetrics, error)`
- `CollectFacts() (*protocol.Facts, error)`

## 依赖谁

- 标准库。
- `internal/protocol`（仅用于序列化上报数据结构）。
- **严禁依赖任何服务端 internal 包（如 internal/db、api、control 等）。**

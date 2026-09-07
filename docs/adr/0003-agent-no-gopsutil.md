# ADR 0003 Agent 不用 gopsutil，直接读 /proc

**状态**：已采纳 · 2026-09-07

## 背景
komari-agent 用 gopsutil 采集，为的是同时支持 Linux / Windows / macOS / FreeBSD。
dash 的目标环境明确只有 **Alpine / Debian / Ubuntu**，全是 Linux。

资源占用是本项目的硬性验收指标（RSS ≤ 20 MB、稳态 CPU ≤ 0.5%）。

## 决定
自己写 Linux `/proc` 采集，约 400 行，放在 `agent/collect/linux/`，
上层通过 `Collector` 接口调用。

理由：
- 二进制从 ~12 MB 降到 ≤ 5 MB（gopsutil 带进大量 Windows WMI / Darwin cgo 分支）
- 热路径可以自己控制分配：固定缓冲一次读完 `/proc/stat`，稳态零分配；
  gopsutil 每次调用返回新分配的结构体切片
- 避开 gopsutil 某些配置下 CPU 采样会 sleep 1 秒的行为
- 采集逻辑本身很简单，`/proc` 接口是内核稳定 ABI，不存在维护风险

## 代价
- 将来要支持 Windows/macOS 需要另写实现。接口已经留好，但确实是额外工作。
  鉴于目标场景是 VPS，这个风险可以接受。
- `/proc` 各字段在极老内核上可能缺失（如 `MemAvailable`），需要显式的回退逻辑。

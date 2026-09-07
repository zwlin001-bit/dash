# P1-08 Agent 主循环与资源预算

> 第一期 · 任务 08/19 ｜ 前置：06、07 ｜ 分支：`agy/p1-08-agent-runtime`
> 设计依据：[`../../03-agent.md`](../../03-agent.md) §2、§3、§4、§8

## 目标

把采集层和传输层组装成一个跑得起来的 agent。
**这个任务的验收是四个硬性数字，不达标直接打回。**

## 交付物

```
agent/runtime/    三档定时器、复用的编码缓冲、状态文件
cmd/dash-agent/   命令行参数、环境变量、配置文件
scripts/agent-bench.sh   ★ 一条命令打印四项指标和是否达标
```

## ★ 资源预算（验收指标）

| 指标 | 上限 | 目标 | 测量 |
|---|---|---|---|
| 常驻 RSS | **20 MB** | 12 MB | 连跑 1 小时后 `ps -o rss=` |
| 稳态 CPU | **0.5%** 单核（5s 间隔） | 0.2% | 1 小时内 `/proc/<pid>/stat` 的 utime+stime 增量 |
| 磁盘写入 | **≤ 1 次/分钟** | 0 | `/proc/<pid>/io` 的 `write_bytes` |
| 二进制体积 | **≤ 6 MB** | 5 MB | `ls -l bin/dash-agent` |

## 约束

- 这三行必须有：
  ```go
  runtime.GOMAXPROCS(1)
  debug.SetGCPercent(50)
  debug.SetMemoryLimit(24 << 20)
  ```
- 三档周期：fast 5s、slow 60s、facts 变更时上报（兜底 30 分钟）
- **slow 档数据搭在下一个 fast 报文里发，不单独建包**
- **facts 只在 `facts_hash` 变化时上报。** 把「每 5 分钟报一次基础信息」压成「基本不报」
- JSON 编码缓冲**复用**，不用 `fmt.Sprintf` 拼热路径字符串
- 状态文件 `/var/lib/dash-agent/state.json`：**最多每分钟写一次**，
  写临时文件 → `fsync` → `rename` 原子替换。丢失不影响运行
- **日志写 stderr**，不开日志文件、不做轮转
- **采集参数以服务端 `agent.hello` 应答下发的为准**，本地配置只是服务端不可达时的兜底
- 配置项与优先级见 `03-agent.md` §8：命令行 > 环境变量（`DASH_AGENT_*`）> 配置文件

## 验收

1. **`scripts/agent-bench.sh` 四项全绿**，PR 里贴实际输出
2. 连续运行 1 小时无内存增长趋势
3. facts 不变时，1 小时内只上报了启动那一次 + 兜底两次
4. 服务端下发新的 `interval_fast_s`，agent 立即生效，**不需要重连**

## 边界

不做安装脚本（任务 18）、不做多架构构建（任务 09）。

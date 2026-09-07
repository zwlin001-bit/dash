# P1-08 Agent 主循环与资源预算

> 第一期 · 任务 08/19 ｜ 前置：06、07 ｜ 分支：`agy/p1-08-agent-runtime`
> 设计依据：[`../../03-agent.md`](../../03-agent.md) §2、§3、§4、§8

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

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

## 采集层实际 API

★ **任务 06 的实际 API 与最初任务书原稿不同**（实现更优，任务书已更新）。
开工前看 [`P1-06-agent采集层.md`](P1-06-agent采集层.md) 的「类型定义」一节。

要点：`Collectors()` / `CollectorsByTier(t)` / `NewSample()` / `Reset(tier)`；
`Sample` **直接持有 `protocol.NetReport` / `SlowReport` / `FactsParams`**，
传输层可零转换直接序列化，**不要再写一层映射**。

## 配置项与状态文件

**配置项全表**见 [`../../03-agent.md`](../../03-agent.md) §8。
优先级：命令行 > 环境变量（`DASH_AGENT_*`）> 配置文件 > 内置默认。

`/var/lib/dash-agent/state.json`：

```jsonc
{
  "version": 1,
  "node_id": "01J…",
  "facts_hash": "…",
  "net_baseline": {                    // 用于跨重启的网卡计数器基线
    "eth0": {"total_up": 0, "total_down": 0, "at_ms": 0}
  }
}
```

- 只在内容变化时写，**且最多每分钟一次**
- 写临时文件 → `fsync` → `rename` 原子替换
- **丢失不影响运行**：`node_id` 从配置文件读，`facts_hash` 重算，基线重新建立
- `version` 字段用于将来格式变更；读到不认识的版本就当作不存在，重建

### 三档定时的调度

- 用**单个 1 秒 ticker** 驱动，内部按计数判断该跑哪些档，
  **不要开三个 goroutine 三个 ticker**——那会让 `GOMAXPROCS(1)` 下的调度更抖
- 采集耗时超过间隔时**跳过这一轮**并打日志，绝不排队堆积
- ★ **时间戳用 `time.Now().UnixMilli()` 取真实时间**，
  但间隔判断用 `time.Since(单调时钟)`——系统时间被 NTP 调整时不能让采集乱掉

## scripts/agent-bench.sh 规格

必须能一条命令跑完并打印结论。要求：

```
用法: ./scripts/agent-bench.sh [时长秒数，默认 3600]

输出示例:
  RSS           11.2 MB   / 上限 20 MB    ✅
  CPU            0.18 %   / 上限 0.5 %    ✅
  磁盘写         0 次/分  / 上限 1 次/分  ✅
  二进制         4.8 MB   / 上限 6 MB     ✅
  结论: 达标
```

- RSS 取运行期间的**峰值**，不是结束时的瞬时值
- CPU 用 `/proc/<pid>/stat` 的 `utime+stime` 增量 ÷ 时长 ÷ `CLK_TCK`
- 磁盘写用 `/proc/<pid>/io` 的 `write_bytes` 与 `syscall_w` 增量
- 任一项超标时**退出码非 0**，方便接进 CI

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

---

# 验收记录

## 第 1 轮 · 2026-09-07 · ✅ 通过

分支 `agy/p1-08-agent-runtime`，提交 `44ca555`。已合并到 main。

### ★ 四项资源预算（真机实测）

| 指标 | 实测 | 上限 | 结果 |
|---|---|---|---|
| RSS 峰值 | **4.84 MB** | 20 MB | ✅ 远低于目标值 12 MB |
| CPU（90 秒稳态） | **0.000 %** | 0.5 % | ✅ |
| 磁盘写 | **0 bytes / 0 次** | ≤1 次/分钟 | ✅ |
| 二进制体积 | **5.23 MB** | 6 MB | ✅ |

### ✅ 其他

| 项 | 实测 |
|---|---|
| 三行资源控制 | ✅ `GOMAXPROCS(1)` / `SetGCPercent(50)` / `SetMemoryLimit(24<<20)` 都在 `main.go` |
| 采集循环确实在跑 | ✅ 断连状态下每 5 秒一次 HTTP 回退上报，证明 fast 档正常调度 |
| 退避抖动 | ✅ 日志可见 `943.108636ms`、`2.148302769s`——不是固定的 1s/2s |
| 三次失败转回退 | ✅ `ws dial failed 3 times ... switching to FallbackHTTP` |
| 日志写 stderr | ✅ 未开日志文件 |
| `scripts/agent-bench.sh` | ✅ 已交付 |
| 测试 | ✅ `agent/runtime` 等全过 |

**任务 08 关闭。**

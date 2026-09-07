# 06 三网测速模块

**状态：本期只交付「可用的最小实现 + 完整的数据/协议契约」，把扩展点预留好。**

目的：衡量一台 VPS 到中国大陆三大运营商的网络质量，用来判断这台机器值不值得留、
适不适合做代理落地。

---

## 1. 概念

| 概念 | 说明 |
|---|---|
| **目标（target）** | 一个可测的对端。属于某个运营商归类，带一种探测方式和一个端点 |
| **运营商归类（isp_kind）** | `telecom` 电信 / `unicom` 联通 / `mobile` 移动 / `custom` 自定义 |
| **探测方式（probe_kind）** | `tcping` 延迟丢包 / `http_down` 下行带宽 / `http_up` 上行带宽 / `trace` 回程路由 |
| **测速任务（task）** | 「对哪些节点、用哪些目标、按什么周期跑」 |
| **测速结果（run）** | 一次「某节点 × 某目标」的执行记录 |

**三个内置归类（电信、联通、移动）由迁移脚本预置，用户不可删除、可停用、可改端点。**
**自定义归类下可以加任意多个目标**，字段与内置完全一致——内置只是 `is_builtin=1` 的普通行，
不走任何特殊代码路径。

---

## 2. 表结构

```
speedtest_targets(
  id, isp_kind str(16), name str(64),
  probe_kind str(16), endpoint str(255),
  max_bytes i64 NULL,          -- http_down/up 的传输上限，防止跑爆流量
  max_duration_s i32,          -- 单次探测硬超时
  probe_count i32 NULL,        -- tcping 的探测次数
  is_builtin bool, is_enabled bool, display_order i32,
  note text NULL, created_at_ms, updated_at_ms)
  ix_st_targets_isp (isp_kind, is_enabled)

speedtest_tasks(
  id, name str(64), is_enabled bool,
  scope_kind str(16), scope_ref str(26) NULL,   -- all / group / tag / node
  target_ids text,               -- 逗号分隔的 target id 列表（不做 join，应用层解析）
  interval_s i32 NULL,           -- 周期执行；NULL 表示仅手动触发
  next_run_at_ms ts NULL,
  created_at_ms, updated_at_ms)

speedtest_runs(
  id, node_id, target_id, task_id NULL, job_id NULL,
  run_state str(16),             -- pending / running / ok / failed / timeout
  latency_ms f64 NULL, jitter_ms f64 NULL, loss_pct f64 NULL,
  down_bps i64 NULL, up_bps i64 NULL,
  bytes_moved i64 NULL, duration_ms i32 NULL,
  hops_json text NULL,           -- trace 结果
  error_msg text NULL,
  started_at_ms ts, ended_at_ms ts NULL, created_at_ms)
  ix_st_runs_node (node_id, started_at_ms)
  ix_st_runs_target (target_id, started_at_ms)
```

**趋势数据同时写入窄表**（`metric_series` + `sample_dim`），走已有的 rollup 与保留期机制：

| metric_code | dim_key |
|---|---|
| `st.latency_ms` | target id |
| `st.loss_pct` | target id |
| `st.down_bps` | target id |
| `st.up_bps` | target id |

这样测速趋势图复用监控图表的全部代码，不另起一套。
`speedtest_runs` 保留 90 天（明细，含错误信息），趋势靠窄表长期留存。

### 2.1 内置目标种子数据

迁移脚本预置每个运营商 **一组** 目标（一个 `tcping` + 一个 `http_down`），
端点值放在迁移里、可在界面改。**不要把具体 IP/URL 硬编码进 Go 代码。**
端点失效时用户自己改，不需要发版。

---

## 3. Agent 侧

独立模块 `agent/speedtest/`，能力位 `speedtest`，**默认关闭**，由服务端能力协商开启。

硬性约束（否则会直接违反 `03-agent.md` 的资源预算）：

1. **只在收到服务端指令时执行**，绝不进入 fast/slow 采集循环
2. **同一时刻最多一个探测在跑**，排队，不并发
3. `http_down` 必须同时受 `max_bytes` 和 `max_duration_s` 双重限制，**先到先停**
4. 下载数据**直接丢弃**（读进固定大小的复用缓冲区再扔），不落盘、不进堆
5. 探测期间不影响正常上报——上报走独立 goroutine 和独立缓冲
6. 探测结束后主动 `debug.FreeOSMemory()`，把带宽测试撑起来的堆还给系统
7. `tcping` 用 TCP 连接建立耗时，**不用 ICMP**——非特权用户发不了 raw socket，
   而 agent 默认非 root 运行（`03-agent.md` §7）

### 3.1 各探测方式的算法

**tcping**：对 `host:port` 连续建立 `probe_count` 次 TCP 连接（默认 10 次，间隔 200ms），
记录每次三次握手耗时。`latency_ms` 取中位数，`jitter_ms` 取相邻样本差值的平均，
`loss_pct` = 失败次数 / 总次数。**连上就立刻关闭，不发任何数据。**

**http_down**：`GET endpoint`，丢弃响应体，统计有效传输窗口内的字节数。
去掉前 1 秒的 TCP 慢启动窗口再算速率，取剩余窗口的平均值。
`down_bps = bytes_in_window * 8 / window_seconds`。

**http_up**：`POST endpoint`，发送重复的固定缓冲区（不预生成大 buffer）。
多数公开测速点不支持，因此**上行测速是可选项，失败不算任务失败**。

**trace**：本期只留字段和协议位，不实现。

---

## 4. 协议

在 `04-protocol.md` v1 基础上增加：

**Server → Agent**（request）

```json
{"jsonrpc":"2.0","id":42,"method":"server.speedtest","params":{
  "run_id": "01JC…",
  "probes": [
    {"target_id":"01JB…","probe_kind":"tcping","endpoint":"1.2.3.4:443",
     "probe_count":10,"max_duration_s":15},
    {"target_id":"01JB…","probe_kind":"http_down","endpoint":"https://…/100mb.bin",
     "max_bytes":52428800,"max_duration_s":20}
  ]
}}
```

**Agent → Server**（notification，每个探测完成即发一条，不等整批）

```json
{"jsonrpc":"2.0","method":"agent.speedtest_result","params":{
  "run_id":"01JC…","target_id":"01JB…",
  "ok":true,
  "latency_ms":38.2,"jitter_ms":2.1,"loss_pct":0,
  "down_bps":94200000,"bytes_moved":52428800,"duration_ms":4460,
  "started_at_ms":0,"ended_at_ms":0,
  "error":null
}}
```

逐条回传而不是攒批，是为了前端能看到进度，也避免一个探测卡住导致整批结果丢失。

---

## 5. 扩展点

新增一种探测方式的完整步骤：

1. 在 `agent/speedtest/` 实现 `Probe` 接口并注册到探测表
2. 在 `speedtest_targets.probe_kind` 里用新值
3. 结果字段不够用时，加列 + 一条迁移

**不需要改协议**（`probe_kind` 是字符串，参数走 `params` 里的自由字段），
**不需要改服务端调度逻辑**。agent 遇到不认识的 `probe_kind` 返回
`-32002 能力未开启`，不崩溃。

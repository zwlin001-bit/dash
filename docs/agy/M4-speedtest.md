# M4 三网测速模块

设计文档：[`../06-speedtest.md`](../06-speedtest.md)

用户要求：**预置电信、联通、移动三个选项，外加自定义（自定义可以有多个）。**

---

## T4.1 目标管理

**交付物**：`speedtest_targets` 的 CRUD API + 界面。

**约束**
- 内置的电信/联通/移动由 `migrations/0001_init.*.sql` 预置，`is_builtin=1`
- **内置不可删除，但可停用、可改端点**——端点失效时用户自己改，不需要发版
- 自定义归类（`isp_kind='custom'`）下**可加任意多个**目标
- 内置与自定义**走完全相同的代码路径**，`is_builtin` 只影响「能不能删」这一条规则
- **具体 IP / URL 不许硬编码进 Go 代码**，只能在迁移的种子数据里

**验收**：能新增 3 个自定义目标；删除内置目标被拒绝；停用内置目标后调度不再下发它。

---

## T4.2 Agent 探测模块

**交付物**
```
agent/speedtest/
  probe.go      type Probe interface { Kind() string; Run(ctx, spec) (Result, error) }
  registry.go
  tcping.go  http_down.go  http_up.go
```

**约束（违反会直接击穿 M1 的资源预算，重点检查）**
1. **只在收到服务端指令时执行**，绝不进入 fast/slow 采集循环
2. **同一时刻最多一个探测**，排队不并发
3. `http_down` 同时受 `max_bytes` 与 `max_duration_s` 限制，**先到先停**
4. 下载数据读进**复用的固定缓冲区后直接丢弃**，不落盘、不进堆
5. 探测期间正常上报不受影响（独立 goroutine + 独立缓冲）
6. 探测结束调用 `debug.FreeOSMemory()`，把撑起来的堆还给系统
7. `tcping` 用 **TCP 握手耗时，不用 ICMP**——agent 非 root 运行，发不了 raw socket
8. `http_up` 失败**不算任务失败**（多数公开测速点不支持上行）
9. `trace` 本期不实现，遇到返回 `-32002`

算法细节（延迟取中位数、下行去掉前 1 秒慢启动窗口等）见 `06-speedtest.md` §3.1。

**验收**
- 跑一次 50MB 的 `http_down`，探测期间 agent RSS 峰值 **≤ 40 MB**，结束后 60 秒内回落到 20 MB 以内
- 探测全程正常上报不中断、不丢点
- `max_bytes` 生效：把上限设成 1MB，实际传输不超过 1MB
- 同时下发 3 个探测，确认是串行执行的

---

## T4.3 调度与结果

**交付物**：`internal/speedtest/`——任务调度、结果入库、趋势写窄表。

**约束**
- 结果明细进 `speedtest_runs`（保留 90 天）
- 趋势同时写 `metric_series` + `sample_dim`（`st.latency_ms` / `st.loss_pct` /
  `st.down_bps` / `st.up_bps`），**复用监控的 rollup 与图表代码，不另起一套**
- 每个探测完成即回传一条，**不攒批**
- 周期任务通过 `speedtest_tasks.interval_s` 调度；`NULL` 表示仅手动触发

**验收**：手动触发一次全量测速，三个运营商 + 自定义目标的结果都入库；
趋势图能画出来，用的是监控模块同一套图表组件。

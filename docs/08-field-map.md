# 08 指标字段总表

**这是跨层的唯一对照表。** 一个指标从 `/proc` 到图表要经过五层，
每层都有自己的名字，任何一层对不上就是一个静默的 bug。

任务 04（建表）、05（协议）、06（采集）、11（落库）、12（rollup）、13（查询）、15（图表）
**必须以本表为准**。并行开发时，这张表是防止各层跑偏的唯一保障。

**新增一个主机级指标 = 在本表加一行 + 五处各改一处。** 这就是扩展的标准动作。

---

## 1. 主机级指标（宽表 `sample_host`）

### 1.1 Fast 档（默认 5 秒）

| 采集源 | 协议字段 | `sample_host` 列 | 类型/单位 | rollup 聚合 |
|---|---|---|---|---|
| `/proc/stat` cpu 行两次采样差值 | `cpu_pct` | `cpu_pct` | f64 / % | avg · max · min |
| `/proc/meminfo` `MemTotal-MemAvailable` | `mem_used` | `mem_used` | i64 / bytes | avg · max · min |
| `/proc/meminfo` `SwapTotal-SwapFree` | `swap_used` | `swap_used` | i64 / bytes | avg · max · min |
| `/proc/loadavg` 第 1 项 | `load[0]` | `load1` | f64 | avg · max · min |
| `/proc/loadavg` 第 2 项 | `load[1]` | `load5` | f64 | avg · max · min |
| `/proc/loadavg` 第 3 项 | `load[2]` | `load15` | f64 | avg · max · min |
| `/proc/net/dev` 发送字节差值 ÷ 间隔 | `net.up_bps` | `net_up_bps` | i64 / bytes/s | avg · max · min |
| `/proc/net/dev` 接收字节差值 ÷ 间隔 | `net.down_bps` | `net_down_bps` | i64 / bytes/s | avg · max · min |
| `/proc/net/dev` 发送字节累计 | `net.total_up` | `net_total_up` | i64 / bytes | **max**（桶内单调，等于 last） |
| `/proc/net/dev` 接收字节累计 | `net.total_down` | `net_total_down` | i64 / bytes | **max** |
| **服务端计算，agent 不上报** | — | `traffic_up` | i64 / bytes | **sum** |
| **服务端计算，agent 不上报** | — | `traffic_down` | i64 / bytes | **sum** |
| `/proc/uptime` 第 1 项 | `uptime_s` | `uptime_s` | i64 / 秒 | **last** |

`traffic_up/down` 由服务端从 `net_total_*` 算增量得到，
公式和重启回绕处理见 `02-database.md` §5.7。**agent 不参与，也不该参与。**

### 1.2 Slow 档（默认 60 秒，搭在下一个 fast 报文的 `slow` 字段里）

| 采集源 | 协议字段 | `sample_host` 列 | 类型/单位 | rollup 聚合 |
|---|---|---|---|---|
| 各挂载点 `statfs` 已用求和 | `slow.disk_used` | `disk_used` | i64 / bytes | avg · max · min |
| `/proc` 下数字目录计数 | `slow.proc_count` | `proc_count` | i32 | avg · max · min |
| `/proc/net/tcp` + `tcp6` 行数 − 表头 | `slow.tcp_count` | `tcp_count` | i32 | avg · max · min |
| `/proc/net/udp` + `udp6` 行数 − 表头 | `slow.udp_count` | `udp_count` | i32 | avg · max · min |

**slow 档字段整体可选。** 不是 slow 周期的那一轮，协议里整个 `slow` 对象不出现。
服务端遇到不带 `slow` 的报文，对应列写 NULL，**不是写 0**。

### 1.3 Rollup 列命名

`sample_host_1m` / `_1h` / `_1d` 三张表列形状一致：

```
node_id, bucket_ms, sample_cnt,
<列>_avg  <列>_max  <列>_min        -- 聚合方式为 avg·max·min 的
<列>_last                           -- 聚合方式为 max/last 的（net_total_*、uptime_s）
<列>_sum                            -- 聚合方式为 sum 的（traffic_*）
```

★ **逐级聚合（`_1m`→`_1h`→`_1d`）的 avg 必须加权**：
`SUM(x_avg * sample_cnt) / SUM(sample_cnt)`。直接 `AVG(x_avg)` 是错的。

---

## 2. 维度指标（窄表 `metric_series` + `sample_dim`）

不定数量的维度不能进宽表。第一期只有两类：

| metric_code | dim_key | 采集源 | 单位 | 档 |
|---|---|---|---|---|
| `disk.used` | 挂载点路径，如 `/` | `statfs` | bytes | Slow |
| `disk.total` | 挂载点路径 | `statfs` | bytes | Slow |
| `nic.total_up` | 网卡名，如 `eth0` | `/proc/net/dev` | bytes | Slow |
| `nic.total_down` | 网卡名 | `/proc/net/dev` | bytes | Slow |

协议里放在 `slow.disks[]` / `slow.nics[]`，元素形如 `{"k":"/","used":…,"total":…}`。

第二期的 `net.rtt_ms` / `net.loss_pct`（Ping）、`st.*`（测速）沿用同一张窄表，
**不另建时序表**。

---

## 3. Facts 字段（`node_facts`）

只在 `facts_hash` 变化时上报，兜底 30 分钟。

| 采集源 | 协议字段 | `node_facts` 列 |
|---|---|---|
| `runtime.GOARCH` | `arch` | `arch` |
| `/etc/os-release` 的 `ID` | `os_name` | `os_name` |
| `/etc/os-release` 的 `VERSION_ID` | `os_version` | `os_version` |
| `syscall.Uname` 的 release | `kernel` | `kernel` |
| DMI / cpuinfo hypervisor 标志推断 | `virt` | `virt` |
| `/proc/cpuinfo` `model name` | `cpu_model` | `cpu_model` |
| `/proc/cpuinfo` `core id` 去重计数 | `cpu_cores` | `cpu_cores` |
| `/proc/cpuinfo` `processor` 计数 | `cpu_threads` | `cpu_threads` |
| `/proc/meminfo` `MemTotal` | `mem_total` | `mem_total` |
| `/proc/meminfo` `SwapTotal` | `swap_total` | `swap_total` |
| 各挂载点 `statfs` 总量求和 | `disk_total` | `disk_total` |
| 网卡地址或外部查询 | `ipv4` / `ipv6` | `ipv4` / `ipv6` |
| `/proc/stat` 的 `btime` × 1000 | `boot_at_ms` | `boot_at_ms` |

**总量（`mem_total`/`swap_total`/`disk_total`）放 facts，不放每次采样。**
它们几乎不变，每 5 秒重复存一遍是纯浪费。图表算占比时前端用 facts 里的总量。

`facts_hash` = 上表所有字段（除 `boot_at_ms`）按固定顺序拼接后的哈希。
**`boot_at_ms` 不参与哈希**，否则每次重启都会触发一次全量上报——那正是我们想避免的。

---

## 4. 查询 API 响应结构（= 图表组件入参）

★ **任务 13 的返回和任务 15 的图表组件 props 是同一个结构。
这两个任务如果并行开发，必须都以本节为准。**

用列式而不是对象数组：400 个点 × 8 个字段，列式比对象数组小 5 倍左右，
前端画图也是直接要列。

```json
{
  "node_id": "01JBX…",
  "source": "sample_host_1m",
  "from_ms": 1757200000000,
  "to_ms":   1757221600000,
  "step_ms": 60000,
  "ts_ms":   [1757200000000, 1757200060000, ...],
  "series": {
    "cpu_pct":  [3.5, 4.1, null, ...],
    "mem_used": [412000000, 418000000, 415000000, ...]
  }
}
```

约定：

- `source` 回带实际用了哪张表，**便于验证选表逻辑是否正确**，也方便排查
- `step_ms` 是桶宽；raw 表回 0（不等距）
- **缺失点用 `null`，不用 0。** 前端遇到 `null` 断线，不连成一条假的直线
- `series` 的 key 与 §1 的「`sample_host` 列」完全一致——
  raw 用列名本身，rollup 表用哪个聚合由 `fields` 参数指定（如 `cpu_pct:max`），
  但返回的 key 统一去掉后缀，回到列名
- `ts_ms` 与每个 series 数组**等长**，一一对应

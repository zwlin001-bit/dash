# 11 采集算法规格

**任务 06 的实现依据。** `08-field-map.md` 说的是「哪个字段对到哪」，
本文件说的是「这个数怎么算出来的」。

每一项都给出：读哪个文件、取哪个字段、怎么算、有什么坑。
**不要凭直觉实现，这里每条坑都是真会踩到的。**

---

## 1. CPU 使用率 — `/proc/stat`

第一行：`cpu  user nice system idle iowait irq softirq steal guest guest_nice`（单位 USER_HZ）

```
busy  = user + nice + system + irq + softirq + steal
total = busy + idle + iowait
pct   = 100 * (busy₂ - busy₁) / (total₂ - total₁)
```

**坑**：
- ★ `guest` 和 `guest_nice` **已经分别包含在 `user` 和 `nice` 里了**，再加一次会算重
- `steal` **必须计入 busy**——VPS 上被宿主机抢走的时间用户是能感觉到卡的
- 首次采集没有前一次样本，**跳过这一轮不上报 `cpu_pct`**（省略字段，不是报 0）
- `total₂ - total₁ == 0` 时省略字段，不要除零

## 2. 内存 — `/proc/meminfo`

**单位是 kB，必须 ×1024 转成字节。**

```
默认:   mem_used = MemTotal - MemAvailable
回退:   mem_used = MemTotal - MemFree - Buffers - Cached     （MemAvailable 缺失时）
swap:   swap_used = SwapTotal - SwapFree
```

**坑**：
- ★ **Alpine 的精简内核可能没有 `MemAvailable`**，回退逻辑必须有，且**不许 panic**
- `mem_include_cache=true` 时用 `MemTotal - MemFree`
- `SwapTotal` 为 0 时 `swap_used` 报 0（这是真实的 0，不是缺失）

## 3. 负载 — `/proc/loadavg`

`0.12 0.09 0.05 1/234 5678` → 取前三个字段。

## 4. 网络 — `/proc/net/dev`

跳过前 2 行表头。每行：`  eth0: <rx_bytes> <rx_packets> ... <tx_bytes> <tx_packets> ...`

冒号后第 **1** 个字段是接收字节，第 **9** 个是发送字节。

```
net_total_up   = Σ 各网卡 tx_bytes
net_total_down = Σ 各网卡 rx_bytes
net_up_bps     = (total_up₂ - total_up₁) / 间隔秒数
```

**坑**：
- ★ **网卡名长时冒号前面没有空格**：`enp0s31f6:12345678 ...`。
  **必须按 `:` 切分，不能按空白切分**——这是最经典的 `/proc/net/dev` 解析 bug
- 默认排除：`lo` `docker*` `veth*` `br-*` `tun*` `tap*` `kube*`
- 计数器回绕/网卡重置**由服务端处理**（`02-database.md` §5.7），agent 只管上报累计值
- 首次采集没有前值，省略 `up_bps`/`down_bps`，但 `total_up`/`total_down` 照报

## 5. 磁盘 — `syscall.Statfs`

对每个纳入统计的挂载点：

```
total = Blocks * Bsize
used  = (Blocks - Bfree) * Bsize      ← 与 df 的 Used 一致
avail = Bavail * Bsize
```

`disk_used` / `disk_total` = 各挂载点求和。

**坑**：
- ★ `used` 用 **`Bfree`**（含保留块），`avail` 用 **`Bavail`**（不含）。用错会和 `df` 对不上
- 默认排除文件系统类型：`tmpfs` `devtmpfs` `overlay` `squashfs` `proc` `sysfs`
  `cgroup` `cgroup2` `debugfs` `tracefs` `securityfs` `pstore` `autofs` `mqueue` `hugetlbfs`
- **同一设备挂载多次（bind mount）要去重**，否则容量会被算成两倍。按 `Fsid` 或设备号去重
- `Statfs` 失败（挂载点不可访问）**跳过该挂载点，不中断整轮采集**
- 挂载点列表从 `/proc/self/mounts` 读，**不是 `/etc/fstab`**

## 6. 连接数 — `/proc/net/sockstat`（不是 `/proc/net/tcp`）

★ **这是本项目相对 komari 的一个实质优化。**

komari 解析 `/proc/net/tcp` + `/proc/net/tcp6`，连接数上千时每次要读几百 KB、分配大量内存。
`/proc/net/sockstat` 只有几行：

```
sockets: used 312
TCP: inuse 12 orphan 0 tw 3 alloc 20 mem 1
UDP: inuse 5 mem 2
```

`/proc/net/sockstat6`：`TCP6: inuse 5` / `UDP6: inuse 1`

```
tcp_count = TCP.inuse + TCP6.inuse
udp_count = UDP.inuse + UDP6.inuse
```

**坑**：
- `inuse` **不含 TIME_WAIT**（`tw` 是单独字段）。这是有意的：
  TIME_WAIT 数量抖动大、参考价值低。**在界面上标注清楚口径**，否则用户会拿它和 `ss -s` 对不上
- `sockstat6` 在纯 IPv4 内核上不存在 → 按 0 处理，不报错
- 文件缺失时回退到数 `/proc/net/tcp` 的行数，并在日志里提示一次

## 7. 进程数

数 `/proc` 下的纯数字目录。用 `os.ReadDir` 一次读完，**不要对每个再 `Stat`**。

## 8. Uptime — `/proc/uptime`

`12345.67 98765.43` → 第一个字段，取整成秒。

## 9. Facts

| 字段 | 来源 |
|---|---|
| `arch` | `runtime.GOARCH` |
| `os_name` / `os_version` | `/etc/os-release` 的 `ID` / `VERSION_ID`（去引号） |
| `kernel` | `syscall.Uname` 的 `Release` |
| `cpu_model` | `/proc/cpuinfo` 的 `model name`；ARM 上常缺失，回退 `Hardware` / `/proc/device-tree/model`，再回退 `unknown` |
| `cpu_threads` | `/proc/cpuinfo` 里 `processor` 行数 |
| `cpu_cores` | `(physical id, core id)` 去重计数；缺失时等于 `cpu_threads` |
| `mem_total` / `swap_total` | `/proc/meminfo`（×1024） |
| `disk_total` | 各挂载点 `Blocks * Bsize` 求和 |
| `boot_at_ms` | `/proc/stat` 的 `btime` 行 × 1000 |
| `virt` | 见 §9.1 |
| `ipv4` / `ipv6` | ★ **agent 不上报，由服务端填**，见 §9.3 |

### 9.1 虚拟化识别

按顺序判断，命中即停：

1. `/.dockerenv` 存在，或 `/proc/1/cgroup` 含 `docker`/`lxc`/`kubepods`/`containerd` → `container`
2. `/sys/hypervisor/type` 存在 → 读其内容（`xen` 等）
3. `/sys/class/dmi/id/sys_vendor` + `product_name` 匹配：
   `QEMU`→`kvm`、`VMware`→`vmware`、`VirtualBox`→`virtualbox`、
   `Xen`→`xen`、`Amazon EC2`→`aws`、`Google`→`gcp`、`Microsoft Corporation`→`hyperv`、
   `Alibaba Cloud`→`aliyun`
4. `/proc/cpuinfo` 的 `flags` 含 `hypervisor` → `vm`
5. 都不命中 → `physical`

DMI 文件在非 root 下**可能读不到**，读失败就跳到下一步，不报错。

### 9.2 `facts_hash`

除 `boot_at_ms`、`ipv4`、`ipv6` 外的所有 facts 字段，**按本文件表格的顺序**
用 `\x1f` 连接，取 SHA-256，十六进制前 32 位。

★ **`boot_at_ms` 不参与**——否则每次重启都触发一次全量 facts 上报，
而「基本不报 facts」正是省资源的手段之一。

### 9.3 IP 地址由服务端记录

★ **agent 不做任何公网 IP 查询。**

- 不访问 ipify 之类的第三方服务（引入外部依赖 + 出网失败风险 + 隐私）
- 不从网卡取（NAT 后面拿到的是内网地址，没有意义）
- **服务端从 WebSocket 连接的 `RemoteAddr` 记录**，准确、零成本、天然处理 NAT
- 反代后面要正确读 `X-Forwarded-For` / `X-Real-IP`，且**只信任本机反代**

---

## 10. 性能要求

- 每个 `/proc` 文件用**复用的固定缓冲**一次 `Read` 读完（`/proc` 文件都很小，
  `sockstat` 几百字节、`stat` 几 KB）。**不用 `bufio.Scanner`**——它每行都分配
- 解析用 `bytes.IndexByte` + `strconv.ParseUint` 在切片上直接做，**不做 `string()` 转换**
- `Sample` 结构体由主循环持有并复用，采集器只往里写
- 稳态一轮 fast 采集的 `allocs/op` **必须是个位数**

---

## 11. 采集失败的统一处理

| 情况 | 做法 |
|---|---|
| 文件不存在 | 该字段**省略**（不是 0），记一次日志，之后不再重复记 |
| 解析失败 | 同上 |
| 单个挂载点/网卡失败 | 跳过它，其余继续 |
| 首次采集缺前值（CPU、网速） | 省略该字段，`total_*` 类照报 |

**绝不允许因为某一项采集失败而中断整轮采集或让进程退出。**

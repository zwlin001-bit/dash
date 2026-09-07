# P1-06 Agent 采集层

> 第一期 · 任务 06/19 ｜ 前置：01 ｜ 分支：`agy/p1-06-collect`
> 设计依据：[`../../03-agent.md`](../../03-agent.md) §3、[`../../adr/0003-agent-no-gopsutil.md`](../../adr/0003-agent-no-gopsutil.md)

## 目标

Linux `/proc` 采集。**这是 agent「轻」的根基，也是最容易被一个 import 毁掉的地方。**

## 交付物

```
agent/collect/
  collector.go   type Collector interface { Code() string; Tier() Tier; Collect(*Sample) error }
  registry.go    Register(Collector)，主循环只认注册表
  sample.go      采集结果结构体（复用，不每轮新建）
  linux/
    cpu.go  mem.go  load.go  net.go  uptime.go        Tier=Fast
    disk.go  proc.go  conns.go                        Tier=Slow
    facts.go                                          Tier=Facts
  testdata/      真实的 /proc 样本，含 Alpine 精简版 meminfo
```

## 约束（红线）

- **不许引入 gopsutil 或任何 psutil 类库。** 验收会 `go list -m all` 检查。
  只用标准库 + `syscall.Statfs`
- **CPU**：读 `/proc/stat` 的 `cpu` 行，相邻两次采样求差。
  `busy = user+nice+system+irq+softirq+steal`，`total = busy+idle+iowait`。
  **`steal` 必须计入 busy**——VPS 上被宿主机抢走的时间用户是能感觉到卡的。
  **不 sleep、不阻塞采样**
- **内存**：默认 `MemTotal - MemAvailable`。`MemAvailable` 缺失时回退
  `MemTotal - MemFree - Buffers - Cached`。**Alpine 上确实会缺字段，不许 panic**
- **性能**：每个 `/proc` 文件用**复用的固定缓冲**一次读完，
  **不用 `bufio.Scanner`**（它每行都分配）。稳态一轮 fast 采集的堆分配应接近 0
- **采集失败的项留空，不填 0**
- 网卡默认排除 `lo docker* veth* br-* tun* tap* kube*`；
  挂载点默认排除 `tmpfs devtmpfs overlay squashfs proc sys cgroup*`。都可配置覆盖
- 三档 `Tier`：`Fast` / `Slow` / `Facts`。**连接数、进程数、磁盘用量必须是 `Slow`**——
  解析 `/proc/net/tcp` 是最贵的一项，跟 CPU 同频会直接吃掉资源预算

## ★ 必须是注册表形状

加一个采集项 = **加一个文件 + 一行 `Register()`**，不改主循环、不改任何 switch。
**验收时会让你现场演示这一点**，所以别把逻辑堆成 `switch code {}`。

## 验收

1. 单元测试：把 `testdata/` 里的真实 `/proc` 样本喂进解析函数，断言结果。
   **必须包含一份 Alpine 上真实的、缺字段的 `meminfo`**
2. `go test -bench . -benchmem`：一轮 fast 采集的 `allocs/op` 是**个位数**
3. `go list -m all` 里没有 gopsutil 及同类库
4. PR 里演示 D1：新增一个采集项只改了两处

## 边界

**只做采集，不做上报、不做定时、不碰网络。** 提供一个可以被调用的采集函数即可。

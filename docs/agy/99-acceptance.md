# 验收清单

Claude 验收时逐条对照本表。**每一条都是二值的：通过 / 不通过。**

## A. 原则符合性

| # | 检查项 | 依据 |
|---|---|---|
| A1 | `internal/`（除 `db/dialect`）和 `agent/` 下无 Oracle 方言、无裸 `LIMIT`/`FETCH`/`ON DUPLICATE` | P2、T0.2 |
| A2 | 方言差异只出现在 `internal/db/dialect` 的三个模板里，没有第四处 | P2.9 |
| A3 | 每条迁移都有 `.oracle.sql` 和 `.mysql.sql` 两份，且表列一致 | P2.12、T0.3 |
| A4 | 无触发器/存储过程/序列/物化视图/分区表/DB 定时任务 | P2.2 |
| A5 | 时间列全部是 `*_ms` 的整数，无 `DATE`/`TIMESTAMP` 列 | P2.4 |
| A6 | 主键全部是应用生成的 ULID，无 IDENTITY/AUTO_INCREMENT/SEQUENCE | P2.3 |
| A7 | 标识符全小写、不加引号、≤30 字符、不撞保留字 | P2.7 |
| A8 | 每个 `internal/*`、`agent/*` 目录有 `README.md` | P1.4 |
| A9 | `docs/` 与代码一致：表结构、协议、配置项三处逐一核对 | P1.1-1.3 |
| A10 | `.secrets/` 未被提交；日志与代码中无明文密码/token | P6、T0.4 |

## B. Agent 资源（硬指标）

| # | 检查项 | 上限 |
|---|---|---|
| B1 | 常驻 RSS（跑满 1 小时） | 20 MB |
| B2 | 稳态 CPU（5s 间隔，1 小时均值） | 0.5% 单核 |
| B3 | 磁盘写入频率 | ≤ 1 次/分钟 |
| B4 | 二进制体积 | ≤ 6 MB |
| B5 | `ldd bin/dash-agent` | `not a dynamic executable` |
| B6 | 未引入 gopsutil 或同类库 | `go list -m all` 检查 |
| B7 | fast 采集一轮的 `allocs/op` | 个位数 |
| B8 | 测速期间 RSS 峰值 / 结束后 60s 回落 | ≤ 40 MB / ≤ 20 MB |

直接跑 `scripts/agent-bench.sh`，它应该把 B1-B4 一次性打印出来。

## C. 三系统兼容

| # | 检查项 |
|---|---|
| C1 | Alpine / Debian / Ubuntu 干净容器里 agent 都能跑起来并上报 |
| C2 | 三个系统的 `install-agent.sh` 都能装上，服务能开机自启 |
| C3 | 重复执行安装脚本不报错（幂等） |
| C4 | Alpine 走 OpenRC、Debian 系走 systemd，都被正确探测 |
| C5 | `setup.sh install` 在三个系统上都能完成部署 |

## D. 可扩展性（演示驱动）

要求在 PR 说明里**实际演示**这三件事，不是口头声明：

| # | 检查项 |
|---|---|
| D1 | 新增一个采集项：只加一个文件 + 一行 `Register()`，主循环和协议未改 |
| D2 | 新增一个下发动作：只加一个文件 + 一行注册，协议未改 |
| D3 | 新增一种测速探测方式：只加一个文件 + 一行注册，协议和服务端调度未改 |

## E. 功能

| # | 检查项 |
|---|---|
| E1 | 30 个模拟 agent 上报 10 分钟，行数正确，无丢批 |
| E2 | 数据库断开 30 秒，恢复后写入继续，实时视图全程可用 |
| E3 | 模拟机器重启（计数器归零），流量增量不出现负值或异常巨值 |
| E4 | rollup 幂等，逐级 avg 加权正确 |
| E5 | 保留期清理生效，无长事务 |
| E6 | 四个查询跨度分别命中 raw/1m/1h/1d，点数 ≤400，< 500ms |
| E7 | token 吊销后 agent 停止重连 |
| E8 | 未注册的动作名被拒绝，参数不经过 shell 解释 |
| E9 | job 中途 kill dashd，重启后从断点继续 |
| E10 | agent 升级：正常包成功、sha256 错误包被拒绝且旧版继续运行 |
| E11 | 三网 + 自定义测速能跑通，结果入库，趋势图用监控同一套组件 |
| E12 | 内置测速目标不可删、可停用、可改端点 |
| E13 | `setup.sh install` 只问域名即完成部署，443 可访问，进程非 root |
| E14 | 在线改域名成功切换且进程未重启；改成未解析域名被拒绝 |

## F. 未实现但需预留（检查「留没留」，不检查「实不实现」）

| # | 检查项 |
|---|---|
| F1 | 云 provider 契约的表（`providers`/`cloud_accounts`/`cloud_resources`）已建 |
| F2 | 代理管理三张表已建 |
| F3 | 告警三张表已建 |
| F4 | 协议里的 terminal、shell exec、trace 位已定义，实现返回明确错误码而不是崩溃 |

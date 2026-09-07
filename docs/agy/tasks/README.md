# 第一期任务清单

**第一期只做两件事：监控信息、机器清单维护。**
参考 komari 取其核心（agent 上报），其余按 `docs/` 的设计重做。

## 怎么用

- **一个文件一个任务，按编号从 01 顺序做下去。**
- 每个任务文件是自包含的：目标、交付物、约束、验收都在里面。
  设计文档只在需要背景时才去翻。
- 每个任务开工前还要读两份通用文件（只读一次，后面就熟了）：
  - [`../../PRINCIPLES.md`](../../PRINCIPLES.md) —— 硬性约束
  - [`../03-rules.md`](../03-rules.md) —— 通用工程约定（依赖纪律、测试、日志、提交规范）
- **一次只做一个任务。** 不要顺手做后面的。

## 任务表

| # | 任务 | 前置 | 产出物大致范围 |
|---|---|---|---|
| 01 | [工程骨架与依赖纪律](P1-01-工程骨架.md) | 无 | `go.mod` `Makefile` 目录结构 |
| 02 | [配置与日志](P1-02-配置与日志.md) | 01 | `internal/config` `internal/logx` |
| 03 | [数据库访问层](P1-03-数据库访问层.md) | 01 | `internal/db` + `dialect` + 方言 lint |
| 04 | [迁移框架与建表](P1-04-迁移与建表.md) | 03 | `internal/migrate` `migrations/0001_*` |
| 05 | [协议契约](P1-05-协议契约.md) | 01 | `internal/protocol` |
| 06 | [Agent 采集层](P1-06-agent采集层.md) | 01 | `agent/collect` |
| 07 | [Agent 传输层](P1-07-agent传输层.md) | 05 | `agent/transport` |
| 08 | [Agent 主循环与资源预算](P1-08-agent主循环.md) | 06, 07 | `agent/runtime` `cmd/dash-agent` |
| 09 | [Agent 静态构建与三系统验证](P1-09-agent构建.md) | 08 | 构建矩阵 + 验证报告 |
| 10 | [服务端接入与在线状态](P1-10-服务端接入.md) | 04, 05 | `internal/control` |
| 11 | [批量落库与流量计数](P1-11-批量落库.md) | 10 | `internal/ingest` |
| 12 | [Rollup 与保留期](P1-12-rollup与保留期.md) | 04 | `internal/ingest/rollup` |
| 13 | [时序查询 API](P1-13-查询api.md) | 04 | `internal/api/metrics` |
| 14 | [机器清单 API](P1-14-机器清单api.md) | 04, 02 | `internal/inventory` |
| 15 | [前端脚手架与图表组件](P1-15-前端脚手架.md) | 01 | `web/` |
| 16 | [节点列表与详情页](P1-16-节点页面.md) | 13, 15 | `web/` |
| 17 | [机器清单管理页](P1-17-清单管理页.md) | 14, 15 | `web/` |
| 18 | [Agent 安装脚本](P1-18-agent安装脚本.md) | 09, 14 | `scripts/install-agent.sh` |
| 19 | [setup.sh 一键部署](P1-19-一键部署.md) | 04, 15 | `setup.sh` |

**里程碑**
- 完成 11 → **数据能从真机进 ADB**
- 完成 17 → **界面上能看监控和管机器**
- 完成 19 → **一条命令部署**

## 第一期不做

指令下发、三网测速、告警与通知、Job 引擎、阿里云/GCP、代理管理、
web terminal、agent 自升级下发。这些的设计已经在 `docs/` 里，第二期再排。

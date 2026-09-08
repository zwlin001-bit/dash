# P2-02 凭据管理与阿里云 Provider

> 第二期 · 任务 02 ｜ 前置：无（可与 P2-01、P2-03 并行）｜ 分支：`agy/p2-02-aliyun-provider`

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## 目标

**把阿里云 ECS 接进来：凭据能安全存、实例能列、能启停、CDT 流量能查。**

严格实现 `01-architecture.md` §3.3 的 Provider 插件契约 v1，
表结构照 `02-database.md` §4.1。

## ❶ 凭据管理

```
credentials(id, name str(64), cred_kind str(32),      -- aliyun_ak
            enc_payload bin, enc_key_id str(32), enc_nonce str(64),
            created_at_ms, updated_at_ms)
```

- 信封加密，主密钥 `/etc/dash/master.key`（0600，`setup.sh` 已生成），**绝不入库**
- ★ **写入后任何接口都不许再读出明文**。API 只返回 `id`/`name`/`cred_kind`/
  `created_at_ms` 和一个掩码后的指纹（如 `LTAI****3f2a`）
- ★ **明文只在 provider 进程内存里存在**，dashd 通过 RPC 把凭据传给 provider，
  用完即弃，不写临时文件、不进环境变量（`/proc/<pid>/environ` 可读）
- 删凭据前检查有没有 `cloud_accounts` 在用，有就拒绝

## ❷ Provider 用 Go 写，独立二进制

`PRINCIPLES.md` P4.3 允许 Python，但**本 provider 用 Go**，理由写进 `adr/`：

- P5 要求「一条命令部署」。Python provider 要带 venv + 阿里云 SDK，
  在 Alpine 上还要编译 cryptography，**把部署从一条命令变成一堆坑**
- 阿里云 Go SDK 的 `CommonRequest` 能覆盖本任务全部三个 API，
  能力上不比 Python SDK 差

★ **产物是独立二进制 `bin/dash-provider-aliyun`，不是编进 dashd。**
进程边界与 RPC 契约必须保留——这是「加一个云厂商不改核心代码」的前提。
`setup.sh` 一并安装到 `/usr/local/bin/`。

★ **依赖纪律**：只引 `github.com/aliyun/alibaba-cloud-sdk-go/sdk` 核心包，
**用 `CommonRequest` 手工构造请求**，不许 `import` 任何 `sdk/service/*` 子包 ——
那些包每个都上万行生成代码，会让二进制膨胀几十 MB。
`scripts/lint-imports.sh` 里加一条规则挡住。

## ❸ 三个只读查询 + 两个动作

| 用途 | API | 域名 | 版本 |
|---|---|---|---|
| 账号级公网流量 | `ListCdtInternetTraffic` | `cdt.aliyuncs.com` | `2021-08-13` |
| 实例状态 | `DescribeInstances` | `ecs.<region>.aliyuncs.com` | `2014-05-26` |
| 实例账单 | `DescribeInstanceBill` | 见下 | `2017-12-14` |
| 启动 | `StartInstance` | ecs | |
| 停止 | `StopInstance` | ecs | |

★ **三条必须记牢的事实**（借鉴自 aliyun-guard 的实现经验，已核对官方语义）：

1. **CDT 流量是账号级的，不是实例级的。** 同一对 AK/SK 下所有实例共享这个数字。
   ★ **每个账号每轮只查一次并缓存**，不许按实例循环查，否则又慢又触发限流。
   返回的 `TrafficDetails[].Traffic` 单位是字节，要自己求和
2. **`DescribeInstances` 单次最多 100 个实例**，且必须按 `(凭据, Region)` 分组调用。
   跨 region 不能合并成一个请求
3. **BSS 账单的 endpoint 按账号归属地区分**：
   中国站 `business.aliyuncs.com`，国际站 `business.ap-southeast-1.aliyuncs.com`。
   ★ 这一项要做成云账号上的一个配置字段，**不要猜**

### 失败隔离

★ **BSS 账单查询失败绝不能影响 CDT 与 ECS。**
账单只是展示信息，流量和状态才是决策依据。
BSS 报 `NoPermission` 或 endpoint 错误时，返回结构里带上 `bill_error` 字段照常返回，
调用方继续工作。

### 重试

网络类错误（超时、连接重置、5xx）重试 3 次，退避 1s/4s/15s。
★ **鉴权错误、参数错误、限流以外的 4xx 不重试** —— 重试只会更快打到限流。

## ❹ RPC 契约

实现 `01-architecture.md` §3.3 的方法：
`provider.describe`、`provider.healthcheck`、`resource.list`、`resource.get`、
`resource.action`、`job.poll`、`event.log`。

`resource.action` 支持 `start` / `stop`，**异步返回 job handle**。
`resource.list` 返回归一化对象，`attrs` 里放阿里云特有字段
（`instance_charge_type`、`internet_max_bandwidth_out` 等）。

★ **provider 不碰数据库。** 持久化全在 dashd。

## ❺ 服务端与界面

- `cloud_accounts` / `cloud_resources` 表按 `02-database.md` §4.1 建
- API：云账号 CRUD、`POST /api/v1/cloud-accounts/{id}/sync` 触发同步、资源列表
- ★ **同步是耗时操作，走 P2-03 的 Job 引擎**，HTTP 立即返回 job id
- ★ **自动发现**：给定 AK/SK + region 列表，扫出账号下全部 ECS 供勾选导入，
  比让用户一个个填实例 ID 强得多
- 界面：云账号列表页 + 资源列表页

## ❻ RAM 权限最小化

★ **在界面和 README 里写清楚需要的最小权限**，不要让用户图省事挂 `AdministratorAccess`：

| 权限 | 用途 |
|---|---|
| `ecs:DescribeInstances` / `ecs:StartInstance` / `ecs:StopInstance` | 查状态、启停。**建议用 Resource 条件限定到具体实例** |
| `AliyunCDTReadOnlyAccess` | 查流量 |
| `AliyunBSSReadOnlyAccess` | 查账单（可选） |

## 验收

1. 存一对 AK/SK → **任何 API 都读不出明文**，`credentials` 表里 `enc_payload` 是密文
2. `ps aux`、`/proc/<provider pid>/environ`、`/proc/<pid>/cmdline` 里**搜不到 SK**
3. 自动发现能扫出账号下的真实 ECS 实例
4. 手动 `resource.action start` / `stop` → **阿里云控制台上实例真的启停了**
5. CDT 流量查出来的数字与阿里云控制台「CDT 用量」一致（误差在统计延迟内）
6. ★ 把 BSS 权限故意去掉 → CDT 和 ECS **仍然正常**，返回里带 `bill_error`
7. 断网 → 重试 3 次后失败，日志里**无 AK/SK 明文**
8. `ls -la bin/dash-provider-aliyun` 体积 **< 30 MB**（依赖纪律的直接体现）
9. `make lint` 通过，含新增的 SDK 子包 import 禁令
10. 20 个实例跨 3 个 region → **DescribeInstances 调用次数 = 3，CDT 调用次数 = 1**

## 边界

- 不做实例创建 / 销毁 / 改配（`resource.create` / `delete` 先返回未实现）
- 不做 GCP
- 不做保活逻辑（那是 P2-04）

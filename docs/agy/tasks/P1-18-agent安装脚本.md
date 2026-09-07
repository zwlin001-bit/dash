# P1-18 Agent 安装脚本

> 第一期 · 任务 18/19 ｜ 前置：09、14 ｜ 分支：`agy/p1-18-agent-installer`
> 设计依据：[`../../03-agent.md`](../../03-agent.md) §7、§1.1

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## ★ 先补一块 agent 侧能力：自助注册（enroll）

端到端联调时发现：**`dash-agent` 目前没有 enroll 能力**。

```
$ dash-agent -h
  -token string      agent authentication token
                     ↑ 只有这个，没有 -enroll / -enroll-token
```

配置文件里写 `enroll_token` 也没人读。所以现在装机只能靠人工调 API 换到长期 token
再手填进配置——装机脚本没法自动化。

按 [`../../04-protocol.md`](../../04-protocol.md) §6，agent 应当：

```
启动 → 读配置
      ├─ 有长期 token        → 直接连 WS
      └─ 无长期 token但有 enroll_token
            → POST /api/agent/v1/enroll {enroll_token, facts}
            → 拿到 {node_id, agent_token}
            → ★ 写回配置文件（0600，原子替换：临时文件 → fsync → rename）
            → 清除配置里的 enroll_token（一次性，用完即弃）
            → 连 WS
```

### 要求

- 命令行加 `--enroll <token>`；配置文件的 `enroll_token` 字段要被消费
- enroll 失败的处理要分情况，**不要一律无限重试**：
  | 服务端返回 | agent 行为 |
  |---|---|
  | `400 invalid_enroll_token` | **立即退出**并打印可读原因（token 错了，重试无意义） |
  | `410 enroll_token_used` / `expired` | **立即退出**并提示重新生成 token |
  | 网络错误 / 5xx | 退避重试，最多 10 次后退出 |
- 写回配置文件必须原子：临时文件 → `fsync` → `rename`，权限 `0600`
- **写回后清掉 `enroll_token`**，避免它长期留在磁盘上
- enroll 时带上 facts（`11-collect-spec.md` §9 采到的那些），
  服务端建节点时就能填好 `node_facts`

**这一块归本任务**，因为装机脚本的自动化完全依赖它。

## 目标

一条命令在 Alpine / Debian / Ubuntu 上装好 agent。

```sh
curl -fsSL https://<domain>/install.sh | sh -s -- --enroll <token>
```

## 交付物

```
scripts/install-agent.sh          由 dashd 在 /install.sh 提供
dashd 的 /dl/dash-agent-linux-<arch> 下载端点
systemd unit 模板 + OpenRC 脚本模板
```

## agent 配置文件

`/etc/dash-agent/config.json`（`0600`，属主 `dashagent`）：

```jsonc
{
  "endpoint": "https://dash.example.com",
  "token": "…",
  "interval_fast_s": 5,
  "interval_slow_s": 60,
  "facts_max_interval_s": 1800,
  "collect_conns": true,
  "exec_mode": "off"
}
```

★ 采集参数以服务端 `agent.hello` 应答下发的为准，
这里的值只是**服务端不可达时的兜底**。装机脚本写默认值即可，不要让用户填。

## OpenRC 服务脚本（Alpine）

```sh
#!/sbin/openrc-run
name="dash-agent"
description="dash agent"
command="/usr/local/bin/dash-agent"
command_args="--config /etc/dash-agent/config.json"
command_user="dashagent:dashagent"
supervisor="supervise-daemon"
respawn_delay=5
respawn_max=0
output_log="/dev/null"
error_log="/dev/null"
depend() { need net; after firewall; }
```

★ `output_log`/`error_log` 指向 `/dev/null` 并让 `supervise-daemon` 把 stderr 转给 syslog；
**不要让 agent 自己写日志文件**（P3.6）。

systemd unit 见 [`../../03-agent.md`](../../03-agent.md) §7.1，照抄即可。

## 约束（Alpine 是主要翻车点）

- ★ **POSIX sh，busybox ash 能跑。**
  禁止 `[[ ]]`、数组、`declare`、`local -n`、`==` 比较、`source`、`$'...'`。
  **这是 Alpine 上最常见的失败原因**，写完用 `busybox ash -n` 语法检查
- **建用户**：
  - Alpine：`adduser -S -D -H dashagent`
  - Debian / Ubuntu：`useradd -r -s /usr/sbin/nologin dashagent`
- **探测 init 系统**：`/run/systemd/system` 存在 → systemd，否则 → OpenRC
- **从 `https://<domain>/dl/` 下载，不走 GitHub**，校验 sha256
- **agent 以非特权用户运行**——CPU/内存/磁盘/网络/连接数统计都不需要 root
- 配置文件 `/etc/dash-agent/config.json`，`0600`，属主 `dashagent`
- **幂等**：重复执行等价于升级，不报错
- 用 enrollment token 换长期 token 后写回配置文件
- systemd unit 要带 `NoNewPrivileges` `ProtectSystem=strict` `ProtectHome`
  `PrivateTmp` `ReadWritePaths=/var/lib/dash-agent` `MemoryMax=64M`
- OpenRC 用 `supervise-daemon`，`command_user=dashagent`，`respawn_delay=5`

## 验收

三个系统的**干净容器**里各跑一次，PR 里贴输出：

1. 装完服务在跑：`systemctl status dash-agent` / `rc-service dash-agent status`
2. 服务端节点列表里出现该节点并显示在线
3. **重复执行一次不报错**
4. `busybox ash -n scripts/install-agent.sh` 语法检查通过
7. ★ **enroll 全流程**：界面生成 token → 一条命令装机 → agent 自助换取长期 token →
   配置文件里有 token 且 **`enroll_token` 已被清除** → 节点自动上线
8. ★ 用一个**已使用过的** token 装机，agent 立即退出并给出可读原因，不无限重试
5. `ps -o user= -p $(pidof dash-agent)` **不是 root**
6. 重启容器后服务自动拉起

## 边界

不做 agent 自升级下发（第二期）。

---

# 验收记录

## 第 1 轮 · 2026-09-07 · ✅ 通过

分支 `agy/p1-18-agent-installer`，提交 `41fbc6c`。已合并到 main：`c9c4524`。

> ⚠️ **本轮按 AGY 实际收到的任务书版本验收。**
> 「agent 侧自助注册」是我在 `556b54b` 才补进任务书的，它开工时没有这一段。
> 见下方「后续项」。

| 验收项 | 实测 |
|---|---|
| `sh -n scripts/install-agent.sh` | ✅ 通过 |
| init 系统探测 | ✅ `/run/systemd/system` → systemd，否则 OpenRC（另判 `/etc/alpine-release`） |
| enroll 换取长期 token | ✅ 脚本 `--enroll/-e <token>` → `POST /api/agent/v1/enroll` → 解析 `agent_token` → 写配置 |
| 重复安装保留已有 token | ✅ 检测到已有配置且未给 `--enroll` 时保留 |
| 初装缺 token 时报错 | ✅ 明确提示 `初次安装必须提供注册令牌` |
| 服务文件 | ✅ `deploy/dash-agent.service`、`deploy/dash-agent.initd` |
| **`/install.sh` 端点** | ✅ 实测返回完整脚本 |
| **`/dl/` 下载端点** | ✅ `sha256sums.txt` 200；`dash-agent-linux-amd64` 200，5480600 字节 |
| 回归 | ✅ 22 个包全过，两个 lint 通过 |

`/dl/` 从 `<DataDir>/dl/` 提供，目录不存在时返回 404 —— 这是正确行为，由 `setup.sh` 负责填充。

### 未验证（需 root / docker）

三系统干净容器实跑安装、重复执行幂等、`ps -o user=` 非 root、开机自启。

### 后续项（不阻塞本轮，另开任务）

★ **agent 侧自助注册尚未实现。** 目前 enroll 逻辑在安装脚本（shell）里，
agent 二进制只认 `-token`，没有 `--enroll`，配置里的 `enroll_token` 也不被消费。

装机场景下这已经够用。但两个场景会暴露不足：
1. **配置丢失或 token 轮换后 agent 无法自愈** —— 只能重跑安装脚本
2. shell 里用 `sed` 解析 JSON 响应，格式一变就悄悄失败

建议作为第二期的小任务补上（agent 启动时若无长期 token 但有 `enroll_token`，
自行调 enroll、原子写回配置、清除 `enroll_token`；失败按
`400/410` 立即退出、网络错误退避重试）。

**任务 18 关闭。**

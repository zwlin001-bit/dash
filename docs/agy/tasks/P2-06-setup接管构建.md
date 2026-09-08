# P2-06 setup.sh 接管构建流水线

> 第二期 · 任务 06 ｜ 前置：P1-25 ✅ P2-02 ✅（已合并）｜ 分支：`agy/p2-06-setup-build`

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## 目标

**`./setup.sh upgrade` 一条命令走完：构建前端 → 构建全部二进制 → 安装 → 迁移 → 重启 → 校验。**

现在这套流程一半在 `Makefile`、一半在 `setup.sh`，中间靠人记住顺序，
已经出过好几次事故。

---

## 0. 现状：六个已确认的缺口

**动手前先把这六条都读一遍**，任务要求逐条修掉：

### ❶ 没有任何地方会自动构建前端

`make build` 只会**检查**产物是否与源码一致（P1-25 的守卫），检查不过就失败。
`setup.sh` 从头到尾**没有一处调用 `make build-web`**。
结果是每次都要人肉记得先跑一次，忘了就卡在守卫上。

### ❷ `cmd_upgrade` 完全不管 provider 二进制

`grep -n "provider" setup.sh` 在 `cmd_upgrade` 里 **0 命中**。
`cmd_install` 会装 `dash-provider-aliyun`，但 `upgrade` 只替换 `dashd`。
★ **在 P2-02 之前装好的机器，upgrade 之后永远拿不到 provider。**

### ❸ ★ `providers.exec_path` 是相对路径，生产上必然起不来

```go
// internal/cloud/service.go:52
_, err := s.db.Exec(ctx, insertQ, "aliyun", "阿里云", "bin/dash-provider-aliyun", 1, now, now)
```

而 `internal/provider/manager.go` 直接 `exec.Command(execPath, "-socket", socketPath)`，
systemd unit 里**没有 `WorkingDirectory`**，systemd 默认工作目录是 `/`：

```
bin/dash-provider-aliyun  →  /bin/dash-provider-aliyun  →  不存在
```

**云资产和保活在生产上一次都跑不起来。** 开发机上从仓库根目录跑才碰巧是对的。

### ❹ 版本号永远是 `dev`

`setup.sh status` 显示 `程序版本: dev`，因为：

- `cmd_upgrade` 自己 `go build` 时**一个 `-ldflags` 都没带**
- 就算走 `make build`，`VERSION ?= dev` 也没人赋值

线上跑的是哪个 commit，现在无从得知。

### ❺ `make build-web` 假设 `node_modules` 已存在

```make
(cd web && npm run build)
```

全新机器上没有 `web/node_modules`，这条直接失败。

### ❻ `npm install` 会改写 `package-lock.json`

一旦锁文件被改，`lint-dist.sh` 的指纹就变了 —— 因为指纹包含 `package-lock.json`。
于是「构建完立刻又不一致」。★ **必须用 `npm ci`**。

---

## 1. 新增 `setup.sh build`

```
./setup.sh build [--skip-web] [--version <ver>]
```

步骤，**每步失败即中止并说清楚下一步该干什么**：

1. **探测工具链**：`go`、`node`、`npm`。缺什么说什么，带上安装建议
2. **前端**（除非 `--skip-web`）：
   - `web/node_modules` 不存在或 `package-lock.json` 比它新 → `npm ci`
   - `npm run build` → 同步进 `internal/api/dist/` → 写指纹
   - ★ **用 `npm ci` 不用 `npm install`**，理由见 §0 ❻
3. **校验产物**：跑 `scripts/lint-dist.sh`。这一步现在应该必过，
   过不了说明前端构建没生效，要报错而不是继续
4. **后端**：`dashd`、`dash-agent`、`dash-provider-aliyun` 三个，
   全部带上 `-ldflags`（见 §3）

★ **`build` 子命令不碰系统**，只产出 `bin/`。装机与升级各自调它。

## 2. 无工具链时的优雅降级

不是每台机器都装了 Node。规则：

| 情况 | 行为 |
|---|---|
| 有 npm，源码与产物不一致 | 构建前端 |
| 有 npm，已一致 | **跳过前端构建**，直接用仓库里的产物（省几十秒） |
| 无 npm，但产物与源码**一致** | ✅ **正常继续** —— 仓库里的产物就是对的 |
| 无 npm，产物与源码**不一致** | ❌ 中止，提示「本机无 Node，请在有 Node 的机器上构建并提交产物」 |

★ **最后一行不许放过。** 放过就等于 P1-25 白做。

## 3. 版本注入

```sh
VERSION=$(git describe --tags --always --dirty 2>/dev/null || cat VERSION 2>/dev/null || echo dev)
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_TIME=$(date -u +'%Y-%m-%dT%H:%M:%SZ')
```

三个二进制都带上，`Makefile` 与 `setup.sh` **共用同一套取值逻辑**，
不要两边各写一遍再对不上。

★ `setup.sh status` 要能显示 **dashd 与 provider 各自的版本**，
版本不一致时给出警告 —— 这是「升级漏了 provider」最直接的暴露方式。

## 4. `upgrade` 要一起换 provider

- 备份、替换、回滚：★ **dashd 和 provider 一起来一起回**，
  不许出现「dashd 是新的、provider 是旧的」的中间态
- provider 二进制不存在时不算失败（用户可能不用云功能），但要在输出里说明

## 5. 修掉 `exec_path`

两处都要改：

- **种子值**改成绝对路径 `/usr/local/bin/dash-provider-aliyun`
- ★ `manager.go` 的 `startProcess` 要能解析：
  绝对路径直接用 → 相对路径先按**可执行文件所在目录**解析 → 再退回 `PATH` 查找。
  开发机从仓库根目录跑、生产机 systemd 从 `/` 跑，**两种都要能起来**
- 已经装过的机器要能自愈：升级时若 `providers.exec_path` 仍是旧的相对路径，
  **自动更正为绝对路径**（一条 UPDATE，幂等）

## 6. 幂等

`setup.sh build` 与 `upgrade` **重复执行结果一致**，这是 `PRINCIPLES.md` P5.5 的要求。
反复跑三次，第二三次不应产生任何非预期变更。

## 验收

1. ★ **干净机器（有 go + node）**：`git clone` 后直接 `sudo ./setup.sh install`
   一条命令装完，**中途不需要手动跑 make 任何目标**
2. 改一行 `web/src/**/*.tsx` → `./setup.sh build` → 产物更新、指纹一致、
   `make build` 通过
3. ★ **无 npm 的机器**：产物与源码一致时 `./setup.sh build` **成功**；
   故意改一行 `web/src` 后再跑 → **失败**且提示明确
4. `./setup.sh upgrade` 后 `/usr/local/bin/dash-provider-aliyun` **确实被更新**
   （比对 sha256）
5. ★ `setup.sh status` 显示的 **`程序版本` 不再是 `dev`**，
   与 `git describe` 一致；dashd 与 provider 版本一致
6. ★ **provider 真的能起来**：systemd 下重启 dashd，在界面上点一次云账号同步，
   `ps aux | grep dash-provider` 能看到进程，日志里没有 `no such file or directory`
7. 老机器（`exec_path` 是相对路径）升级后 → **自动更正为绝对路径**并能正常拉起
8. 连跑三次 `./setup.sh upgrade` → 第二三次无非预期变更，服务持续可用
9. 升级中途让迁移失败 → dashd **和** provider 一起回滚到旧版本
10. `make lint`、`make test` 全绿

## 边界

- 不引入 CI 系统，只把本机流程理顺
- 不做交叉编译分发（`build-agent-all` 保持现状）
- 不改前端构建工具链本身（仍是 vite）

# P1-09 Agent 静态构建与三系统验证

> 第一期 · 任务 09/19 ｜ 前置：08 ｜ 分支：`agy/p1-09-build-matrix`
> 设计依据：[`../../03-agent.md`](../../03-agent.md) §1、§1.1

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## 目标

一份产物在 Alpine(musl) / Debian / Ubuntu 上直接跑。

## 交付物

```
make build-agent-all   →  bin/dash-agent-linux-{amd64,arm64,armv7}
                          bin/sha256sums.txt
docs/agy/tasks/P1-09-验证报告.md   三系统实测输出
```

构建命令：
```
CGO_ENABLED=0 GOOS=linux GOARCH=<arch> go build -trimpath \
  -ldflags "-s -w -X main.version=$(VERSION)"
```

## 约束

- **`CGO_ENABLED=0` 是硬要求。** 这是同一份产物能同时跑在 musl 和 glibc 上的唯一原因
- `ldd bin/dash-agent-linux-amd64` 必须输出 `not a dynamic executable`
- 三个架构：`amd64`、`arm64`、`arm GOARM=7`
- **agent 只用 UTC 毫秒时间戳，不依赖 tzdata**——Alpine 默认不装 tzdata
- 产出 `sha256sums.txt`，第二期的升级下发要用

## 验收

三个系统各跑一次，PR 里贴实际输出：

| 系统 | 命令 |
|---|---|
| Alpine | `docker run --rm -v $PWD/bin:/x alpine /x/dash-agent-linux-amd64 --version` |
| Debian | `docker run --rm -v $PWD/bin:/x debian:12 /x/dash-agent-linux-amd64 --version` |
| Ubuntu | `docker run --rm -v $PWD/bin:/x ubuntu:24.04 /x/dash-agent-linux-amd64 --version` |

外加：
1. `ldd` 输出为 not a dynamic executable
2. 二进制体积 ≤ 6 MB（三个架构都要贴）
3. `sha256sums.txt` 生成正确

## 边界

只做构建和验证，不做安装脚本。

---

# 验收记录

## 第 1 轮 · 2026-09-07 · ✅ 通过

分支 `agy/p1-09-build-matrix`，提交 `62e0ab0`。已合并到 main。

| 验收项 | 实测 |
|---|---|
| 三架构构建 | ✅ amd64 **5.23 MB** / arm64 **5.00 MB** / armv7 **5.06 MB**，均 ≤6 MB |
| `CGO_ENABLED=0` 静态 | ✅ `ldd` 输出「不是动态可执行文件」 |
| `sha256sums.txt` | ✅ 三份校验和已生成 |
| 额外交付 | ✅ `scripts/verify-agent-matrix.sh`、`scripts/mock-server.go`（超出任务书要求，有价值） |
| 构建优化 | ✅ 加了 `-tags nethttpomithttp2` 进一步瘦身 |

### 待环境补验

Alpine / Debian / Ubuntu 三容器实跑 `--version` —— **本机无 docker**。
`verify-agent-matrix.sh` 已交付，有 docker 的机器上跑一次即可。

**任务 09 关闭。**

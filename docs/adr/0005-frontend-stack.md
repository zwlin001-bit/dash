# ADR 0005 前端技术栈

**状态**：已采纳 · 2026-09-07

## 背景
任务 15 要搭前端脚手架，16、17 都建在它上面。之前的任务书**没有指定技术栈**，
让实施方自选会导致三个任务风格不一致，也让后续期次难以接手。

## 决定

| 层 | 选型 | 版本 |
|---|---|---|
| 框架 | **React + TypeScript** | React 18 |
| 构建 | **Vite** | 5.x |
| 样式 | **CSS 变量 + CSS Modules**（无框架） | — |
| 服务端状态 | **TanStack Query** | 5.x |
| 路由 | **React Router** | 6.x |
| 图表 | **Apache ECharts** | 5.x |

全部锁定具体版本，不用 `latest`。

## 理由

**React 而不是 Vue/Svelte**：不是技术优劣，是接手成本。生态最大、资料最多，
换人接手或让不同 agent 接力时的摩擦最小。这个项目的前端不复杂，框架差异带来的收益远小于一致性收益。

**TanStack Query 而不是手写 fetch**：任务 16 明确要求「快速切换跨度时不能出现旧响应覆盖新响应」。
请求竞态、缓存、失效、重试这些自己写一遍容易出错，而这正是 TanStack Query 的核心职责。

**ECharts 而不是 uPlot / Chart.js**：
- uPlot 更小更快，但缩放、tooltip、图例、多轴都要自己写。我们要的是四个跨度 × 多指标叠加 × 明暗主题，交互代码量不小
- ECharts 这些开箱即有，`dataZoom` 和 `tooltip.axisPointer` 直接满足需求
- 体积代价（约 1 MB）只影响首次加载，而**前端体积不在本项目的约束里**——
  `PRINCIPLES.md` P3 的体积预算只约束 agent

**CSS 变量 + CSS Modules，不用 Tailwind、不用组件库**：
用户提供了两份设计稿（`docs/agy/css/dash2.html`、`demo2.html`），
它们已经是一套成熟的语义 token 系统（`--bg-card` `--text-main` `--accent` `--ok/warn/err`），
明暗两个主题共用同一份变量名。**直接沿用它，不要再叠一层 Tailwind**——
两套样式系统并存只会互相打架。完整规格见 `docs/13-ui-spec.md`。

## 代价

- 前端产物约 1.5–2 MB（gzip 后约 500 KB），内嵌进 `dashd` 二进制。
- 没有 Tailwind 的原子类，写样式略啰嗦一些。换来的是和设计稿一比一对应，
  以及切主题时不需要维护两套类名。
  服务端二进制会到十几 MB——**用户已明确接受服务端吃资源**
- 引入 Node 构建链。`setup.sh` 不需要它（发布时前端已构建好并 embed）,
  只有开发和 CI 需要

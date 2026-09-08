# 13 界面规格

**任务 15、16、17 的实现依据。**

## 1. 参考实现（历史）

```
docs/agy/css/dash2.html    浅色主题（主）· 侧栏 288px · 顶栏 64px · 毛玻璃
docs/agy/css/demo2.html    深色主题（主）· 侧栏 220px · 顶栏 48px · 紧凑
```

> **注**：以上两份 HTML 是**最初的静态设计稿**，已被 P1-28「视觉基线换血」取代。
> 当前配色、圆角、阴影的权威来源是 **`web/src/styles/tokens.css`**，
> 不要再以 dash2.html / demo2.html 为准。

两份是同一套 token 契约的两个主题原型，语义变量名已提取进 `tokens.css`。
组件类名沿用了 demo 里的命名，实现是 React 组件，不是照抄 HTML 结构。

---

## 2. Token 契约

**所有颜色、间距、圆角一律走变量，组件里不许出现字面量颜色值。**

### 2.1 结构

| 变量 | 用途 |
|---|---|
| `--side-w` | 侧栏宽 |
| `--top-h` | 顶栏高 |
| `--radius-sm/md/lg/full` | 圆角 |
| `--sp-1` … `--sp-6` | 间距 4/8/12/16/24/32px |

### 2.2 背景与边框

`--bg-app` 页面底 · `--bg-card` 卡片 · `--bg-card-sub` 卡片内次级块 ·
`--bg-hover` 悬停 · `--bg-header` 顶栏 · `--bg-side` 侧栏
`--border-main` 主边框 · `--border-light` 更浅 · `--border-dim` 更暗

### 2.3 文字

`--text-main` 正文 · `--text-dim` 次要 · `--text-mute` 弱化/占位

### 2.4 语义色（★ 状态一律用这组，不要直接用色板）

| 变量 | 含义 | 用在 |
|---|---|---|
| `--accent` / `--accent-hover` / `--accent-bg` | 主操作 | 主按钮、选中态、链接 |
| `--accent-gradient` | 品牌渐变 | 主按钮背景、强调元素 |
| `--ok` / `--ok-bg` / `--ok-border` | 正常 | 节点在线、健康、达标 |
| `--warn` / `--warn-bg` / `--warn-border` | 警告 | 即将到期、流量接近上限、时钟异常 |
| `--err` / `--err-bg` / `--err-border` | 错误 | 节点离线、投递失败、数据库不可用 |
| `--color-teal/cyan/blue/yellow/red/purple` | 图表系列色 | **只给图表用**，不用于状态 |

### 2.5 阴影、毛玻璃与遮罩

`--card-shadow` `--card-shadow-lg` `--modal-shadow`
`--glass-bg` `--glass-bg-strong` `--glass-border` `--glass-blur` `--glass-shadow`
`--glow-1` `--glow-2`（品牌氛围辉光，用于登录页等背景装饰）
`--overlay`（模态框遮罩背景，暗色 `rgba(2,6,14,.62)` / 浅色 `rgba(15,30,60,.45)`）

浅色主题下顶栏/侧栏用毛玻璃（`backdrop-filter: blur(var(--glass-blur))`），
深色主题下通过半透明边框与卡片背景区分层次。

### 2.6 字体

```css
--font-sans: -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",
             Arial,"PingFang SC","Hiragino Sans GB","Microsoft YaHei",
             "Noto Sans CJK SC",sans-serif;
--font-mono: "JetBrains Mono","Fira Code",ui-monospace,SFMono-Regular,Menlo,
             Monaco,Consolas,"Liberation Mono","Courier New",monospace;
```

★ **不引入 Web Font**（demo 里的 Geist 不要）。中文字重回退链已经排好，
自带字体足够，加载外部字体会拖慢首屏且在内网环境失效。

★ **所有数字一律用 `--font-mono`**（`cell-mono` 类）：CPU 百分比、流量、
字节数、时间戳、ID。等宽是表格里数字能对齐扫读的前提。
配合 `font-variant-numeric: tabular-nums` 保证所有数字列绝对对齐。

---

## 3. 双主题

> ★ **实际结构（P1-28 换血后）：暗色优先。** `tokens.css` 以 `:root` 定义暗色 token，
> 浅色 token 通过 `[data-theme="light"]` 和媒体查询覆盖。

```css
:root                                           { /* 暗色 token（默认） */ }
:root[data-theme="dark"]                        { /* 暗色，显式选择，与 :root 一致 */ }
:root[data-theme="light"]                       { /* 浅色，显式选择优先 */ }
@media (prefers-color-scheme: light) {
  :root:not([data-theme="dark"])               { /* 浅色，跟随系统且未强制暗色 */ }
}
```

三态：显式亮 / 显式暗 / 跟随系统（默认）。选择存 `localStorage`。
★ **每个 token 都要在浅色和深色下各定义一次**，
不许有「只在某个主题下定义」的变量——那会在另一个主题下变成空值。

---

## 4. 布局

```
┌────────────────────────────────────────────┐
│  顶栏  --top-h   logo · 全局搜索 · 事件铃 · 主题 · 用户  │
├──────────┬─────────────────────────────────┤
│ 侧栏      │  内容区                          │
│ --side-w │  ┌ page-head ─────────────────┐  │
│          │  │ 标题 · 面包屑 │ toolbar    │  │
│ 菜单      │  └───────────────────────────┘  │
│          │  卡片 / 表格 / 图表               │
└──────────┴─────────────────────────────────┘
```

- 侧栏在 `< 1024px` 折叠成抽屉
- 内容区最大宽度不设限（表格要能铺开），但左右留 `--sp-5`
- 表格用 `.tbl-wrap` 包裹并 `overflow-x: auto`，**页面本身绝不横向滚动**

---

## 5. 组件类名（沿用 demo）

| 类 | 说明 |
|---|---|
| `.btn` + `.primary` / `.danger` / `.mini` | 按钮 |
| `.badge` + `.badge-info` / `.badge-neutral` | 标签徽章（节点 tag 用这个） |
| `.pill` + `.red` / `.clickable` | 状态药丸 |
| `.card` | 卡片容器 |
| `.tbl-wrap` `.col-num` `.col-text` `.cell-mono` | 表格 |
| `.page-head` `.toolbar` | 页头与右侧操作区 |
| `.health` + `.ok` / `.warn` / `.err` | 健康指示点 |
| `.db-banner` + `.warn` / `.err` | 顶部全局横幅 |
| `.text-pos` / `.text-neg` | 数值正负着色 |

React 侧：`web/src/components/` 一个组件一个文件，样式走 CSS Modules
（`Button.module.css`），**变量从 `tokens.css` 取**。

图标用 **SVG sprite**（demo 里的 `<symbol>` + `<use>` 模式），
一个 `icons.svg` 内联进 index.html，**不引入图标库**。

---

## 6. 状态映射（★ 不要各页面各定义一套）

| 业务状态 | 视觉 |
|---|---|
| 节点 `online` | `.health.ok` 绿点 + `--ok` |
| 节点 `offline` | `.health.err` 红点 + `--err`，整行文字降到 `--text-dim` |
| 节点 `never`（从未上线） | `.health` 灰点 + `--text-mute` |
| 时钟异常 | 节点名后加 `.badge.badge-neutral` 标 |
| 到期 ≤7 天 | 到期列 `--warn`；已过期 `--err` |
| 流量 ≥80% | `--warn`；≥100% `--err` |
| 数据库不可用 | 顶部 `.db-banner.err`，**页面其余部分继续显示内存缓存数据** |

---

## 7. 图表

ECharts。约定：

- 系列色按顺序取 `--color-blue` → `--color-teal` → `--color-yellow` →
  `--color-purple` → `--color-cyan` → `--color-red`。
  **从 CSS 变量读取（`getComputedStyle`），不在 JS 里写死十六进制**，否则切主题不跟随
- 主题切换时**重新取色并 `setOption`**，不重建实例
- 网格线用 `--border-light`，坐标轴文字用 `--text-dim`
- `null` 值断线，**不连成直线**（`connectNulls: false`）
- 时间轴用本地时区显示，**数据本身是 epoch 毫秒**
- 4 个跨度共用一个组件，跨度切换只换数据不换实例

---

## 8. 不做的事

- 不引入组件库（antd / MUI / Element）
- 不引入图标库
- 不引入 Web Font
- 不做动画特效，`transition` 只用在 hover / 主题切换，时长 ≤150ms

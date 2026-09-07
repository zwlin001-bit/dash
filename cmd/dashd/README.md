# cmd/dashd

服务端守护进程主入口。

## 职责边界

- 服务端主二进制入口程序。
- 负责整体运行环境初始化、命令行参数解析（支持 `-v`, `--version`）。
- 集中装配各个子业务模块（基于 `Module` 契约列表与 `App` 容器）。
- 作为单个静态二进制产物编译输出至 `bin/dashd`。

## 对外接口

- `App`：服务端核心组件容器结构体。
- `Module`：模块注册契约接口（`Name() string`, `Register(*App) error`）。
- `modules`：全局模块装配切片（后续任务按需追加一行）。
- `RegisterModules(app *App, mods []Module) error`：装配执行逻辑。

## 依赖谁

- 标准库。
- 装配列表中的各服务端内部模块（`internal/*`）。

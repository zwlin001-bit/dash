# internal/app

服务端核心应用上下文与模块契约定义。

## 职责边界

- 定义服务端核心 `App` 结构体，承载运行时核心组件（路由分发器、配置、数据库等）。
- 定义 `Module` 契约接口，解耦 `cmd/dashd` 装配逻辑与各子模块实现。

## 对外接口

- `App`: 服务端核心运行时结构体。
- `Module`: 服务端子模块注册接口（`Name() string`, `Register(*App) error`）。

## 依赖谁

- 标准库 `net/http`

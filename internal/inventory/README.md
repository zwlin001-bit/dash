# internal/inventory

机器资产清单与节点元信息管理。

## 职责边界

- 管理节点（nodes）基本信息生命周期（创建、编辑、状态流转、删除、令牌吊销）。
- 管理节点标签（tags / node_tags 关联表）与分组（node_groups）。
- 持久化和维护节点硬件/静态规格信息（node_facts）。
- 维护节点计费与流量配额周期信息（node_billing）。
- 管理 Agent 注册令牌（enroll_tokens）生成、列表、作废与一键安装命令下发。
- 保证所有变更类操作写审计日志（audit_log）。
- 遵循应用层显式级联清理（删节点时一并清理 node_tags / node_facts / node_billing；删标签先清 node_tags；删分组置空节点 node_group_id）。时序数据不同步删，由保留期自动过期。

## 对外接口

- `NodeService`：节点管理业务接口与 HTTP Handler。
- `GroupService`：节点分组管理业务接口与 HTTP Handler。
- `TagService`：标签管理业务接口、节点打标签、反查节点与 HTTP Handler。
- `FactsService`：节点静态硬件规格查询与写入。
- `BillingService`：节点计费与流量信息增删改查。
- `EnrollService`：注册令牌生成与作废接口。
- `NewModule()`：提供符合 `app.Module` 契约的模块实例与路由装配。

## 依赖谁

- `internal/db`
- `internal/audit`
- `internal/ulid`
- `internal/auth`

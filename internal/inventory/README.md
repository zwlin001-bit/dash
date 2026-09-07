# internal/inventory

机器资产清单与节点元信息管理。

## 职责边界

- 管理节点（nodes）基本信息生命周期（创建、编辑、状态流转、删除）。
- 管理节点标签（tags / node_tags）与分组（node_groups）。
- 持久化和维护节点硬件/静态规格信息（node_facts）。
- 维护计费信息（node_billing）。

## 对外接口

- `NodeService`：节点管理业务接口。
- `TagService`：标签管理业务接口。

## 依赖谁

- `internal/db`
- `internal/logx`

# migrations

数据库 Schema 迁移脚本目录（占位）。

## 职责边界

- 存放数据库版本演进的 DDL 脚本。
- 文件编号递增、只增不改，每次变更必须同时提供 Oracle 与 MySQL 两套 DDL（例如 `0001_init.oracle.sql` 与 `0001_init.mysql.sql`）。
- 两份 DDL 的表名、列名、约束必须完全对称。
- 执行状态记录于 `schema_migrations` 表。

## 依赖谁

- 由 `dashd migrate` 或 `make migrate` 脚本调用执行。

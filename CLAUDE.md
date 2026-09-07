# dash

个人 VPS 管理平台：监控 + 云资产（阿里云 / GCP）+ 代理服务器管理。

## 先读这个

**`PRINCIPLES.md` 是本工程的硬性约束，动手前必读。** 尤其是：
- 数据库只写可移植 SQL（Oracle ADB → 未来 MySQL），禁止 Oracle 方言
- agent 有明确的资源预算，超标视为缺陷
- 改表结构 / 改协议，必须同步改文档

## 设计文档

`docs/` 是设计的事实来源，见 `docs/README.md`。

## 分工

设计（Claude）→ 实现（反重力）→ 验收（Claude）。
发现设计有问题：先改 `docs/`，再改代码。

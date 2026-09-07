# ADR 0004 云厂商集成用独立进程插件，允许 Python

**状态**：已采纳 · 2026-09-07

## 背景
「架构耦合、加一个云厂商要改一堆地方」是上一版明确的不满点。
同时，阿里云和 GCP 的官方 SDK 在 Python 侧更成熟完整，
主进程用 Go 但希望能用 Python 写云集成。

## 选项
1. **Go 接口 + 编译期插件**。性能最好，但强制所有 provider 用 Go，
   且新增 provider 要重新编译整个 dashd。
2. **Go plugin（.so）**。Go 的 plugin 机制对版本和构建环境极其敏感，
   `CGO_ENABLED=0` 下不可用，与我们的静态构建要求直接冲突。排除。
3. **独立进程 + RPC**。

## 决定
选 3：每个 provider 是独立进程，dashd 监管其生命周期，
通过 Unix domain socket 上的 JSON-RPC 2.0 通信，契约见 `docs/01-architecture.md` §3.3。

- 语言不限，**Go 与 Python 混用被明确允许**
- provider 不碰数据库，只负责「云厂商 API → dash 归一化资源模型」的翻译
- Python provider 用独立 venv，不污染系统 Python
- provider 崩溃不影响主进程，自动重启

## 代价
- 每次调用多一次进程间序列化。云 API 调用本身是几百毫秒级，这点开销可忽略。
- 部署时要管理 provider 进程和 Python 运行时。由 `setup.sh` 承担，用户无感。
- 契约一旦发布就要保持兼容。因此契约必须版本化，见 `provider.describe` 的返回。

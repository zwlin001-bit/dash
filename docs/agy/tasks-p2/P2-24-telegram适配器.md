# P2-24 Telegram 适配器

> 第二期 · 任务 24 ｜ 前置：21、23 ｜ 分支：`agy/p2-24-telegram` ｜ 迁移编号：无
> 设计依据：[`../../09-events-notify.md`](../../09-events-notify.md) §3、§6

> ★ **完成后必须自己 `git push -u origin <分支名>`**，并确认
> `git log --oneline -1 origin/<分支名>` 能看到你的提交。只提交到本地等于没交付。

## 目标

把渲染好的文本真的发到 Telegram。同时定义好「适配器」这个扩展点。

## 交付物

```
internal/notify/sender/
  sender.go     type Sender interface { Kind() string; Send(ctx, ch Channel, text string) error }
  registry.go   注册表
  telegram.go
  webhook.go
  README.md
```

## 约束

- ★ **注册表形状**：加一种渠道 = 加一个文件 + 一行注册，不改路由和投递逻辑。
  验收会让你现场演示
- Telegram 用 `sendMessage`，支持 `chat_id`、`message_thread_id`（话题群）、
  `parse_mode`（HTML / MarkdownV2）
- ★ **同一 chat 串行 + 限速**。Telegram 对单 chat 有速率限制，
  并发发送会被 429，而且是静默丢消息
- 收到 429 要读 `retry_after` 并遵守，**不要固定退避**
- 消息超长（TG 限 4096 字符）要截断并加省略标记，**不是发送失败**
- 错误要能区分「可重试」（网络、5xx、429）和「不可重试」（token 错、chat 不存在），
  不可重试的直接标 `failed`，不浪费三次重试
- webhook 适配器：支持自定义 method、headers、body 模板
- **不引入 Telegram SDK**，标准库 `net/http` 足够（P3 依赖纪律）

## 验收

1. 用真实 bot 发一条消息到真实 chat，收到（PR 里贴截图或 message_id）
2. 同一 chat 连发 30 条，**没有被 429 丢消息**
3. 故意用错误 token → 标记为不可重试的 `failed`，不重试三次
4. 发一条 5000 字符的消息 → 正确截断并送达
5. 演示：新增一种渠道只加了一个文件 + 一行注册
6. `go list -m all` 里没有新增第三方依赖

## 边界

只负责「把这段文本发出去」。不管什么时候发、发几次、发给谁。

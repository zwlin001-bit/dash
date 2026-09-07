# 05 部署

目标：**`./setup.sh install`，只问域名，装完即用，443 端口，证书自动。**

---

## 1. 用户视角

```sh
git clone <repo> && cd dash
sudo ./setup.sh install
# > 请输入访问域名: dash.example.com
# ...
# 安装完成。访问 https://dash.example.com
# 初始管理员: admin / <随机生成的密码，只显示一次>
```

非交互形式：

```sh
sudo ./setup.sh install --domain dash.example.com --yes
```

> **ADB 已关闭 mTLS，用连接串（TLS-only）直连，不需要 wallet。**
> 数据库连接信息通过 `--db-dsn` / `--db-user` / `--db-password` 或对应环境变量提供。

子命令：`install` / `upgrade` / `uninstall` / `status`。**全部幂等，可重复执行。**

---

## 2. install 做的事

1. **前置检查**：root 权限、发行版（alpine / debian / ubuntu）、架构、443 端口未被占用、能出网
2. **建用户**：`dashd` 系统用户（Alpine `adduser -S -D`，Debian 系 `useradd -r -s /usr/sbin/nologin`）
3. **落文件**
   ```
   /usr/local/bin/dashd
   /etc/dash/config.toml        0600 dashd:dashd   ← 只放自举必需项
   /etc/dash/master.key         0400 dashd:dashd   ← 随机生成，凭据加密主密钥
   /var/lib/dash/               0750 dashd:dashd   ← 证书缓存、provider socket
   /var/lib/dash/providers/
   ```
4. **赋能力**：`setcap cap_net_bind_service=+ep /usr/local/bin/dashd`
   （Alpine 需先 `apk add libcap`）。**dashd 不以 root 运行。**
5. **写服务**：systemd unit 或 OpenRC 脚本，enable
6. **数据库**：连接 ADB → 执行 `migrations/` → 写入 `settings.site.domain` → 创建管理员账号
7. **启动**并等待健康检查通过（`GET /healthz` 返回 200），失败则打印日志尾部并回滚服务状态

### 2.1 config.toml（只有自举必需项）

```toml
[db]
driver      = "oracle"          # oracle | mysql
dsn         = "…"
wallet_path = ""

[server]
listen        = ":443"
data_dir      = "/var/lib/dash"
master_key    = "/etc/dash/master.key"
```

**域名不在这里。** 域名存数据库 `settings` 表的 `site.domain`，界面可改（P5.4）。

---

## 3. 域名与证书

- 证书用 ACME（`golang.org/x/crypto/acme/autocert`），缓存目录 `/var/lib/dash/certs`
- 证书按 `settings.site.domain` 的当前值签发
- 同时监听 80 端口仅用于 ACME HTTP-01 挑战 + 301 跳 443
- **改域名流程**：界面提交新域名 → 校验域名解析已指向本机 → 写入 `settings` →
  触发新证书签发 → 签发成功后切换 → 失败则回滚旧域名并提示。**不重启进程。**
- 支持配置 DNS-01（用于泛域名或 80 端口不可达的环境），凭据存 `credentials` 表

---

## 4. upgrade / uninstall

```
setup.sh upgrade    # 停服务 → 备份旧二进制 → 换新 → 跑 migrations → 起服务 → 健康检查
                    # 健康检查失败自动回滚到旧二进制
setup.sh uninstall  # 停服务、删 unit、删二进制；默认保留 /etc/dash 与数据库
                    # --purge 才删配置（数据库永远不动，只提示）
setup.sh status     # 服务状态、版本、数据库连通性、证书到期时间、在线 agent 数
```

---

## 5. Provider 进程

- 二进制 / 脚本放 `/var/lib/dash/providers/<code>/`
- dashd 启动时读 `providers` 表，拉起 `is_enabled=1` 的 provider
- Python provider：`setup.sh` 检测到需要时创建独立 venv（`/var/lib/dash/providers/<code>/venv`），
  不污染系统 Python，也不要求系统装特定 Python 版本以外的东西
- 监管：崩溃自动重启（退避），连续失败 5 次标记 `unhealthy` 并告警，不影响主进程

---

## 6. 备份

`setup.sh` 不管数据库备份（ADB 自带自动备份）。
需要备份的本机文件只有两个，安装完成时明确提示用户：

```
/etc/dash/master.key   ← 丢失则所有已存凭据无法解密
/etc/dash/config.toml
```

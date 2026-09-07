# 14 双域名部署架构

**任务 19 重做的设计依据。** 替代 `05-deployment.md` 的单域名方案。

---

## 1. 为什么两个域名

管理控制台的攻击面远大于 agent 接入面：它有登录、有全部管理 API、有前端。
而它**只有本人使用**。所以把两者物理隔开：

| 入口 | 域名 | 监听 | 谁访问 | 暴露什么 |
|---|---|---|---|---|
| **控制台** | 内网域名 | **WireGuard 内网 IP** | 只有本人，经 WG 进来 | 前端 + `/api/v1/**` |
| **Agent 接入** | 互联网域名 | 公网 IP | 所有被管机器 | 仅 `/api/agent/v1/**` + `/dl/**` + `/install.sh` |

★ **控制台不监听公网。** 没有 WG 就连不上，扫描器扫不到，登录接口不暴露在互联网上。
这是整套架构最值钱的一条，实现时不许为了方便打折。

---

## 2. 部署形态

```
                  ┌──────────────── 这台服务器 ────────────────┐
  WG 客户端        │                                            │
  （本人）  ──WG──▶│  wg0: 10.x.x.x                             │
                  │    └─ nginx :443  (内网域名)               │
                  │         └─▶ dashd 127.0.0.1:8443           │
                  │                                            │
  agent  ──公网──▶│  eth0: 公网 IP                              │
                  │    └─ nginx :443  (互联网域名)             │
                  │         └─▶ dashd 127.0.0.1:8443（限路径）  │
                  └────────────────────────────────────────────┘
```

- **dashd 只监听 `127.0.0.1:8443`**，不直接对外。TLS 由 nginx 终结
- nginx 两个 server 块，按域名分流，**路径白名单在 nginx 层做**
- 两个域名各自签发证书

### 2.1 为什么把路径限制放在 nginx 而不是 dashd

在 dashd 里按 Host 头判断也能做，但 **nginx 层的限制是配置即审计** ——
一眼能看出公网入口开了哪些路径。而且 dashd 被打穿时 nginx 仍是一道墙。

代价是 nginx 配置要跟 API 路由保持同步。因此：
★ **新增 agent 侧端点时必须同步改 nginx 白名单**，写进 `PRINCIPLES.md` 的文档同步条款。

---

## 3. nginx 配置骨架

```nginx
# ---------- 控制台：内网 ----------
server {
    listen      10.x.x.x:443 ssl;    # ★ 绑 WG 内网 IP，不是 0.0.0.0
    http2       on;
    server_name dash-admin.internal;

    ssl_certificate     /etc/dash/certs/admin/fullchain.pem;
    ssl_certificate_key /etc/dash/certs/admin/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8443;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade    $http_upgrade;   # SSE / WS
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;                          # ★ SSE 必须关缓冲
        proxy_read_timeout 3600s;
    }
}

# ---------- Agent 接入：公网 ----------
server {
    listen      443 ssl;
    http2       on;
    server_name dash.example.com;

    ssl_certificate     /etc/dash/certs/agent/fullchain.pem;
    ssl_certificate_key /etc/dash/certs/agent/privkey.pem;

    # ★ 只放行这三类路径，其余一律 404
    location /api/agent/v1/ { proxy_pass http://127.0.0.1:8443; include /etc/nginx/dash-proxy.conf; }
    location /dl/           { proxy_pass http://127.0.0.1:8443; include /etc/nginx/dash-proxy.conf; }
    location = /install.sh  { proxy_pass http://127.0.0.1:8443; include /etc/nginx/dash-proxy.conf; }

    location / { return 404; }
}
```

`map $http_upgrade $connection_upgrade { default upgrade; '' close; }` 放 http 块。

---

## 4. 证书

两个域名的证书获取方式不同：

| 域名 | 方式 | 原因 |
|---|---|---|
| 互联网域名 | **ACME HTTP-01**（certbot / acme.sh） | 公网可达，标准流程 |
| 内网域名 | **ACME DNS-01**，或自签 + 客户端信任 | ★ 内网 IP 上 Let's Encrypt 的 HTTP-01 **验证不了**，必须走 DNS-01 |

`setup.sh` 要问清楚内网域名用哪种方式：
- 有公共 DNS 且能配 API → DNS-01（推荐，能拿到受信任证书）
- 纯内部域名 → 生成自签证书 + 提示用户在客户端导入 CA

**不要试图用 HTTP-01 给内网域名签证书**，会一直失败且原因难查。

---

## 5. setup.sh install 流程

交互只有三个必答项：

```
$ sudo ./setup.sh install
> 控制台域名（内网访问）        : dash-admin.internal
> 控制台绑定的内网 IP           : 10.8.0.1        ← 自动列出本机非公网 IP 供选择
> Agent 接入域名（公网）        : dash.example.com
```

其余全部默认值。非交互形式：

```sh
sudo ./setup.sh install \
  --admin-domain dash-admin.internal --admin-bind 10.8.0.1 \
  --agent-domain dash.example.com --yes
```

步骤：

```
1. 前置检查    发行版、架构、443 未占用、内网 IP 存在且可绑定
2. 装依赖      nginx（apt / apk），不装则报错退出
3. 建用户目录  dashd 用户、/etc/dash、/var/lib/dash
4. 落文件      dashd 二进制、config.toml(0600)、nginx 两个 server 块
5. 证书        按 §4 分别获取，失败给出可读原因（尤其内网域名那条）
6. 数据库      dashd init-db（建表 + 写两个域名到 settings + 建管理员）
7. 启动        dashd 服务 + nginx reload
8. 健康检查    内网域名 curl /healthz 200；公网域名 curl /api/agent/v1/... 通、
               curl / 返回 404（★ 验证路径白名单确实生效）
```

### 5.1 域名存哪

```
settings.site.admin_domain    控制台域名
settings.site.admin_bind_ip   控制台绑定的内网 IP
settings.site.agent_domain    agent 接入域名
```

`install_cmd`（一键装机命令）用 **`agent_domain`** 拼，不是 admin_domain ——
装机脚本要从公网下载。

---

## 6. 资源预算

| 组件 | 稳态 RSS | 说明 |
|---|---|---|
| dashd | **≤ 1 GB** | 30 台 agent 在线 |
| nginx | ≤ 50 MB | 两个 server 块，无缓存 |
| **合计** | **≤ 1.1 GB** | 1 GB 机器偏紧，**2 GB 舒适** |

若目标机器只有 1 GB：把 `db.max_open_conns` 降到 10，
`SetMemoryLimit` 设 700 MB，并在文档里注明这是压缩配置。

---

## 7. 与旧方案的差异

`05-deployment.md` 的单域名 + autocert + dashd 直接绑 443 方案**作废**。
主要变化：

| | 旧 | 新 |
|---|---|---|
| 域名 | 1 个 | **2 个** |
| TLS 终结 | dashd 内置 autocert | **nginx** |
| dashd 监听 | 0.0.0.0:443（setcap） | **127.0.0.1:8443**（不需要 setcap） |
| 控制台暴露面 | 公网 | **仅 WG 内网** |
| 依赖 | 无 | nginx |

★ dashd 不再需要 `cap_net_bind_service` —— 监听 8443 不需要特权。这是个额外收益。

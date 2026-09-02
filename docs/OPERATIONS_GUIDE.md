# 运行保障指南：限流、指标与告警

> 适用对象：运行生产游戏服务的维护者。本文覆盖阶段 3 中「配置安全」与「可观测性与运行保障」两节已落地的部分。
>
> 备份、恢复演练与账本对账见[数据库备份与恢复指南](BACKUP_AND_RESTORE_GUIDE.md)。

## 1. 三条防线分别防什么

| 防线 | 防的是什么 | 没有它会怎样 |
|---|---|---|
| 分层限流 | 脚本暴力猜密码、批量注册、刷 TRTC 凭证、单机开几百条连接 | 账号被撞库；TRTC 每张 UserSig 都计费，被刷等于替别人付云账单 |
| `/metrics` | 看不见服务内部状态 | 只能靠玩家描述症状反推，例如「全桌卡死」只能等加时卡耗尽才发现 |
| 告警 | 服务挂了、备份停了、账本不平却没人知道 | 备份静默失败 60 天后本机与远端副本全部轮转清空 |

## 2. 分层限流

限流由游戏服务内置，单实例内存实现，随服务重启清零。所有参数通过环境变量配置，格式为 `次数/时长`（Go 时长语法，如 `30/5m`、`5/1h`），写 `off` 关闭该项。

| 变量 | 默认 | 作用域 | 覆盖入口 |
|---|---|---|---|
| `RATE_LIMIT_AUTH_PER_IP` | `30/5m` | 客户端 IP | 登录、注册、刷新令牌合计 |
| `RATE_LIMIT_REGISTER_PER_IP` | `5/1h` | 客户端 IP | 注册（在上一项基础上单独收紧） |
| `RATE_LIMIT_LOGIN_FAILURES_PER_USER` | `8/15m` | 用户名 | **只在密码错误时计数**，成功登录后清零 |
| `RATE_LIMIT_USER_OPS_PER_USER` | `30/1m` | 已登录用户 | 建房、入房、准备、离桌、虚拟充值 |
| `RATE_LIMIT_TRTC_PER_USER` | `12/1m` | 已登录用户（调试令牌路径按 IP） | TRTC 凭证签发 |
| `RATE_LIMIT_WS_CONNECTIONS_PER_IP` | `20` | 客户端 IP | 同时保持的 WebSocket 连接数 |

设计要点：

- **按用户名的登录失败锁定**只针对被猜的那个账号，其他人登录不受影响；用户名比较不区分大小写与首尾空白。
- **WebSocket 上限在升级握手之前判定**，被拒的连接收到普通 HTTP 429，不占用已建立连接。
- 被限流的响应为 HTTP `429`，正文 `{"error":"rate_limited"}`，并带 `Retry-After` 秒数。客户端已有该错误码的中文映射（「消息发送太快，请稍后再试」）。
- 默认值面向熟人牌局的真实强度，正常玩家不会触碰。一桌 10 人同时开语音也远低于 TRTC 每分钟 12 次的上限。

### 可信代理：让限流认对人

游戏服务跑在 Docker 里、前面是 nginx。**不配置代理信任时，服务看到的所有请求都来自 nginx 那一个地址**，按 IP 的限流会把全体玩家算成同一个人——玩家多起来后会互相触发限流。

`TRUSTED_PROXIES` 填写允许采信 `X-Forwarded-For` 的代理地址或网段，逗号分隔：

```dotenv
# 宿主机 nginx 经 Docker 端口转发进入容器，容器看到的来源是它所在网络的网关。
# 用网段而不是单个地址：重建网络后网关可能变化，网段写法不会悄悄失效。
TRUSTED_PROXIES=127.0.0.1,172.20.0.0/16
```

**确认实际网段时不要看默认 `bridge` 网络**——按部署手册游戏服务容器在自建的 `texas-internal` 网络上。直接问容器：

```bash
docker inspect texas-game-server   -f '{{range $name, $net := .NetworkSettings.Networks}}{{$name}}: gateway={{$net.Gateway}} ip={{$net.IPAddress}}{{"
"}}{{end}}'
```

输出形如 `texas-internal: gateway=172.20.0.1 ip=172.20.0.3`，取网关所在的 `/16` 网段。若前面还有云厂商负载均衡，把它的网段一并加入，例如 `127.0.0.1,172.20.0.0/16,100.64.0.0/10`。

**安全边界**：只有直连方在该列表内时才读取转发头，否则一律用直连 IP。因此填错成公网网段不会让限流失效，只是可信范围过宽；**留空**则代理后所有请求同源，限流形同虚设。

nginx 侧需要传递真实 IP（宝塔默认模板通常已有）：

```nginx
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
```

## 3. 指标端点 `/metrics`

Prometheus 文本格式，无第三方依赖。**必须配置 `METRICS_TOKEN`（至少 16 位）才会注册该端点**；未配置时 `/metrics` 返回 404。访问时携带 `Authorization: Bearer <token>`。

```bash
curl -s -H "Authorization: Bearer $METRICS_TOKEN" http://127.0.0.1:8080/metrics
```

| 指标 | 类型 | 含义 |
|---|---|---|
| `texas_http_requests_total{route,status}` | counter | 按路由模式与状态码的 HTTP 请求数 |
| `texas_websocket_connections_active` | gauge | 当前 WebSocket 连接数 |
| `texas_websocket_messages_total{type}` | counter | 入站 WebSocket 消息数，按类型 |
| `texas_table_actions_total{result}` | counter | 牌局动作提交数，`accepted` / `rejected` |
| `texas_tables_active` | gauge | 本进程内存中的运行中牌桌数 |
| `texas_snapshot_broadcast_failures_total` | counter | **整桌无人收到快照的次数，每一次都是一桌卡死** |
| `texas_rate_limited_total{scope}` | counter | 被限流拒绝的请求数，按作用域 |

几条值得盯的信号：

- `texas_snapshot_broadcast_failures_total` **任何增长**都应当排查，它对应 P0 加的全桌级错误日志。
- `texas_rate_limited_total{scope="auth_ip"}` 或 `{scope="login_failures_user"}` 持续增长说明有人在撞库。
- `texas_rate_limited_total{scope="trtc"}` 增长说明有人在刷凭证。
- `texas_http_requests_total{status="5xx"}` 出现即为服务端异常。

接入 Prometheus / Grafana 时，在抓取配置里加 `authorization: {credentials: <token>}` 即可。**不要把 `/metrics` 暴露到公网 nginx**——它包含房间数、连接数等运营信息；只从服务器本机或内网抓取。

## 4. 告警

告警脚本位于 `deploy/monitor/`，三条线：

| 线 | 触发方式 | 说明 |
|---|---|---|
| 服务健康 | `texas-healthcheck.timer` 每 5 分钟访问 `/readyz` | 只在**状态翻转**时通知（挂了发一次、恢复发一次），持续故障不刷屏 |
| 备份新鲜度 | 同上，附带检查最近备份文件年龄 | 超过 36 小时未成功备份即告警；防止备份静默停跑 |
| 定时任务失败 | `OnFailure=texas-alert@%n.service` | 备份、对账任一失败立即通知，并附最近 25 行日志 |

### 4.1 配置通知渠道

创建 `/opt/texas/alert.env`：

```bash
sudo tee /opt/texas/alert.env >/dev/null <<'EOF'
# 接收告警的 webhook。未配置时告警只写系统日志，不阻塞任何任务。
TEXAS_ALERT_WEBHOOK=
# 载荷格式：dingtalk | wecom | feishu | bark | generic
TEXAS_ALERT_FORMAT=dingtalk
# 可选：告警标题中的主机名，默认取 hostname
# TEXAS_ALERT_HOST=texas-prod
# 可选：备份过期阈值（小时），默认 36
# TEXAS_BACKUP_MAX_AGE_HOURS=36
EOF
sudo chmod 600 /opt/texas/alert.env
```

`TEXAS_ALERT_FORMAT` 对应：

- `dingtalk`：钉钉群机器人，见下方逐步说明
- `wecom`：企业微信群机器人，webhook 形如 `https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...`
- `feishu`：飞书自定义机器人（文本消息）
- `bark`：Bark 推送到 iPhone，webhook 形如 `https://api.day.app/<key>`
- `generic`：`{"text": "..."}`，适用于 Server酱及自建接收端

每条告警都以 `【好友德州】` 开头（可用 `TEXAS_ALERT_TAG` 改）。钉钉与企业微信机器人常用「自定义关键词」做安全校验，把关键词设为这个标签即可保证送达。

webhook 地址等同于向你发消息的权限，`alert.env` 已被仓库卫生检查列为禁止提交。

#### 钉钉群机器人逐步配置

1. 在钉钉群里：群设置 → 机器人 → 添加机器人 → **自定义**（Webhook 接入）。
2. 机器人名字随意，例如「好友德州告警」。
3. **安全设置必须选一项**，选 **自定义关键词**，填 `好友德州`。不要选「加签」——它要求对每条消息做 HMAC 签名，本脚本不支持；也不要只选 IP 白名单，服务器公网 IP 变化会导致静默丢弃。
4. 完成后复制 webhook，形如 `https://oapi.dingtalk.com/robot/send?access_token=xxxxxxxx`。
5. 写入 `/opt/texas/alert.env`：

   ```bash
   TEXAS_ALERT_WEBHOOK=https://oapi.dingtalk.com/robot/send?access_token=xxxxxxxx
   TEXAS_ALERT_FORMAT=dingtalk
   ```

6. 立刻验证：`sudo /opt/texas/bin/texas-alert.sh "测试" "通道正常"`，群里应在几秒内收到。

常见失败：收不到但脚本显示「告警已发送」→ 多半是关键词不匹配被钉钉静默丢弃，用 `curl` 直接调用 webhook 时钉钉会返回 `{"errcode":310000,...}` 说明原因。钉钉限制每个机器人每分钟 20 条，巡检只在状态翻转时发送，不会触及该上限。

### 4.2 安装

```bash
cd <服务器仓库目录> && git pull --ff-only origin main
sudo cp deploy/monitor/texas-alert.sh deploy/monitor/texas-healthcheck.sh /opt/texas/bin/
sudo cp deploy/backup/texas-reconcile.sh /opt/texas/bin/
sudo chmod +x /opt/texas/bin/texas-*.sh
sudo cp deploy/monitor/texas-alert@.service \
        deploy/monitor/texas-healthcheck.service deploy/monitor/texas-healthcheck.timer \
        deploy/backup/texas-reconcile.service deploy/backup/texas-reconcile.timer \
        deploy/backup/texas-backup.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now texas-healthcheck.timer texas-reconcile.timer
```

`texas-backup.service` 需重新复制一次：它新增了 `OnFailure=` 行。

### 4.3 验证告警真的能到

**不要假定配置正确**，主动触发一次：

```bash
# 1. 直接发一条测试消息
sudo /opt/texas/bin/texas-alert.sh "测试" "如果你看到这条消息，告警通道正常"

# 2. 模拟一次定时任务失败，验证 OnFailure 链路
sudo systemctl start texas-alert@manual-test.service
```

两条都收到才算通。之后每次修改 webhook 都重做第 1 步。

## 5. 账本对账

`texas-reconcile.timer` 每周一 05:00 对**生产库本身**运行只读一致性校验（与恢复演练共用 `texas-verify.sql`），任一项 FAIL 即失败并触发告警。

它与备份恢复演练互补：演练在恢复出的副本上验证「备份可用」，对账盯着正在运行的库验证「账目始终自洽」。

**收到对账失败告警时**：先立即手动备份保留现场，再排查是哪次操作产生了不一致。**不要直接改数据把账调平**，那会掩盖真正的代码缺陷。

手动执行：

```bash
sudo /opt/texas/bin/texas-reconcile.sh
```

## 6. 服务端环境变量汇总

以下新增变量写入游戏服务的环境文件（`/opt/texas/secrets/game-server.env` 或部署时使用的位置），改动后需重建/重启游戏服务容器：

```dotenv
TRUSTED_PROXIES=127.0.0.1,172.20.0.0/16
METRICS_TOKEN=<至少16位随机字符串>
# 限流保持默认即可；只在确有需要时覆盖
# RATE_LIMIT_AUTH_PER_IP=30/5m
```

生成令牌：

```bash
openssl rand -hex 24
```

## 7. 生产验证记录

2026-09-01：`TRUSTED_PROXIES=127.0.0.1,172.20.0.0/16`（`texas-internal` 网段）与 `METRICS_TOKEN` 已配置并重建容器；`/metrics` 无令牌 401、带令牌 200；钉钉群机器人（自定义关键词「好友德州」）经手动触发与 `texas-alert@manual-test.service` 两条链路均收到消息；对账、巡检定时任务已启用，首次对账全部 PASS。

## 8. 检查清单

- [ ] `TRUSTED_PROXIES` 已填入 nginx 到容器的真实来源地址，且 nginx 转发了 `X-Forwarded-For`
- [ ] 在两台不同网络的设备上同时登录，确认不会互相触发限流
- [ ] `METRICS_TOKEN` 已配置，本机 `curl` 带令牌能拿到指标，不带令牌返回 401，nginx 未把 `/metrics` 暴露到公网
- [ ] `/opt/texas/alert.env` 已配置 webhook，`texas-alert.sh "测试"` 能收到消息
- [ ] `systemctl start texas-alert@manual-test.service` 能收到失败告警
- [ ] `systemctl list-timers` 中能看到 `texas-healthcheck.timer` 与 `texas-reconcile.timer`
- [ ] 手动执行 `texas-reconcile.sh` 全部 PASS

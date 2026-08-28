# 好友德州游戏服务

Go 实现的权威牌局服务，提供账号、娱乐筹码钱包/虚拟充值、好友房、最近牌局、管理员治理、TRTC 凭证 REST API，以及牌桌、房主转移、主动亮牌、自动准备、补码、聊天和语音状态 WebSocket 协议。

好友房只在最后一名成员离桌后关闭；当前房主离桌时，按成员加入时间和座位号将房主身份转移给最早加入的剩余玩家。每手结算后服务端启动 10 秒自动准备倒计时，在线且有牌桌筹码的玩家到期自动准备，期间可显式取消。弃牌玩家与非摊牌获胜者可以在下一手开始前主动公开自己的底牌。

## 本地启动

从仓库根目录复制 `.env.example` 为 `.env`，填写本地 TRTC 配置。随后在本目录执行：

```powershell
go run .\cmd\server
```

默认监听 `:8080`。健康检查为 `GET /healthz`，WebSocket 入口为 `/ws`。

## PostgreSQL 迁移

设置根目录 `.env` 中的 `DATABASE_URL` 后，在本目录执行：

```powershell
go run .\cmd\migrate up
```

迁移完成后启用持久化运行时：

```dotenv
STORAGE_BACKEND=postgres
DATABASE_URL=postgres://用户名:密码@主机:5432/数据库名?sslmode=require
DATABASE_AUTO_MIGRATE=false
```

服务启动时会验证迁移版本和校验和；版本缺失或 SQL 漂移时拒绝启动。开发环境也可将 `DATABASE_AUTO_MIGRATE=true`，生产环境建议保持 `false` 并独立执行迁移命令。

`000002_admin_console` 增加账号角色与服务器注册开关，`000003_admin_account_management` 增加管理员筹码调整账本类型，`000004_chat_moderation` 增加持久化文字禁言状态。升级已有数据库后必须先执行 `migrate up`，再启动新版本游戏服务。

## 管理员治理

- `requestAdmin=true` 只允许原子创建服务器首位管理员；已有管理员时返回 `admin_already_initialized`。
- `/v1/admin/*` 所有接口均同时要求有效会话和服务端 `admin` 角色，客户端是否显示入口不参与安全判断。
- 停用、删除和密码重置会撤销目标账号的会话；管理员自身不可停用或删除。
- 删除使用 `users.status=deleted` 软删除，牌局历史、筹码流水与审计事件保持不变。
- 新用户注册开关仅影响公开注册；管理员仍可在管理界面创建受管账号。
- 管理员可查看账号在线状态与当前房间号、修改登录用户名和钱包筹码，并将玩家请出房间。房间内禁止直接调整筹码；正在进行的一手牌必须先结算。
- 管理员可禁言或解除禁言普通账号；禁言只影响牌桌文字、快捷语和表情，不影响牌局与语音。状态持久化到 PostgreSQL，对已连接的牌桌立即生效，并与操作审计在同一事务提交。
- 在线状态由客户端每 30 秒发送一次已认证心跳，90 秒无心跳后显示离线；管理页面每 15 秒静默刷新一次。当前为单实例内存状态，后续多实例部署时迁移至 Redis。
- 已登录用户可修改自己的登录用户名和密码；修改密码会撤销旧会话并向当前客户端签发新会话。

`ALLOWED_ORIGINS` 使用逗号分隔完整的 Web 来源（例如 `https://poker.example.com`），同时控制 REST CORS 和 WebSocket Origin 校验；不要填写路径、中文标点或通配符。

仅对可丢弃的开发数据库回滚最近一次迁移：

```powershell
go run .\cmd\migrate down --steps 1
```

## 检查

```powershell
gofmt -w .\cmd .\internal
go vet ./...
go test ./...
```

测试套件包含固定种子规则模拟、请求幂等、断线恢复，以及 10 个独立 WebSocket 客户端连续 100 手验收。

## 容器化开发依赖

安装并启动 Docker Desktop 后，可从仓库根目录启动仅绑定本机的 PostgreSQL 和 Redis：

```powershell
docker compose -f .\deploy\docker-compose.dev.yml up -d
```

服务镜像定义位于 `services/game_server/Dockerfile`。Redis 当前仅作为下一步多实例路由的开发依赖，尚未接入游戏运行时。

香港生产环境的数据库迁移、服务容器更新、应用回滚和 Web 静态文件发布步骤见[生产环境更新手册](../../docs/PRODUCTION_UPDATE_GUIDE.md)。

## 当前限制

默认配置仍使用内存仓储，生产环境已经接通 PostgreSQL 并完成真实数据库集成验收。Redis 当前尚未接入游戏运行时；后续按[上线准备计划](../../docs/PHASE_3_PLAN.md)继续实现租约、多实例恢复、自动备份和运行保障。

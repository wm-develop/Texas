# 好友德州游戏服务

Go 实现的权威牌局服务，提供账号、娱乐筹码钱包/虚拟充值、好友房、最近牌局、TRTC 凭证 REST API，以及牌桌、补码、聊天和语音状态 WebSocket 协议。

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

## 当前限制

默认配置仍使用内存仓储。PostgreSQL 运行时已经接通，但本机没有可用的 PostgreSQL/Docker 服务，所以带 `TEST_DATABASE_URL` 的真实数据库集成用例尚未执行。完成该验收后，再按 [上线准备计划](../../docs/PHASE_3_PLAN.md) 接入 Redis 租约、多实例恢复、备份和管理治理。

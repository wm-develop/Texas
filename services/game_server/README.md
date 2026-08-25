# 好友德州游戏服务

Go 实现的权威牌局服务，提供账号、娱乐筹码钱包/虚拟充值、好友房、最近牌局、TRTC 凭证 REST API，以及牌桌、补码、聊天和语音状态 WebSocket 协议。

## 本地启动

从仓库根目录复制 `.env.example` 为 `.env`，填写本地 TRTC 配置。随后在本目录执行：

```powershell
go run .\cmd\server
```

默认监听 `:8080`。健康检查为 `GET /healthz`，WebSocket 入口为 `/ws`。

## 检查

```powershell
gofmt -w .\cmd .\internal
go vet ./...
go test ./...
```

测试套件包含固定种子规则模拟、请求幂等、断线恢复，以及 10 个独立 WebSocket 客户端连续 100 手验收。

## 当前限制

当前默认使用内存账号、钱包、房间、聊天、历史和账本仓储，仅适用于本地或封闭测试。阶段 3 的首版 PostgreSQL 数据契约位于 [`migrations`](migrations)，但运行时尚未启用数据库连接；待事务仓储与集成测试完成后才会要求准备 PostgreSQL，随后再按 [上线准备计划](../../docs/PHASE_3_PLAN.md) 接入 Redis、备份和多实例路由。

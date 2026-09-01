# 好友德州自建部署指南

本文面向第一次部署本项目的维护者，介绍如何从一台空白 Linux 服务器搭建 PostgreSQL、游戏服务和 Flutter Web 站点。所有域名、密码和路径均为示例，执行前必须替换为自己的值。

本项目不会在 GitHub Release 中直接提供 Web、Windows、Android 或 HarmonyOS 安装包。不同部署者需要使用自己的服务地址、TRTC 应用和平台签名，因此应从对应 Git 标签自行构建。

部署前先阅读[项目现状](PROJECT_STATUS.md)中的当前限制：本版本只能安全地以单游戏服务实例运行，PostgreSQL 不等于进行中牌桌的完整故障恢复。

## 1. 部署结构

推荐使用两个启用 HTTPS 的域名：

- `poker.example.com`：Flutter Web 静态站点。
- `api-poker.example.com`：Go API 和 WebSocket，反向代理至服务器的 `127.0.0.1:8080`。

服务端采用单实例部署：

```text
浏览器/客户端
  ├─ HTTPS/WSS ─> Nginx/Caddy/宝塔 ─> 127.0.0.1:8080 ─> 游戏服务
  └─ HTTPS ─────> Web 静态站点

游戏服务 ─> Docker 内部网络 ─> PostgreSQL 17
游戏服务 ─> 腾讯云 TRTC（可选；牌桌语音需要）
```

当前版本的 Redis 尚未接入运行时，不需要在生产环境部署 Redis。生产环境应使用 PostgreSQL；内存仓储只适合本地临时调试，服务重启后数据会丢失。

## 2. 前置条件

服务器建议准备：

- 64 位 Linux，至少 2 GB 内存并配置 Swap。
- Docker 及 Docker Compose v2。
- Git。
- 能申请 HTTPS 证书并支持 WebSocket 的反向代理。
- 已解析到服务器的 Web 域名和 API 域名。
- 如需语音，准备腾讯云 TRTC `SDKAppID` 和 `SecretKey`。

客户端构建环境参见根目录 [README](../README.md#环境要求)。HarmonyOS 构建还需要本机 DevEco Studio 签名配置；Android 正式分发应使用自己的发布签名。

## 3. 创建服务器目录

以下示例使用 `/opt/texas`：

```bash
mkdir -p /opt/texas/repository /opt/texas/secrets /opt/texas/backups
chmod 700 /opt/texas/secrets
git clone https://github.com/wm-develop/Texas.git /opt/texas/repository
cd /opt/texas/repository
git tag --list --sort=version:refname
git checkout v0.1.1   # 或替换为准备部署的更新标签
```

如果 `repository` 已经存在且不是空目录，不要再次执行 `git clone`。应先确认其中是否已有仓库，再使用 `git status` 和 `git remote -v` 检查。

## 4. 创建 Docker 网络和 PostgreSQL

创建仅供容器通信的网络和持久化数据卷：

```bash
docker network create texas-internal
docker volume create texas-postgres-data
```

请生成独立的高强度数据库密码，并避免把密码直接保存在 Shell 历史中。创建 `/opt/texas/secrets/postgres.env`：

```dotenv
POSTGRES_DB=texas
POSTGRES_USER=texas
POSTGRES_PASSWORD=请替换为随机高强度密码
```

限制权限并启动数据库：

```bash
chmod 600 /opt/texas/secrets/postgres.env

docker run -d \
  --name texas-postgres \
  --restart unless-stopped \
  --network texas-internal \
  --env-file /opt/texas/secrets/postgres.env \
  -v texas-postgres-data:/var/lib/postgresql/data \
  --health-cmd='pg_isready -U texas -d texas' \
  --health-interval=10s \
  --health-timeout=5s \
  --health-retries=10 \
  postgres:17-alpine
```

不要使用 `postgres:latest`，以免一次普通更新意外跨越 PostgreSQL 主版本。`texas-postgres-data` 是 Docker 命名卷，可用下面的命令查看实际位置：

```bash
docker volume inspect texas-postgres-data
```

等待 `docker ps` 中数据库显示 `healthy` 后继续。

## 5. 配置游戏服务

创建 `/opt/texas/secrets/game-server.env`：

```dotenv
PORT=8080
STORAGE_BACKEND=postgres
DATABASE_URL=postgres://texas:数据库密码@texas-postgres:5432/texas?sslmode=disable
DATABASE_AUTO_MIGRATE=false

ALLOWED_ORIGINS=https://poker.example.com
AUTH_ACCESS_TOKEN_TTL_SECONDS=900
AUTH_REFRESH_TOKEN_TTL_SECONDS=2592000

TRTC_SDK_APP_ID=替换为自己的SDKAppID
TRTC_SECRET_KEY=替换为自己的SecretKey
TRTC_USER_SIG_EXPIRE_SECONDS=3600
TRTC_DEBUG_TOKEN=替换为随机调试口令
```

注意：

- `ALLOWED_ORIGINS` 必须是完整 Web 来源，不带路径和末尾斜杠；多个来源以英文逗号分隔。
- 数据库密码若包含 `@`、`:`、`/`、`?` 等 URL 特殊字符，必须在 `DATABASE_URL` 中进行百分号编码。
- 不需要语音时可以同时留空 `TRTC_SDK_APP_ID` 和 `TRTC_SECRET_KEY`；只填写其中一个会导致服务拒绝启动。
- 不要把该文件、TRTC 密钥、数据库密码或平台签名提交到 Git。

```bash
chmod 600 /opt/texas/secrets/game-server.env
```

## 6. 构建镜像并迁移数据库

在仓库根目录执行：

```bash
cd /opt/texas/repository
IMAGE_TAG="$(git describe --tags --always --dirty)"

docker build \
  -t "texas-game-server:${IMAGE_TAG}" \
  services/game_server
```

执行全部待应用迁移：

```bash
docker run --rm \
  --network texas-internal \
  --env-file /opt/texas/secrets/game-server.env \
  --entrypoint /usr/local/bin/migrate \
  "texas-game-server:${IMAGE_TAG}" \
  up
```

再次执行迁移应显示 `count: 0`，表示迁移具有幂等性：

```bash
docker run --rm \
  --network texas-internal \
  --env-file /opt/texas/secrets/game-server.env \
  --entrypoint /usr/local/bin/migrate \
  "texas-game-server:${IMAGE_TAG}" \
  up
```

## 7. 启动游戏服务

```bash
docker run -d \
  --name texas-game-server \
  --restart unless-stopped \
  --network texas-internal \
  --env-file /opt/texas/secrets/game-server.env \
  -p 127.0.0.1:8080:8080 \
  --health-cmd='wget -qO- http://127.0.0.1:8080/readyz || exit 1' \
  --health-interval=10s \
  --health-timeout=5s \
  --health-retries=10 \
  "texas-game-server:${IMAGE_TAG}"
```

核对运行状态：

```bash
docker ps --filter name=texas-game-server
docker logs --tail 100 texas-game-server
curl -i http://127.0.0.1:8080/healthz
curl -i http://127.0.0.1:8080/readyz
```

`readyz` 返回 HTTP 200 和 `{"status":"ready"}` 后再配置公网入口。

## 8. 配置 HTTPS 与 WebSocket 反向代理

为 `api-poker.example.com` 创建 HTTPS 站点，将全部请求反向代理到：

```text
http://127.0.0.1:8080
```

反向代理必须支持 WebSocket，并传递以下头部：

```nginx
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection "upgrade";
proxy_set_header Host $host;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;
proxy_read_timeout 3600s;
```

只需向公网开放 80/443。不要暴露 PostgreSQL 端口；游戏服务的 8080 端口也应仅绑定 `127.0.0.1`。

公网验证：

```bash
curl -i https://api-poker.example.com/healthz
curl -i https://api-poker.example.com/readyz
```

## 9. 构建和发布 Flutter Web

在安装了项目指定 Flutter OH 工具链的开发机执行：

```powershell
cd C:\path\to\Texas\apps\poker_client

flutter pub get
flutter build web --release `
  --dart-define=GAME_SERVER_URL=wss://api-poker.example.com/ws `
  --dart-define=GAME_HTTP_SERVER_URL=https://api-poker.example.com
```

将 `build/web` 内的全部文件上传到 `poker.example.com` 的静态网站根目录。不要只上传 `index.html`。站点应启用 HTTPS；若使用前端路由，还应把不存在的静态路径回退到 `index.html`。

浏览器打开 Web 站点后，确认 REST 请求访问 `https://api-poker.example.com`，WebSocket 连接访问 `wss://api-poker.example.com/ws`。

## 10. 构建其他客户端

Windows、Android 和 HarmonyOS 使用相同的两个 `--dart-define`：

```powershell
flutter build windows --release `
  --dart-define=GAME_SERVER_URL=wss://api-poker.example.com/ws `
  --dart-define=GAME_HTTP_SERVER_URL=https://api-poker.example.com

flutter build apk --release `
  --dart-define=GAME_SERVER_URL=wss://api-poker.example.com/ws `
  --dart-define=GAME_HTTP_SERVER_URL=https://api-poker.example.com

flutter build hap --release `
  --dart-define=GAME_SERVER_URL=wss://api-poker.example.com/ws `
  --dart-define=GAME_HTTP_SERVER_URL=https://api-poker.example.com
```

这些产物必须由部署者自行测试和签名：

- Windows 发布时需分发整个 `build/windows/x64/runner/Release` 目录。
- Android 正式发布前应配置自己的 Release 签名。
- HarmonyOS 需要在 DevEco Studio 中完成自己的签名；当前模块声明支持手机、平板和 PC/二合一设备。
- iOS 尚未作为本版本的正式交付平台。

## 11. 首次登录与管理员

服务器没有管理员时，在注册页连续点击“好友德州”上方牌图标 10 次，看到提示后注册。该账号会原子地成为首位管理员，之后隐藏入口不能再次提权。

进入管理员界面后，可以关闭公开注册，再按需创建熟人账号。项目中的充值和筹码均为无支付能力的娱乐虚拟筹码。

## 12. 备份和后续更新

至少定期备份 PostgreSQL：

```bash
docker exec texas-postgres \
  pg_dump -U texas -d texas -Fc \
  > "/opt/texas/backups/texas-$(date +%Y%m%d-%H%M%S).dump"
```

确认备份文件大小不为零，并将备份复制到服务器之外的安全位置。首次部署完成后，后续数据库迁移、服务镜像替换、回滚和 Web 更新请参阅[生产环境更新手册](PRODUCTION_UPDATE_GUIDE.md)。

## 13. 上线检查清单

- [ ] 数据库和游戏服务容器均正常运行，游戏服务为 `healthy`。
- [ ] 本机及公网 `/healthz`、`/readyz` 均返回 HTTP 200。
- [ ] PostgreSQL 和 8080 没有直接暴露到公网。
- [ ] Web 的 HTTPS、REST CORS 与 WebSocket Origin 均匹配。
- [ ] TRTC 密钥只存在于服务器私有环境文件中。
- [ ] 能完成注册、登录、创建房间、加入房间和一手完整牌局。
- [ ] Web、Windows、Android、HarmonyOS 按实际需要完成真机或目标设备验证。
- [ ] 已创建并验证第一份数据库备份。

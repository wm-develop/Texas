# 生产环境更新手册

本文用于每次发布后更新“好友德州”的单实例生产环境，覆盖 PostgreSQL 数据库迁移、Go 游戏服务更新和 Flutter Web 更新。示例域名与目录如下：

开始前应先查看[项目现状](PROJECT_STATUS.md)和对应版本 Release 说明，确认本次改动涉及数据库、游戏服务还是客户端。本文描述通用单实例更新流程，不代表项目已具备多实例故障接管。

| 项目 | 当前值 |
| --- | --- |
| 服务器仓库 | `/opt/texas/repository` |
| 生产环境变量 | `/opt/texas/secrets/game-server.env` |
| 数据库备份目录 | `/opt/texas/backups` |
| Docker 网络 | `texas-internal` |
| PostgreSQL 容器 | `texas-postgres` |
| 游戏服务容器 | `texas-game-server` |
| 游戏服务本机端口 | `127.0.0.1:8080` |
| Web 域名 | `https://web.example.com`（替换为实际域名） |
| API / WebSocket 域名 | `https://api.example.com`（替换为实际域名） |

下文统一以 `web.example.com` 和 `api.example.com` 作为占位域名，执行命令前必须替换为实际域名。API 域名应继续反向代理到 `http://127.0.0.1:8080`，Web 域名继续作为纯静态站点。正常版本更新不需要重新创建站点、证书或反向代理。

## 先判断需要更新哪些部分

| 本次改动 | 数据库迁移 | 游戏服务 | Web |
| --- | --- | --- | --- |
| `services/game_server/migrations` 有新迁移 | 必须 | 必须 | 视客户端改动而定 |
| 仅修改 `services/game_server` Go 代码 | 不需要 | 必须 | 通常不需要 |
| 修改 `apps/poker_client` 或共享客户端逻辑 | 不需要 | 视协议改动而定 | 必须 |
| 同时修改协议、服务端和客户端 | 视迁移而定 | 必须 | 必须 |
| 仅修改文档 | 不需要 | 不需要 | 不需要 |

数据库迁移和游戏服务更新都在服务器执行；Web 在 Windows 开发机编译后，将构建目录内容上传到宝塔网站根目录。

## 一、更新前准备

先登录服务器并进入仓库：

```bash
cd /opt/texas/repository
```

确认当前状态并记录正在运行的旧镜像，发生问题时可用于应用回滚：

```bash
git status --short
git rev-parse --short HEAD
OLD_IMAGE="$(docker inspect texas-game-server --format '{{.Config.Image}}')"
echo "$OLD_IMAGE"
```

服务器仓库正常情况下不应有未提交修改。如果 `git status --short` 有输出，先查清这些文件的来源，不要直接覆盖或删除。

### 备份生产数据库

每次存在数据库迁移时必须备份；普通服务更新也建议备份：

```bash
mkdir -p /opt/texas/backups
BACKUP_FILE="/opt/texas/backups/texas_$(date +%Y%m%d_%H%M%S).dump"
docker exec texas-postgres pg_dump -U texas -d texas -Fc > "$BACKUP_FILE"
ls -lh "$BACKUP_FILE"
```

看到备份文件大小不是 `0` 后再继续。备份保存在宿主机 `/opt/texas/backups`，不在代码仓库和 Docker 数据卷内。

### 拉取发布代码并构建镜像

```bash
git pull --ff-only origin main
IMAGE_TAG="$(git rev-parse --short HEAD)"
echo "$IMAGE_TAG"
docker build -t "texas-game-server:${IMAGE_TAG}" services/game_server
```

仅更新 Web 时执行到 `git pull` 即可，不需要构建服务镜像。需要更新数据库或游戏服务时继续构建；镜像标签等于当前 Git 短提交号，便于确认生产环境正在运行哪个版本。

## 二、更新数据库

只有新镜像构建成功后才执行迁移：

```bash
docker run --rm \
  --network texas-internal \
  --env-file /opt/texas/secrets/game-server.env \
  --entrypoint /usr/local/bin/migrate \
  "texas-game-server:${IMAGE_TAG}" \
  up
```

出现 `migration completed` 即表示迁移程序正常结束。`count` 是本次实际执行的迁移数量；没有新迁移时显示 `count: 0` 属于正常情况。

检查已应用的迁移：

```bash
docker exec texas-postgres \
  psql -U texas -d texas \
  -c "SELECT version, name, applied_at FROM schema_migrations ORDER BY version;"
```

也可以检查表是否仍然存在：

```bash
docker exec texas-postgres psql -U texas -d texas -c "\dt"
```

如果迁移失败，停止本次发布，不要替换仍在运行的旧游戏服务。保留完整错误日志、当前镜像标签和备份文件，再针对失败原因处理。

> 不要在生产数据库执行 `migrate down`。回滚服务镜像不等于回滚数据库；生产数据库恢复必须根据对应迁移的兼容性单独制定方案，不能直接套用开发环境的回滚命令。

### 可选：运行真实 PostgreSQL 集成测试

只有已经单独配置 `/opt/texas/secrets/game-server-test.env`，并且它连接的是可丢弃的测试数据库时才执行。严禁让测试环境变量指向生产数据库。

```bash
docker build --target build -t "texas-game-server-test:${IMAGE_TAG}" services/game_server
docker run --rm \
  --network texas-internal \
  --env-file /opt/texas/secrets/game-server-test.env \
  -e GOMAXPROCS=2 \
  "texas-game-server-test:${IMAGE_TAG}" \
  go test -p 1 ./... -count=1
```

如果该测试环境文件不存在，可以跳过本节，不能改用生产环境文件代替。

## 三、更新游戏服务

游戏服务收到停止信号后会先等所有牌桌打完当前手（最多 `SHUTDOWN_DRAIN_TIMEOUT_SECONDS`，默认 120 秒），所以 `docker stop` 必须用 `-t 150` 给足宽限期；Docker 默认 10 秒就会强制杀进程，正在进行的那一手会作废。牌桌上会显示「服务器即将更新」，本手结束后暂停开新局，新容器起来后玩家重新点准备即可。

数据库迁移成功后，替换游戏服务容器：

```bash
docker stop -t 150 texas-game-server
docker rm texas-game-server
docker run -d \
  --name texas-game-server \
  --restart unless-stopped \
  --network texas-internal \
  --env-file /opt/texas/secrets/game-server.env \
  -p 127.0.0.1:8080:8080 \
  "texas-game-server:${IMAGE_TAG}"
```

依次验证容器、本机接口和公网反向代理：

```bash
docker ps --filter name=texas-game-server
docker logs --tail 100 texas-game-server
curl -i http://127.0.0.1:8080/readyz
curl -i https://api.example.com/readyz
```

预期结果：

- 容器状态为 `Up`；如果容器配置了 Docker 健康检查，还应最终显示 `healthy`。
- 日志出现 `postgres storage enabled` 和 `game server listening`，没有持续的 `ERROR`。
- 两个 `/readyz` 请求都返回 `HTTP/1.1 200 OK` 和 `{"status":"ready"}`。

再用客户端完成一次注册或登录、进入牌桌和 WebSocket 连接验证。单独访问 `/readyz` 成功不能代替牌桌联机验证。

### 游戏服务快速回滚

仅当数据库改动与旧服务向后兼容时，才可以使用更新前记录的 `$OLD_IMAGE` 回滚应用：

```bash
docker stop -t 150 texas-game-server
docker rm texas-game-server
docker run -d \
  --name texas-game-server \
  --restart unless-stopped \
  --network texas-internal \
  --env-file /opt/texas/secrets/game-server.env \
  -p 127.0.0.1:8080:8080 \
  "$OLD_IMAGE"
curl -i http://127.0.0.1:8080/readyz
```

如果本次发布包含不兼容的数据库变更，不要直接执行上述回滚，应先停止写入并按该版本的迁移说明处理数据库。

## 四、编译和发布客户端

Web、Windows、Android 和 HarmonyOS 共用同一套 Dart 代码及生产服务地址。所有正式构建都在 Windows 开发机的客户端目录执行。

本节所有 PowerShell 命令使用三个路径变量。**每次新开 PowerShell 会话时先执行一次**，并把值改成本机实际路径：

```powershell
# 仓库检出目录，按本机实际路径修改
$repo    = 'D:\path\to\Texas'
# Flutter OH 安装目录，按本机实际路径修改
$flutter = 'D:\path\to\flutter_flutter\bin\flutter.bat'
# Android platform-tools 中的 adb，仅安装 APK 到真机时需要
$adb     = "$env:LOCALAPPDATA\Android\Sdk\platform-tools\adb.exe"

$client  = "$repo\apps\poker_client"
```

仓库自带的便携 Go 工具链固定在 `$repo\.toolchains\go`，跟随检出目录，不需要单独配置。

```powershell
cd $client
$env:GIT_LFS_SKIP_SMUDGE = '1'
& $flutter pub get
```

`GIT_LFS_SKIP_SMUDGE` 只跳过 HarmonyOS 音频插件仓库中与应用构建无关的示例工程大文件；实际插件源码和四个平台实现仍会完整下载。新电脑首次解析依赖时必须保留这一行，后续构建也可以一直保留。

生产构建统一使用：

```text
WebSocket：wss://api.example.com/ws
HTTP API：https://api.example.com
```

`GAME_SERVER_URL` 必须是 `wss://`，REST 地址必须是 `https://`。不要把本机的 `127.0.0.1` 或局域网 IP 编译进正式客户端。

### Web

编译命令：

```powershell
& $flutter build web --release `
  --dart-define=GAME_SERVER_URL=wss://api.example.com/ws `
  --dart-define=GAME_HTTP_SERVER_URL=https://api.example.com
```

构建产物位于：

```text
<仓库根>\apps\poker_client\build\web
```

#### 上传到宝塔静态站点

在宝塔中打开实际 Web 域名的网站根目录：

1. 先将当前网站文件打包备份，便于回退。
2. 删除或移走旧版本静态文件，但保留站点配置和 SSL 证书。
3. 上传 `build\web` **目录里面的全部内容**，不要再套一层 `web` 文件夹。
4. 确认网站根目录直接包含 `index.html`、`flutter_bootstrap.js`、`main.dart.js` 和 `assets`。

应整体替换旧文件，不要只覆盖 `main.dart.js`，否则资源清单和缓存文件可能来自不同版本。

#### 验证 Web

访问：

```text
https://web.example.com
```

至少验证以下内容：

- 页面和静态资源没有 `404`。
- 注册、登录和刷新页面正常。
- 能创建或加入房间，顶部显示牌桌已同步。
- 浏览器开发者工具中没有 WebSocket 连接错误。

如果浏览器仍显示旧界面，先强制刷新；仍未更新时，清除该站点的缓存和站点数据后重新打开。也可以在无痕窗口核对新版本。

### Windows

编译命令：

```powershell
& $flutter build windows --release `
  --dart-define=GAME_SERVER_URL=wss://api.example.com/ws `
  --dart-define=GAME_HTTP_SERVER_URL=https://api.example.com
```

完整产物目录：

```text
<仓库根>\apps\poker_client\build\windows\x64\runner\Release
```

发布或复制时必须包含 `Release` 目录里的 EXE、DLL 和 `data` 等全部文件，不能只复制 `poker_client.exe`。需要发送压缩包时，可以将整个目录压缩：

```powershell
$windowsRelease = "$client\build\windows\x64\runner\Release"
$windowsZip = "$client\build\Poker_windows_x64.zip"
Compress-Archive -Path "$windowsRelease\*" -DestinationPath $windowsZip -Force
```

### Android

编译 APK：

```powershell
& $flutter build apk --release `
  --dart-define=GAME_SERVER_URL=wss://api.example.com/ws `
  --dart-define=GAME_HTTP_SERVER_URL=https://api.example.com
```

产物位置：

```text
<仓库根>\apps\poker_client\build\app\outputs\flutter-apk\app-release.apk
```

当前 Flutter OH/TRTC 插件组合的 Android Release 已在项目中关闭 R8 和资源裁剪，否则安装包可能启动闪退。不要在未经完整真机验证时重新开启。当前 `android/app/build.gradle.kts` 仍使用调试签名生成 Release APK，适合熟人测试；正式对外分发前应配置独立的 Android 发布签名。

覆盖安装到已连接的 Android 设备可以使用：

```powershell
& $adb install -r `
  "$client\build\app\outputs\flutter-apk\app-release.apk"
```

### HarmonyOS

先确认 DevEco Studio 已生成本机签名配置，且 `apps\poker_client\ohos\build-profile.json5` 中引用的证书和密钥文件仍然有效。该文件包含本机签名信息并已被 Git 忽略，不得提交或复制到服务器。

编译已签名 HAP：

```powershell
& $flutter build hap --release `
  --dart-define=GAME_SERVER_URL=wss://api.example.com/ws `
  --dart-define=GAME_HTTP_SERVER_URL=https://api.example.com
```

产物位置：

```text
<仓库根>\apps\poker_client\build\ohos\hap\entry-default-signed.hap
```

如果只生成未签名 HAP，或构建提示签名材料不存在，应回到 DevEco Studio 重新完成自动签名后再编译。安装前确认手机允许调试/安装该签名应用。

### 四个平台的构建验证

每次准备发布某个平台时，至少确认：

- 构建命令以退出码 `0` 完成，产物修改时间是本次构建时间。
- 登录后能连接实际 API 域名，创建或加入房间正常。
- Android 和 HarmonyOS 在真机上能启动、横屏和全屏显示，不出现白屏或闪退。
- Windows 分发包保留全部依赖文件；Web 已清理旧缓存并验证 WebSocket。
- 涉及语音改动时，至少用两个不同平台完成一次加入语音、开关麦和互相收听测试。

## 五、配置何时需要修改

正常更新不要改 `/opt/texas/secrets/game-server.env`。只有域名、数据库连接、TRTC 凭据或明确新增环境变量时才修改。当前关键配置应满足：

```dotenv
STORAGE_BACKEND=postgres
DATABASE_AUTO_MIGRATE=false
ALLOWED_ORIGINS=https://web.example.com
AUTH_ACCESS_TOKEN_TTL_SECONDS=900
AUTH_REFRESH_TOKEN_TTL_SECONDS=2592000
TRUSTED_PROXIES=172.17.0.1
METRICS_TOKEN=替换为至少16位随机字符串
# 可选：低于该版本的客户端一律被拒（426），不配则完全不启用。
# 编码为 major*1000000 + minor*1000 + patch，与客户端 versionCode 一致（0.2.1 → 2001）。
MINIMUM_CLIENT_VERSION=2001
```

`MINIMUM_CLIENT_VERSION` 用于开发期强制朋友更新客户端：旧客户端连上新服务端常会出难以定位的问题。**必须等新客户端分发完成后再调高它**，否则还没更新的人会立刻被挡在门外（这正是它的作用，但要挑时机）。取值就是新客户端 `pubspec.yaml` 里 `+` 后面那个数。

`ALLOWED_ORIGINS` 必须使用英文逗号分隔完整来源，不能带路径、中文标点或末尾多余逗号。修改环境文件后必须重新创建游戏服务容器，单纯 `docker restart` 不会让容器重新读取修改后的 `--env-file`。

启用 15 分钟短期访问令牌前，应先把带自动刷新功能的新客户端分发到 Web、Windows、Android 和 HarmonyOS。仍在使用旧客户端时，可以临时将 `AUTH_ACCESS_TOKEN_TTL_SECONDS` 设为 `86400`；确认客户端全部更新后再改为 `900` 并重新创建服务容器。

域名未变化时，无需修改宝塔反向代理、WebSocket 配置、DNS 或 HTTPS 证书。

## 六、发布完成检查表

- [ ] 已记录旧镜像名称和更新前 Git 提交号。
- [ ] 数据库备份文件存在且大小不为 `0`。
- [ ] `git pull --ff-only` 和服务镜像构建成功。
- [ ] 新数据库迁移已执行并核对 `schema_migrations`。
- [ ] 游戏服务容器健康，本机和公网 `/readyz` 都返回 `200`。
- [ ] 服务日志无持续错误。
- [ ] Web 使用生产 `wss://` 和 `https://` 地址重新编译。
- [ ] `build\web` 的内容已整体上传至静态网站根目录。
- [ ] 本次需要发布的 Windows、Android、HarmonyOS 产物已使用生产地址构建并在对应真机验证。
- [ ] 已实际验证登录、WebSocket、创建/加入牌桌和一项牌局操作。

发布记录至少保留：Git 提交号、镜像标签、数据库备份文件名、迁移结果、发布时间和验证人。以后若某次版本包含特殊迁移或额外操作，应在发布说明中单独列出，本文只描述通用流程。

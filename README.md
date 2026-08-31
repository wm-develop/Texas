# 好友德州

仅供熟人组织私人牌局、不面向互联网公开传播的跨平台德州扑克项目。客户端基于 Flutter OH，目标平台为 Web、Windows、Android 和 HarmonyOS；服务端使用 Go。

## 项目状态

阶段 0～2 已完成，当前已经具备可供熟人联机测试的 MVP：

- 注册、登录、好友房、房间码和动态扩展至最多 10 人的座位/准备流程，创建房间无需预设人数。
- 房主身份在牌桌中明确标识；房主离桌后按加入先后顺序自动转移，最后一名玩家离桌时才关闭房间。
- 服务端权威的完整德州牌局，包括主池、边池、摊牌、倒计时、每手两张 30 秒加时卡和按小盲单位控制的自定义下注额度。
- 仅在每手结算后，弃牌玩家及因其他玩家全部弃牌而获胜的玩家可主动展示底牌；局中弃牌玩家可向任意仍参局玩家发起私下看牌申请。
- 空座位直接换座、满桌双方确认换位；仅剩两名未弃牌玩家、至少一人全下且另一人完成跟注或全下、后续无需行动时，才协商发一次或发两次；只有双方都选择两次才运行两块公共牌并分别结算。
- 每手结算后进入 10 秒自动准备倒计时，玩家可取消；普通行动 30 秒，场上只剩两名玩家时为 60 秒。
- WebSocket 事件序列、请求幂等、断线补发和私人快照安全恢复。
- 牌桌文字聊天、收起状态未读提醒、快捷语、表情、本地屏蔽和服务端禁言；点击其他玩家头像可发送带全桌动画与音效的赞赏或嘲讽。
- TRTC 自由麦语音、语音成员、开麦/说话状态、单人静音和播放音量。
- 最近牌局、结算历史、追加式筹码账本、基础设置、轻量牌桌动画，以及下注、All in、过牌和弃牌的独立牌桌音效。
- 一次性首管理员引导、服务器端权限校验、账号批量治理、在线/所在房间状态、强制离房、持久化文字禁言、用户名与筹码调整、密码重置、新用户注册开关和管理审计。
- 用户可从个人信息页修改登录用户名、牌桌昵称和密码；短期 Access Token 会在到期前自动使用轮换式 Refresh Token 更新，注销和账号治理可撤销会话。
- Web、Windows、Android、HarmonyOS 构建验证。
- 10 个独立 WebSocket 客户端连续完成 100 手，1000 条玩家账本记录逐手守恒。

阶段 3 的单实例纵向链路已完成。账户娱乐筹码余额、无支付虚拟充值、最近筹码流水、房主自定义盲注/最大带入、玩家自主带入、手间补码、输光自动补码和离桌返还均已接入；盲注最低为 10/20，普通下注和加注统一使用小盲整数倍。PostgreSQL 已覆盖账户/会话、钱包流水、房间成员、牌局行动与历史、结算账本、文字聊天和管理员治理；关键带入、补码、结算和返还采用事务、行锁与请求幂等。真实 PostgreSQL 集成测试及单实例云端部署已经完成，后续重点是 Redis 多实例恢复、备份演练和稳定性验收。执行顺序见 [阶段 3 计划](docs/PHASE_3_PLAN.md)。

> 游戏服务默认仍使用进程内仓储，方便无数据库开发。生产环境设置 `STORAGE_BACKEND=postgres` 并完成全部迁移后启用 PostgreSQL 持久化。本项目仍定位为熟人封闭联机游戏。

## 首位管理员

服务器尚无管理员时，在客户端注册页连续点击“好友德州”上方牌图标 10 次，看到提示后完成注册，该账号会成为首位管理员。该引导只生效一次；已有管理员后不能用隐藏入口再次提权。

管理员登录后可从右上角齿轮进入“服务器管理”，查看账号在线状态、筹码、文字禁言状态及所在房间，修改用户名和钱包筹码，将玩家请出房间，禁言或解除禁言，批量创建/停用/恢复/删除账号、重置密码，以及开启或关闭新用户注册。禁言对已连接牌桌立即生效，并在 PostgreSQL 中持久化；删除为保留历史数据的软删除。正在牌桌中的账号必须先离桌才能调整筹码、停用或删除。所有已登录用户均可点击大厅右上角自己的登录用户名进入个人信息页，修改登录用户名、牌桌昵称和密码。

## 文档索引

- [产品与架构规划](docs/PRODUCT_ARCHITECTURE_PLAN.md)
- [阶段 0：技术验证](docs/PHASE_0_CHECKLIST.md)
- [阶段 1：规则与协议](docs/PHASE_1_CHECKLIST.md)
- [阶段 2：可玩 MVP](docs/PHASE_2_CHECKLIST.md)
- [阶段 2.1：牌桌可玩性补全](docs/PHASE_2_1_TABLE_PLAYABILITY.md)
- [阶段 2 验收记录](docs/PHASE_2_ACCEPTANCE.md)
- [阶段 3：上线准备计划](docs/PHASE_3_PLAN.md)
- [从零自建部署指南](docs/SELF_HOSTING_GUIDE.md)
- [生产环境更新手册](docs/PRODUCTION_UPDATE_GUIDE.md)
- [v0.1.0 首次发布说明](docs/releases/v0.1.0.md)
- [德州扑克规则规格 v1](docs/TEXAS_HOLDEM_RULES_V1.md)
- [WebSocket 协议 v1](docs/WEBSOCKET_PROTOCOL_V1.md)
- [语音 RTC 决策记录](docs/decisions/ADR-001-VOICE-RTC.md)

## 目录结构

```text
apps/poker_client/       Flutter OH 客户端
services/game_server/    Go 游戏服务
docs/                    产品、架构、阶段计划和技术决策
.env.example             本地配置模板
```

`.toolchains`、`.cache`、`.tmp`、构建目录、IDE 配置和真实密钥均为本地文件，不提交版本库。

## 环境要求

- Flutter OH：`oh-3.41.9-dev` 系列，项目验证版本为 `3.41.10-ohos-0.0.3-beta`。
- Go：1.27 或兼容版本。
- Android：JDK 17、Android SDK/NDK。
- HarmonyOS：DevEco Studio、API 26 SDK，并在本机完成签名配置。
- Windows：Visual Studio 及 Windows 桌面开发组件。
- Web：Edge 或 Chrome。

首次运行前，将 `.env.example` 复制为 `.env`，填写自己的 TRTC 配置。`.env` 和 HarmonyOS 本机签名配置已被 Git 忽略，禁止提交真实密钥。

## 启动服务端

在 `services/game_server` 目录运行：

```powershell
go run .\cmd\server
```

若 Go 未加入 `PATH`，可使用项目外或本地忽略目录中的 Go 可执行文件。服务默认监听 `http://127.0.0.1:8080`，也会从仓库根目录 `.env` 加载 TRTC 配置。

## 运行客户端

在 `apps/poker_client` 目录执行。Web 和 Windows 访问本机服务时可以使用：

```powershell
flutter run -d edge `
  --dart-define=GAME_SERVER_URL=ws://127.0.0.1:8080/ws `
  --dart-define=GAME_HTTP_SERVER_URL=http://127.0.0.1:8080
```

```powershell
flutter run -d windows `
  --dart-define=GAME_SERVER_URL=ws://127.0.0.1:8080/ws `
  --dart-define=GAME_HTTP_SERVER_URL=http://127.0.0.1:8080
```

Android 和 HarmonyOS 真机必须把 `127.0.0.1` 换成手机能够访问的电脑局域网地址，或使用已经配置好的 ADB/HDC 端口转发：

```powershell
flutter run -d <设备ID> `
  --dart-define=GAME_SERVER_URL=ws://<电脑IP>:8080/ws `
  --dart-define=GAME_HTTP_SERVER_URL=http://<电脑IP>:8080
```

# 好友德州

仅供熟人组织私人牌局、不面向互联网公开传播的跨平台德州扑克项目。客户端基于 Flutter OH，目标平台为 Web、Windows、Android 和 HarmonyOS；服务端使用 Go。

## 项目状态

阶段 0～2 已完成，当前已经具备可供熟人联机测试的 MVP：

- 注册、登录、好友房、房间码和 2～10 人座位/准备流程。
- 服务端权威的完整德州牌局，包括主池、边池、摊牌、倒计时、每手两张 30 秒加时卡和自定义下注额度。
- WebSocket 事件序列、请求幂等、断线补发和私人快照安全恢复。
- 牌桌文字聊天、快捷语、表情、本地屏蔽和服务端禁言。
- TRTC 自由麦语音、语音成员、开麦/说话状态、单人静音和播放音量。
- 最近牌局、结算历史、追加式筹码账本、基础设置、提示音和轻量牌桌动画。
- Web、Windows、Android、HarmonyOS 构建验证。
- 10 个独立 WebSocket 客户端连续完成 100 手，1000 条玩家账本记录逐手守恒。

下一阶段是上线准备。第一条产品链路将补齐账户娱乐筹码余额、无支付的虚拟充值、房主自定义盲注/最大带入、玩家自主带入及每手结束后的补码；随后完成 PostgreSQL/Redis 持久化、多实例运行、管理治理、可观测性、安全加固、自动发布和 24 小时稳定性验收。执行顺序见 [阶段 3 计划](docs/PHASE_3_PLAN.md)。

> 当前服务端默认使用进程内仓储。服务重启后账号、会话、房间、聊天和最近牌局会清空，因此现阶段只适合本地及封闭联机测试。

## 文档索引

- [产品与架构规划](docs/PRODUCT_ARCHITECTURE_PLAN.md)
- [阶段 0：技术验证](docs/PHASE_0_CHECKLIST.md)
- [阶段 1：规则与协议](docs/PHASE_1_CHECKLIST.md)
- [阶段 2：可玩 MVP](docs/PHASE_2_CHECKLIST.md)
- [阶段 2.1：牌桌可玩性补全](docs/PHASE_2_1_TABLE_PLAYABILITY.md)
- [阶段 2 验收记录](docs/PHASE_2_ACCEPTANCE.md)
- [阶段 3：上线准备计划](docs/PHASE_3_PLAN.md)
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

## 质量检查与构建

服务端：

```powershell
go vet ./...
go test ./...
```

客户端：

```powershell
flutter analyze
flutter test
flutter build web
flutter build windows --debug
flutter build apk --debug
flutter build hap --debug
```

WebSocket 单客户端冒烟工具：

```powershell
dart run tool\websocket_smoke.dart
```

10 客户端/100 手协议验收已经包含在 Go 测试套件中。

## 仓库安全约定

- 不提交 `.env`、TRTC SecretKey、调试令牌、证书、私钥、签名口令或本机绝对路径。
- 不提交 APK、HAP、Windows/Web 构建目录、本地 SDK、缓存和临时下载文件。
- Web TRTC SDK 位于 `apps/poker_client/web/vendor`，是客户端离线运行所需的受控依赖，应随源码提交。
- 提交前至少运行 `go test ./...`、`flutter analyze` 和 `flutter test`。

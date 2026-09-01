# 好友德州客户端

基于 Flutter OH 的横屏客户端，目标平台为 Web、Windows、Android 和 HarmonyOS。完整项目说明见仓库根目录的 [README](../../README.md)，当前实现边界和接手说明见[项目现状](../../docs/PROJECT_STATUS.md)与[项目交接文档](../../docs/PROJECT_HANDOVER.md)。

## 运行

先启动 Go 游戏服务，然后在本目录执行：

```powershell
flutter pub get
flutter run -d <设备ID> `
  --dart-define=GAME_SERVER_URL=ws://<服务地址>:8080/ws `
  --dart-define=GAME_HTTP_SERVER_URL=http://<服务地址>:8080
```

Web/Windows 与服务端在同一台电脑时，服务地址可以使用 `127.0.0.1`；Android/HarmonyOS 真机应使用电脑局域网地址或 ADB/HDC 端口转发。

## 检查

```powershell
flutter analyze
flutter test
```

## 平台说明

- 使用 Flutter OH `oh-3.41.9-dev` 系列工具链。
- TRTC 原生端依赖 `tencent_rtc_sdk 13.4.3`。
- Web 使用仓库内 `web/vendor/trtc/trtc-5.19.1.js`，不依赖运行时 CDN。
- HarmonyOS 根目录 `build-profile.json5` 含本机签名信息，已被 Git 忽略。
- Android 与 HarmonyOS 使用原生挖孔 API，经 MethodChannel 补充 Flutter 未覆盖的安全区；不要改回固定像素边距。
- HarmonyOS 的房间码和金额输入使用自定义横屏数字面板，避免系统输入面板数字不可达或闪退。
- 所有平台默认横屏运行；移动端进入应用后使用全屏模式。
- 当前未引入 Riverpod 或 `go_router`，页面和会话由 `StatefulWidget`、`ChangeNotifier` 及显式 service/client 对象组织。

## 维护重点

牌桌主页面 `lib/features/table/presentation/table_prototype_page.dart` 体积较大。继续增加牌桌功能前，建议按连接/命令、牌桌画布、聊天/语音、动作栏和弹窗拆分，并保留以下层级：公共牌背景 < 玩家框 < 本轮下注筹码 < 赞赏/嘲讽气泡。本人手牌和摊牌都在对应玩家框内显示。

Web、Windows、Android 和 HarmonyOS 的生产构建命令、产物路径与发布注意事项见[生产环境更新手册](../../docs/PRODUCTION_UPDATE_GUIDE.md)。

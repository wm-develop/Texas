# 好友德州客户端

基于 Flutter OH 的横屏客户端，目标平台为 Web、Windows、Android 和 HarmonyOS。完整项目说明见仓库根目录的 [README](../../README.md)。

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
- 所有平台默认横屏运行。

Web、Windows、Android 和 HarmonyOS 的生产构建命令、产物路径与发布注意事项见[生产环境更新手册](../../docs/PRODUCTION_UPDATE_GUIDE.md)。

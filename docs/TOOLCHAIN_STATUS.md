# 开发环境与工具链状态

> 检查日期：2026-08-24

## 已可用

| 工具 | 当前状态 |
|---|---|
| 操作系统 | Windows 11，版本 `10.0.26100.7171` |
| Flutter OH | `3.41.10-ohos-0.0.3-beta`，分支 `oh-3.41.9-dev` |
| Flutter framework revision | `25746761c1` |
| Flutter 仓库 HEAD | `f8a77a6341be239b175d1eac5d0dbacbb12cdbb7` |
| Dart | `3.11.5` |
| HarmonyOS SDK | API 26，位于 DevEco Studio 安装目录 |
| OHPM | `26.0.0.410` |
| HarmonyOS Node | `24.14.1` |
| Java | `17.0.19 LTS` |
| Android Studio / SDK | 已安装，SDK 位于用户目录，Android 平台 31/33/35/36/37 可用 |
| Android NDK | `28.2.13676358`（r28c） |
| Android CMake | `3.22.1` |
| Visual Studio | Professional 2019 `16.11.25` |
| Windows SDK | `10.0.19041.0` |
| Go | 官方便携版 `1.27.0 windows/amd64` |
| Web 调试 | Microsoft Edge 可用 |

Flutter 实际位于：

```text
C:\Programming\env\flutter_flutter
```

Go 便携工具链位于：

```text
C:\Programming\Texas\.toolchains\go
```

Go 下载包已使用官方 SHA256 校验：

```text
f0c0a0d33ba94f4d2c5dbc887334ce678b21813504ddb3aafcb06e60a5a667c4
```

## 尚未完成

| 项目 | 影响 | 后续动作 |
|---|---|---|
| Docker | 暂时不能启动 PostgreSQL/Redis 容器 | 阶段 3 开始持久化与多实例开发前安装 |
| Chrome | Flutter Doctor 未发现 Chrome | 当前用 Edge，不构成阻塞 |
| Android 正式签名 | 当前 APK 使用调试签名 | 发布前配置 keystore；不影响阶段 0 真机联调 |

## 已验证构建

- Web Release：成功。
- Windows Release：成功，生成包含 TRTC SDK 的 `poker_client.exe`。
- HarmonyOS Release：成功，生成包含 TRTC SDK 的 `entry-default-signed.hap`。
- Android Debug：成功，生成包含 TRTC SDK 的 `app-debug.apk`。

## 真机联调结果

> 验证日期：2026-08-24

- Android：Android 16（API 36），`arm64`，无线 ADB 调试。
- HarmonyOS：OpenHarmony 7.0.0.102（API 26），`arm64`，无线 HDC 调试。
- 两端均能通过反向端口连接本机 Go 服务并获取短期 UserSig。
- 两端均能加入同一 TRTC 房间、申请麦克风权限并开启自由麦。
- Android 到 HarmonyOS、HarmonyOS 到 Android 的语音均可正常听见。
- Android 主动退出语音后，牌局 WebSocket 继续保持“服务端已连接”。
- Android 加入语音但未开启自由麦时使用媒体音量；开启自由麦后切换为通话音量，双向语音正常。
- Windows 与 HarmonyOS 双向语音正常；Windows 退出语音后牌局 WebSocket 保持连接。
- Web 已接入 `trtc-sdk-v5 5.19.1`；自动化桥接测试、Release 构建以及 Edge 与 Android 双向通话均通过。
- Web 加入语音时默认不申请麦克风权限；开启自由麦时才申请，关闭后浏览器释放麦克风占用。
- Web 退出语音后牌局 WebSocket 继续保持“服务端已连接”。
- 两端均已锁定为横屏方向；当前视觉样式仍属于技术原型，后续单独优化。

## 已确认的兼容性约束

- `tencent_rtc_sdk` 固定为 `13.4.3`；该版本直接提供 Android、Windows 和 HarmonyOS Flutter 插件。Web 使用本地打包的 `trtc-sdk-v5 5.19.1`，不依赖运行时 CDN。
- TRTC 的 HarmonyOS 字节码 HAR 要求项目启用 `useNormalizedOHMUrl`。
- 权限门面固定为 `permission_handler 12.0.3`。13.x 当前要求的 Android Gradle/Kotlin 组合高于本项目 Flutter OH 工具链。
- HarmonyOS 权限实现使用 OpenHarmony 分支的 `permission_handler_ohos 10.3.2`，通过平台接口覆盖与通用权限门面共存。
- DevEco Studio 会把本机签名材料写入 `ohos/build-profile.json5`，该文件已加入忽略清单，不得提交或分享。
- 牌桌语音使用 `TRTCAppScene.voiceChatRoom`：麦下以 `audience` 身份接收，开麦前切换为 `anchor`，关麦后切回 `audience`。不要改回 `audioCall`，否则 Android 会在麦下仍使用通话音量。

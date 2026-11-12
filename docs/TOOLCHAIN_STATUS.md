# 开发环境与工具链状态

> 最近盘点：2026-11-12（macOS / Apple Silicon）。上一版 Windows 盘点（2026-08-24）保留在文末「历史记录」，其中的版本组合仍是已经跑通过的参考。
>
> 本文记录已跑通的工具版本组合，不代表新维护者必须使用相同绝对路径。接手者应先运行 `flutter doctor -v`、`flutter devices`、`go version` 并记录自己的环境。

## 当前工具链（macOS，2026-11-12）

| 工具 | 当前状态 |
|---|---|
| 操作系统 | macOS `26.7.2`，Apple Silicon（`darwin-arm64`） |
| Flutter OH | `3.44.9+ohos-0.0.1-canary1`，分支 `oh-3.44.9-dev` |
| Flutter framework revision | `17de980673` |
| Flutter 仓库 HEAD | `17de980673387d9b739e9e3558091b7c7583d50f` |
| Dart | `3.12.2` |
| HarmonyOS SDK | API 26，DevEco Studio 26.0.0 自带 |
| OHPM | `26.0.0.630` |
| HarmonyOS Node | `24.14.1` |
| Java | Oracle JDK `17.0.12` |
| Android Studio / SDK | Android Studio 2026.2；SDK 位于 `~/Library/Android/sdk`，平台 36 与 Build-Tools 37 可用 |
| Android NDK | `28.2.13676358`（r28c） |
| Android CMake | `3.22.1` |
| Go | 未安装；仓库内 `.toolchains` 是 `windows/amd64` 版本，在 macOS 上不可用，需要另装 macOS 版 |
| Web 调试 | 未安装 Chrome；可用 Safari，或另装 Chrome/Edge |

macOS 上需要特别注意：

- Android Studio 2026.2 自带的 JBR 是 **25**，而 Gradle 8.14 / AGP 8.11.1 要求 JDK 17。`flutter build` 会优先使用 Android Studio 的 JBR，必须用 `flutter config --jdk-dir=<JDK 17 路径>` 固定，只设 `JAVA_HOME` 不够；Android Studio 自身也要把 Gradle JDK 设为 17。
- 首次克隆后 `ohos/build-profile.json5` 不存在（被版本库忽略），先把同目录的 `关于build-profile.json5` 复制过去，再在 DevEco Studio 里生成本机签名。
- Windows 包只能在 Windows 主机上构建，macOS 无法交叉编译；本机可覆盖 Web、Android 和 HarmonyOS。

Flutter OH 位于（示例）：

```text
/Users/wm/Programming/flutter_flutter
```

Android 正式签名已支持：配置 `android/key.properties` 后使用独立发布密钥，缺失时回退调试签名并告警，步骤见[Android 发布签名配置指南](ANDROID_SIGNING_GUIDE.md)。

## 尚未完成

| 项目 | 影响 | 后续动作 |
|---|---|---|
| Windows 本地 Docker | 2026-08-24 盘点时未安装 | 可使用外部测试 PostgreSQL；生产环境的数据库与服务均以容器运行 |
| Chrome | macOS 上未安装 Chrome | 可用 Safari 调试 Web，或另装 Chrome/Edge，不构成阻塞 |
| Windows 构建 | macOS 不能交叉编译 Windows 桌面端 | 在 Windows 主机或 CI 上构建，命令见[生产环境更新手册](PRODUCTION_UPDATE_GUIDE.md) |
| iOS / macOS 桌面 | 仓库尚无 `ios/`、`macos/` 目录 | 尚未纳入构建与验收范围，需要时先补平台工程与签名 |

## 已验证构建

> 以下结果来自 2026-08-24 的 Windows 机器 + Flutter OH 3.41 组合。切换到 3.44.9 后需要逐端复验，未复验的项目不要当作已支持。

- Web Release：成功。
- Windows Release：成功，生成包含 TRTC SDK 的 `poker_client.exe`。
- HarmonyOS Release：成功，生成包含 TRTC SDK 的 `entry-default-signed.hap`。
- Android Release：成功，生成包含 TRTC SDK 的 `app-release.apk`；当前 Flutter OH/插件组合需关闭 R8 代码和资源裁剪，否则产品包会在启动阶段闪退。

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
- 两端均已锁定为横屏方向；此条记录的是 2026-08-24 的技术原型，后续已完成多轮移动牌桌布局优化。
- 后续版本已改为移动端应用级全屏，并通过 Android `WindowInsets.displayCutout` 与 HarmonyOS `getWindowAvoidArea(TYPE_CUTOUT)` 获取真实挖孔区域。
- HarmonyOS 后续已加入横屏数字输入面板并扩展手机、平板、PC/二合一设备声明；这些改动仍需在新设备上逐项冒烟。

## 已确认的兼容性约束

- `tencent_rtc_sdk` 固定为 `13.4.3`；该版本声明了 Android、OHOS、iOS、Windows 和 macOS 平台。项目当前只验证 Android、Windows 与 HarmonyOS，Web 使用本地打包的 `trtc-sdk-v5 5.19.1`，不依赖运行时 CDN。
- TRTC 的 HarmonyOS 字节码 HAR 要求项目启用 `useNormalizedOHMUrl`。
- 权限门面固定为 `permission_handler 12.0.3`。13.x 当前要求的 Android Gradle/Kotlin 组合高于本项目 Flutter OH 工具链。
- HarmonyOS 权限实现使用 OpenHarmony 分支的 `permission_handler_ohos 10.3.2`，通过平台接口覆盖与通用权限门面共存。
- DevEco Studio 会把本机签名材料写入 `ohos/build-profile.json5`，该文件已加入忽略清单，不得提交或分享；克隆后先复制 `ohos/关于build-profile.json5`。
- 牌桌语音使用 `TRTCAppScene.voiceChatRoom`：麦下以 `audience` 身份接收，开麦前切换为 `anchor`，关麦后切回 `audience`。不要改回 `audioCall`，否则 Android 会在麦下仍使用通话音量。

## 历史记录：Windows 开发机（2026-08-24 盘点）

| 工具 | 当时状态 |
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

路径说明：上面记录的是 2026-08-24 盘点时那台开发机的位置，**仅供参考，不是项目约定**。仓库和 Flutter 都可以放在任意目录，接手后请以自己的实际路径为准。

Go 便携工具链跟随仓库，始终位于仓库内的忽略目录：

```text
<仓库根>\.toolchains\go
```

这是唯一一条与位置有关的稳定约定：`.toolchains` 是仓库相对路径，跟着检出目录走。

Go 下载包已使用官方 SHA256 校验：

```text
f0c0a0d33ba94f4d2c5dbc887334ce678b21813504ddb3aafcb06e60a5a667c4
```

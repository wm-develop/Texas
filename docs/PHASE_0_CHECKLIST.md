# 阶段 0：跨平台技术验证清单

## 产品与架构

- [x] 产品范围完成评审。
- [x] 单桌人数调整为 2～10 人。
- [x] 明确仅供熟人使用，不公开传播。
- [x] 明确注册账号和邀请码边界。
- [x] 语音选用腾讯云 TRTC。
- [x] 语音故障与牌局连接隔离。

## Flutter 客户端

- [x] 使用 `oh-3.41.9-dev` 创建 Web、Windows、Android、OHOS 工程。
- [x] 建立 10 人横屏牌桌原型。
- [x] 展示本手结束后才能入座的空座状态。
- [x] 建立文字聊天交互占位。
- [x] 建立主动加入语音和自由麦交互占位。
- [x] 建立 `VoiceChatService` 平台隔离接口。
- [x] 建立 TRTC 短期凭证客户端与 Android/Windows/HarmonyOS 语音实现。
- [x] 建立麦克风按需授权、单人静音、说话状态和重连状态处理。
- [x] 建立跨平台 WebSocket 客户端。
- [x] Flutter Analyze 无问题。
- [x] Flutter Widget Test 通过。

## Go 服务端

- [x] 安装 Go 1.27.0 便携工具链。
- [x] 建立模块化服务端骨架。
- [x] 建立 `/healthz` 健康检查。
- [x] 建立 `/ws` WebSocket 端点。
- [x] 建立协议 Envelope。
- [x] 建立 ping/pong 和文字消息确认原型。
- [x] Go Vet 与测试通过。
- [x] Dart 客户端到 Go 服务端端到端冒烟测试通过。

## 平台构建

- [x] Web Release 构建成功。
- [x] Windows Release 构建成功。
- [x] HarmonyOS 生成签名 HAP（包含 TRTC SDK）。
- [x] HarmonyOS API 26 真机安装并运行横屏牌桌。
- [x] Android API 36 真机安装并运行横屏牌桌。
- [x] 安装 Android Studio、JDK 17、Android SDK、NDK 和 CMake。
- [x] Android Debug APK 构建成功（包含 TRTC SDK）。
- [ ] Android Release 使用正式签名构建成功。

## 腾讯云 TRTC

- [x] 完成技术方向决策。
- [x] 确定 Android/Windows/HarmonyOS 使用 Flutter RTC SDK。
- [x] 确定 Web 使用 TRTC Web SDK 桥接。
- [x] 创建腾讯云 TRTC 应用。
- [x] 服务端实现 UserSig 签发接口并通过真实配置冒烟验证。
- [x] 接入 `tencent_rtc_sdk 13.4.3`。
- [x] Web 接入本地打包的 `trtc-sdk-v5 5.19.1`，并完成 Dart/JavaScript 语音适配层。
- [x] Web TRTC 桥接的入房、角色切换、音量回调、单人静音和退房模拟测试通过。
- [x] Android 与 HarmonyOS 原生依赖构建验证通过。
- [x] Android 最小语音通话验证。
- [x] Windows 最小语音通话验证。
- [x] Web 最小语音通话验证。
- [x] HarmonyOS 最小语音通话验证。
- [x] Android 与 HarmonyOS 双向语音互通验证。
- [x] Android 退出 TRTC 后牌局 WebSocket 保持连接。
- [x] Android 麦下使用媒体音量、开麦切换通话音量验证。
- [x] Windows 与 HarmonyOS 双向语音互通验证。
- [x] Windows 退出 TRTC 后牌局 WebSocket 保持连接。
- [x] Web 与 Android 双向语音互通验证。
- [x] Web 退出 TRTC 后牌局 WebSocket 保持连接。
- [x] Web 加入语音时默认不申请麦克风权限；开麦时申请，关麦后释放占用。
- [ ] 10 人自由麦弱网和音频焦点验证。

## 阶段退出条件

以下条件全部满足后进入正式功能开发：

- [x] Web、Windows、Android、HarmonyOS 均能运行牌桌原型。
- [ ] 四端能够连接同一个 Go 服务端。
- [ ] 四端能够发送和接收牌桌文字消息。
- [x] 四端的 TRTC 实现均已验证可进入相同的字符串房间体系。
- [x] 关闭或断开 TRTC 后，牌局 WebSocket 仍然正常（Android、Windows、Web 验证）。
- [x] 工具链、权限和插件兼容问题均有记录和解决方案。

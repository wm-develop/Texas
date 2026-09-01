# ADR-001：牌桌语音采用腾讯云 TRTC

> 状态：已接受并已在 Web、Windows、Android、HarmonyOS 落地
>
> 日期：2026-08-24

## 背景

首版需要支持 2～10 人牌桌自由麦语音，并覆盖 Web、Windows、Android 和 HarmonyOS。当前只有一台腾讯云轻量应用服务器，不适合同时承担业务服务、数据库和多人音频媒体转发，也缺少自建 SFU 所需的长期运维与多平台 SDK 能力。

## 决策

采用腾讯云 TRTC 承担实时语音媒体传输。轻量应用服务器只负责：

- 校验注册用户及牌桌座位权限。
- 为合法用户生成短期 TRTC `UserSig`。
- 返回 `SDKAppID`、用户 ID、房间 ID 和临时凭证。
- 通过游戏 WebSocket 同步当前语音加入和开麦状态。长期持久化加入/离开元数据与举报尚未实现。

不在轻量服务器上转发或录制音频。

## 平台接入方式

| 平台 | 接入方式 |
|---|---|
| Android | `tencent_rtc_sdk` Flutter FFI SDK |
| Windows | `tencent_rtc_sdk` Flutter FFI SDK |
| Web | TRTC Web SDK，通过 Dart Web 互操作封装 |
| HarmonyOS | `tencent_rtc_sdk` Flutter FFI/HAR 插件 |
| iOS | 不在首版范围，后续复用 Flutter RTC SDK |

项目验证使用的 `tencent_rtc_sdk 13.4.3` 已直接包含 Android、Windows 和 HarmonyOS 插件，因此这三端共享同一套 Dart 适配实现。该插件仍不提供 Web 实现，因此 Web 单独使用网页 SDK。

参考资料：

- [腾讯云 TRTC 新手指引](https://cloud.tencent.com/document/product/647/49327)
- [腾讯云 TRTC Flutter 快速接入](https://cloud.tencent.com/document/product/647/116547/)
- [腾讯云 TRTC HarmonyOS 快速接入](https://cloud.tencent.com/document/product/647/130532)
- [腾讯 RTC Flutter SDK](https://pub.dev/packages/tencent_rtc_sdk)

## 客户端边界

Flutter 业务层只依赖 `VoiceChatService`，不得直接依赖 TRTC 类。各平台实现负责：

- 加入和退出牌桌语音频道。
- 开启和关闭自由麦。
- 麦下以听众身份使用媒体音量，开麦时切换为主播身份和通话音量。
- 远端用户静音。
- 说话状态和音量提示。
- 音频焦点、耳机和蓝牙切换。
- 独立重连和错误降级。

语音连接异常不得改变牌桌 WebSocket 状态，也不得阻塞出牌和结算。

## 隐私与治理

- 用户主动加入语音，不自动加入收听。
- 加入后仍需用户明确开启麦克风。
- 首版不录音。
- 房主没有一键禁言能力。
- 用户可以单独屏蔽其他玩家。
- 管理员可停用账号；当前持久化“禁言”只限制文字聊天，不是 RTC 服务端禁麦。

## 代价与风险

- 产生第三方 RTC 用量费用。
- Flutter FFI/HAR 与 Web 存在两种接入路径，需要统一适配层。
- HarmonyOS SDK 与 Flutter OH 分支的兼容性必须在阶段 0 真机验证。
- 正式开发前需要创建 TRTC 应用并安全保存 SDK 密钥；密钥只能存在于服务端。

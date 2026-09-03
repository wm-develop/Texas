/// 客户端版本。
///
/// [appVersionCode] 是跨端统一的单调整数，编码方式为
/// `major*1000000 + minor*1000 + patch`（0.2.1 → 2001）。服务端的
/// `MINIMUM_CLIENT_VERSION` 用同一套编码，比较时没有歧义。
///
/// 这里写成常量而不是用 package_info_plus 读平台清单：鸿蒙的插件生态不稳，
/// 为一个版本号多引一个原生依赖不划算。漏改的风险由 app_version_test.dart
/// 兜住——它断言这里、`pubspec.yaml` 与鸿蒙 `AppScope/app.json5` 三处一致。
///
/// 发版时三处一起改：
/// - `pubspec.yaml` 的 `version: <name>+<code>`
/// - `ohos/AppScope/app.json5` 的 `versionName` 与 `versionCode`
/// - 本文件
const String appVersionName = '0.2.1';
const int appVersionCode = 2001;

/// 客户端上报自身版本的两种方式。浏览器的 WebSocket API 不允许设置自定义
/// 请求头，因此 WS 走查询参数，普通 HTTP 走请求头；服务端两者都认。
const String clientVersionHeader = 'X-Client-Version';
const String clientVersionQuery = 'clientVersion';

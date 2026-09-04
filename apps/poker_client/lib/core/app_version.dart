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
const String appVersionName = '0.4.0';
const int appVersionCode = 4000;

/// 客户端上报自身版本的两种方式。浏览器的 WebSocket API 不允许设置自定义
/// 请求头，因此 WS 走查询参数，普通 HTTP 走请求头；服务端两者都认。
const String clientVersionHeader = 'X-Client-Version';
const String clientVersionQuery = 'clientVersion';

/// 把版本号整数还原成 `major.minor.patch`。
///
/// 服务端只传整数（门禁比较的也是整数），界面要显示成人能读的形式。
/// 0 或负数表示「未设置」。
String describeVersionCode(int versionCode) {
  if (versionCode <= 0) return '未限制';
  final major = versionCode ~/ 1000000;
  final minor = (versionCode ~/ 1000) % 1000;
  final patch = versionCode % 1000;
  return '$major.$minor.$patch';
}

/// 把一段输入解析成版本号整数。空串表示不限制（0）；非法输入返回 null，
/// 由界面提示，而不是悄悄当成 0 把门禁关掉。
int? parseVersionCode(String input) {
  final trimmed = input.trim();
  if (trimmed.isEmpty) return 0;
  final value = int.tryParse(trimmed);
  if (value == null || value < 0) return null;
  return value;
}

/// 把 `major.minor.patch` 编成版本号整数，与 [describeVersionCode] 互逆。
///
/// 格式非法（段数不对、有非数字、minor 或 patch 超过 999）时返回 null：
/// minor 和 patch 各只有三位容量，超了会串到上一段去。major 没有上限。
int? encodeVersionName(String name) {
  final parts = name.trim().split('.');
  if (parts.length != 3) return null;
  final numbers = <int>[];
  for (final part in parts) {
    final value = int.tryParse(part);
    if (value == null || value < 0) return null;
    numbers.add(value);
  }
  if (numbers[1] > 999 || numbers[2] > 999) return null;
  return numbers[0] * 1000000 + numbers[1] * 1000 + numbers[2];
}

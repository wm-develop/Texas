import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:poker_client/core/app_version.dart';

/// 版本过旧时的阻断页。
///
/// 开发期服务端改动频繁，旧客户端连上新服务端会出各种难以定位的问题，
/// 因此这里完全阻断而不是提示：不给「继续使用」的出口。
///
/// 只告知版本号，不给下载渠道——安装包由房主自行分发。
class UpdateRequiredPage extends StatelessWidget {
  const UpdateRequiredPage({
    required this.minimumVersionCode,
    required this.onRetry,
    super.key,
  });

  /// 服务端要求的最低版本号；为 0 表示服务端没说清楚，只提示当前版本过旧。
  final int minimumVersionCode;

  /// 重新检查。朋友装好新版本后不必重启也能继续；也用于服务端临时不可达时重试。
  final Future<void> Function() onRetry;

  /// Web 端刷新页面就是最新版本，其余平台需要重新安装。
  static String actionHintFor(TargetPlatform platform, {required bool isWeb}) =>
      isWeb
      ? '请刷新页面获取最新版本。如果刷新后仍提示更新，请强制刷新一次以清除缓存。'
      : '请安装最新版本的客户端后再进入。';

  static String _describe(int versionCode) {
    if (versionCode <= 0) return '未知';
    final major = versionCode ~/ 1000000;
    final minor = (versionCode ~/ 1000) % 1000;
    final patch = versionCode % 1000;
    return '$major.$minor.$patch';
  }

  @override
  Widget build(BuildContext context) {
    final hint = actionHintFor(defaultTargetPlatform, isWeb: kIsWeb);
    return Scaffold(
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 420),
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const Icon(
                  Icons.system_update_alt,
                  size: 56,
                  color: Color(0xFFF6D986),
                ),
                const SizedBox(height: 16),
                const Text(
                  '需要更新客户端',
                  textAlign: TextAlign.center,
                  style: TextStyle(fontSize: 22, fontWeight: FontWeight.w800),
                ),
                const SizedBox(height: 14),
                _line('当前版本', appVersionName),
                if (minimumVersionCode > 0)
                  _line('需要版本', _describe(minimumVersionCode)),
                const SizedBox(height: 14),
                Text(
                  hint,
                  textAlign: TextAlign.center,
                  style: const TextStyle(color: Colors.white70, height: 1.5),
                ),
                const SizedBox(height: 20),
                FilledButton(
                  key: const ValueKey('update-retry-button'),
                  onPressed: () => onRetry(),
                  child: const Text('我已更新，重新检查'),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _line(String label, String value) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 3),
    child: Row(
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      children: [
        Text(label, style: const TextStyle(color: Colors.white70)),
        Text(
          value,
          style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 16),
        ),
      ],
    ),
  );
}

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/app_version.dart';

/// 版本号写在三处：Dart 常量、pubspec、鸿蒙 app.json5。服务端的版本门禁
/// 按这个数拒绝过旧客户端，任何一处漏改都会让门禁判断错误——所以用测试锁住。
void main() {
  test('Dart 常量、pubspec 与鸿蒙清单的版本号完全一致', () {
    final pubspec = File('pubspec.yaml').readAsStringSync();
    final declared = RegExp(
      r'^version:\s*(\d+\.\d+\.\d+)\+(\d+)\s*$',
      multiLine: true,
    ).firstMatch(pubspec);
    expect(declared, isNotNull, reason: 'pubspec 必须写成 <name>+<code>');
    expect(declared!.group(1), appVersionName);
    expect(int.parse(declared.group(2)!), appVersionCode);

    final harmony = File('ohos/AppScope/app.json5').readAsStringSync();
    final harmonyCode = RegExp(r'"versionCode"\s*:\s*(\d+)').firstMatch(harmony);
    final harmonyName = RegExp(
      r'"versionName"\s*:\s*"([^"]+)"',
    ).firstMatch(harmony);
    expect(harmonyCode, isNotNull);
    expect(harmonyName, isNotNull);
    expect(int.parse(harmonyCode!.group(1)!), appVersionCode);
    expect(harmonyName!.group(1), appVersionName);
  });

  test('版本号按 major*1000000 + minor*1000 + patch 编码', () {
    // 服务端与客户端必须用同一套编码，否则大小比较没有意义
    final parts = appVersionName.split('.').map(int.parse).toList();
    expect(
      appVersionCode,
      parts[0] * 1000000 + parts[1] * 1000 + parts[2],
    );
  });
}

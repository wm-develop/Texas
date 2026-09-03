// 一次改完三处版本号。
//
//   dart tool/set_version.dart 0.3.0
//
// 版本号写在 pubspec.yaml、鸿蒙 AppScope/app.json5 与 lib/core/app_version.dart
// 三处，手改容易漏。漏改会让客户端上报错误的版本号，服务端的版本门禁随之
// 判断错误——`flutter test` 里的一致性测试会拦住，但那已经浪费了一次构建。
//
// 构建各平台客户端之前执行一次即可。改完记得跑 flutter test 确认。

import 'dart:io';

import 'package:poker_client/core/app_version.dart';

/// 三处文件里各自要替换的内容。抽成纯函数是为了能单独测试：
/// 这个脚本一旦写错，错误会一直带到发出去的安装包里。
String updatePubspec(String source, String name, int code) => _replaceOne(
  source,
  RegExp(r'^version:\s*\d+\.\d+\.\d+\+\d+\s*$', multiLine: true),
  'version: $name+$code',
  'pubspec.yaml 的 version',
);

String updateHarmonyManifest(String source, String name, int code) {
  final withCode = _replaceOne(
    source,
    RegExp(r'"versionCode"\s*:\s*\d+'),
    '"versionCode": $code',
    'app.json5 的 versionCode',
  );
  return _replaceOne(
    withCode,
    RegExp(r'"versionName"\s*:\s*"[^"]*"'),
    '"versionName": "$name"',
    'app.json5 的 versionName',
  );
}

String updateDartConstants(String source, String name, int code) {
  final withName = _replaceOne(
    source,
    RegExp(r"const String appVersionName = '[^']*';"),
    "const String appVersionName = '$name';",
    'app_version.dart 的 appVersionName',
  );
  return _replaceOne(
    withName,
    RegExp(r'const int appVersionCode = \d+;'),
    'const int appVersionCode = $code;',
    'app_version.dart 的 appVersionCode',
  );
}

/// 找不到目标就报错而不是静默跳过：静默跳过会让人以为改好了。
String _replaceOne(
  String source,
  RegExp pattern,
  String replacement,
  String what,
) {
  if (!pattern.hasMatch(source)) {
    throw FormatException('没有找到 $what，文件格式可能变了');
  }
  return source.replaceFirst(pattern, replacement);
}

/// 读出当前 pubspec 里的版本号整数，用于拦住版本倒退。
int? currentVersionCode(String pubspec) {
  final match = RegExp(
    r'^version:\s*\d+\.\d+\.\d+\+(\d+)\s*$',
    multiLine: true,
  ).firstMatch(pubspec);
  return match == null ? null : int.tryParse(match.group(1)!);
}

const _files = (
  pubspec: 'pubspec.yaml',
  harmony: 'ohos/AppScope/app.json5',
  dart: 'lib/core/app_version.dart',
);

void main(List<String> arguments) {
  if (arguments.length != 1) {
    stderr.writeln('用法: dart tool/set_version.dart <版本号>   例如 0.3.0');
    exit(64);
  }
  final name = arguments.single.trim();
  final code = encodeVersionName(name);
  if (code == null) {
    stderr.writeln('版本号格式不对：需要 major.minor.patch，且 minor 与 patch 不超过 999');
    exit(64);
  }

  final pubspecFile = File(_files.pubspec);
  if (!pubspecFile.existsSync()) {
    stderr.writeln('请在 apps/poker_client 目录下运行本脚本');
    exit(66);
  }
  final pubspec = pubspecFile.readAsStringSync();

  // versionCode 在 Android 与鸿蒙上都必须单调递增，装不回旧版本。
  final previous = currentVersionCode(pubspec);
  if (previous != null && code <= previous) {
    stderr.writeln(
      '新版本号 $code 不高于当前的 $previous。'
      'Android 与鸿蒙的 versionCode 只能递增，请改用更高的版本号。',
    );
    exit(65);
  }

  final harmonyFile = File(_files.harmony);
  final dartFile = File(_files.dart);
  for (final file in [harmonyFile, dartFile]) {
    if (!file.existsSync()) {
      stderr.writeln('找不到 ${file.path}');
      exit(66);
    }
  }

  // 三处要么一起改成功，要么一处都不动：先在内存里全部算好再落盘。
  final String newPubspec;
  final String newHarmony;
  final String newDart;
  try {
    newPubspec = updatePubspec(pubspec, name, code);
    newHarmony = updateHarmonyManifest(harmonyFile.readAsStringSync(), name, code);
    newDart = updateDartConstants(dartFile.readAsStringSync(), name, code);
  } on FormatException catch (error) {
    stderr.writeln(error.message);
    exit(65);
  }

  pubspecFile.writeAsStringSync(newPubspec);
  harmonyFile.writeAsStringSync(newHarmony);
  dartFile.writeAsStringSync(newDart);

  stdout
    ..writeln('版本号已更新为 $name（$code）：')
    ..writeln('  ${_files.pubspec}')
    ..writeln('  ${_files.harmony}')
    ..writeln('  ${_files.dart}')
    ..writeln('')
    ..writeln('接下来：')
    ..writeln('  1. flutter test    确认三处一致')
    ..writeln('  2. 构建并分发各平台客户端')
    ..writeln('  3. 管理员在「服务器管理 → 最低客户端版本」填入 $code');
}

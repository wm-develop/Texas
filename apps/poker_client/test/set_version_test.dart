import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/app_version.dart';

import '../tool/set_version.dart';

/// 发版脚本一旦写错，错误会一直带到发出去的安装包里，因此逐个函数验证。
void main() {
  group('版本号编码', () {
    test('与解码互逆，1.2.1 这类多段版本也正确', () {
      for (final name in ['0.2.1', '0.3.0', '1.2.1', '1.0.0', '12.34.567']) {
        final code = encodeVersionName(name);
        expect(code, isNotNull, reason: name);
        expect(describeVersionCode(code!), name, reason: name);
      }
      // 明确钉住用户问到的这个
      expect(encodeVersionName('1.2.1'), 1002001);
      expect(describeVersionCode(1002001), '1.2.1');
    });

    test('版本号递增时编码也递增', () {
      expect(
        encodeVersionName('0.3.0')!,
        greaterThan(encodeVersionName('0.2.1')!),
      );
      expect(
        encodeVersionName('1.0.0')!,
        greaterThan(encodeVersionName('0.999.999')!),
      );
    });

    test('格式非法时返回空', () {
      // minor 与 patch 各只有三位容量，超了会串到上一段去
      for (final bad in ['0.2', '0.2.1.3', 'a.b.c', '', '0.1000.0', '0.2.1000']) {
        expect(encodeVersionName(bad), isNull, reason: bad);
      }
    });
  });

  group('改写三处文件', () {
    test('pubspec 只改版本行，注释与其他内容不动', () {
      const source = '''
name: poker_client
# 构建号与鸿蒙 versionCode 用同一套编码
version: 0.2.1+2001
environment:
  sdk: ^3.9.2
''';
      final updated = updatePubspec(source, '0.3.0', 3000);

      expect(updated, contains('version: 0.3.0+3000'));
      expect(updated, isNot(contains('0.2.1+2001')));
      expect(updated, contains('# 构建号与鸿蒙 versionCode 用同一套编码'));
      expect(updated, contains('sdk: ^3.9.2'));
    });

    test('鸿蒙清单两个字段一起改', () {
      const source = '''
{
  "app": {
    // versionName 与 pubspec.yaml 的版本号保持一致。
    "versionCode": 2001,
    "versionName": "0.2.1",
    "icon": "\$media:app_icon"
  }
}
''';
      final updated = updateHarmonyManifest(source, '0.3.0', 3000);

      expect(updated, contains('"versionCode": 3000'));
      expect(updated, contains('"versionName": "0.3.0"'));
      expect(updated, isNot(contains('2001')));
      expect(updated, isNot(contains('0.2.1')));
      expect(updated, contains(r'"icon": "$media:app_icon"'));
    });

    test('Dart 常量两个一起改', () {
      const source = '''
/// 发版时三处一起改。
const String appVersionName = '0.2.1';
const int appVersionCode = 2001;

const String clientVersionHeader = 'X-Client-Version';
''';
      final updated = updateDartConstants(source, '0.3.0', 3000);

      expect(updated, contains("const String appVersionName = '0.3.0';"));
      expect(updated, contains('const int appVersionCode = 3000;'));
      expect(
        updated,
        contains("const String clientVersionHeader = 'X-Client-Version';"),
        reason: '同文件里其他常量不能被改到',
      );
    });

    test('找不到目标时报错，而不是静默跳过', () {
      // 静默跳过会让人以为改好了，把旧版本号带进安装包
      expect(
        () => updatePubspec('name: poker_client\n', '0.3.0', 3000),
        throwsA(isA<FormatException>()),
      );
      expect(
        () => updateHarmonyManifest('{}', '0.3.0', 3000),
        throwsA(isA<FormatException>()),
      );
      expect(
        () => updateDartConstants('const int other = 1;', '0.3.0', 3000),
        throwsA(isA<FormatException>()),
      );
    });
  });

  group('拦住版本倒退', () {
    test('读得出当前构建号', () {
      expect(currentVersionCode('version: 0.2.1+2001\n'), 2001);
      expect(currentVersionCode('name: poker_client\n'), isNull);
    });
  });
}

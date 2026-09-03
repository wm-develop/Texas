import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/app_version.dart';
import 'package:poker_client/features/admin/presentation/minimum_client_version_dialog.dart';

void main() {
  group('版本号编解码', () {
    test('整数还原成可读版本号', () {
      expect(describeVersionCode(2001), '0.2.1');
      expect(describeVersionCode(1002003), '1.2.3');
      expect(describeVersionCode(0), '未限制');
      expect(describeVersionCode(-5), '未限制');
    });

    test('空输入表示不限制，非法输入返回空', () {
      expect(parseVersionCode(''), 0);
      expect(parseVersionCode('  '), 0);
      expect(parseVersionCode('2001'), 2001);
      expect(parseVersionCode(' 2001 '), 2001);
      // 非法输入不能悄悄当成 0，那会把门禁关掉而使用者以为设上了
      expect(parseVersionCode('abc'), isNull);
      expect(parseVersionCode('2.0.1'), isNull);
      expect(parseVersionCode('-1'), isNull);
    });
  });

  group('输入预览', () {
    test('合法数字同时显示原值与还原后的版本号', () {
      // 手填一串数字容易错一位，预览让人一眼看出来
      expect(MinimumClientVersionDialog.previewFor('2001'), '= 2001（0.2.1）');
      expect(
        MinimumClientVersionDialog.previewFor('1002003'),
        '= 1002003（1.2.3）',
      );
    });

    test('空与 0 说明是不限制', () {
      expect(MinimumClientVersionDialog.previewFor(''), '不限制客户端版本');
      expect(MinimumClientVersionDialog.previewFor('0'), '不限制客户端版本');
    });

    test('非法输入明确提示，不冒充成某个版本', () {
      expect(MinimumClientVersionDialog.previewFor('0.2.1'), contains('只能填数字'));
      expect(MinimumClientVersionDialog.previewFor('abc'), contains('只能填数字'));
    });
  });

  group('弹窗交互', () {
    Future<int?> openDialog(WidgetTester tester, {required int current}) async {
      int? result;
      var opened = false;
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: Center(
                child: FilledButton(
                  onPressed: () async {
                    opened = true;
                    result = await MinimumClientVersionDialog.show(
                      context,
                      current: current,
                    );
                  },
                  child: const Text('打开'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开'));
      await tester.pumpAndSettle();
      expect(opened, isTrue);
      return result;
    }

    testWidgets('输入时预览实时跟着变', (tester) async {
      await openDialog(tester, current: 2001);
      // 打开时带上当前值
      expect(find.text('= 2001（0.2.1）'), findsOneWidget);

      await tester.enterText(
        find.byKey(const ValueKey('minimum-client-version-field')),
        '2002',
      );
      await tester.pump();
      expect(find.text('= 2002（0.2.2）'), findsOneWidget);
      expect(find.text('= 2001（0.2.1）'), findsNothing);
    });

    testWidgets('非法输入时保存按钮不可用', (tester) async {
      // 放行会被当成 0，等于悄悄把门禁关掉
      await openDialog(tester, current: 2001);
      await tester.enterText(
        find.byKey(const ValueKey('minimum-client-version-field')),
        '0.2.2',
      );
      await tester.pump();

      final save = tester.widget<FilledButton>(
        find.widgetWithText(FilledButton, '保存'),
      );
      expect(save.onPressed, isNull);
      expect(find.textContaining('只能填数字'), findsOneWidget);
    });

    testWidgets('保存返回解析后的整数，取消返回空', (tester) async {
      await tester.runAsync(() async {});
      int? saved;
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: Center(
                child: FilledButton(
                  onPressed: () async => saved =
                      await MinimumClientVersionDialog.show(context, current: 0),
                  child: const Text('打开'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const ValueKey('minimum-client-version-field')),
        '3001',
      );
      await tester.pump();
      await tester.tap(find.widgetWithText(FilledButton, '保存'));
      await tester.pumpAndSettle();
      expect(saved, 3001);

      // 取消不改动任何东西
      saved = null;
      await tester.tap(find.text('打开'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(TextButton, '取消'));
      await tester.pumpAndSettle();
      expect(saved, isNull);
    });

    testWidgets('留空即为不限制，保存返回 0', (tester) async {
      int? saved;
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: Center(
                child: FilledButton(
                  onPressed: () async => saved =
                      await MinimumClientVersionDialog.show(
                        context,
                        current: 2001,
                      ),
                  child: const Text('打开'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const ValueKey('minimum-client-version-field')),
        '',
      );
      await tester.pump();
      expect(find.text('不限制客户端版本'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, '保存'));
      await tester.pumpAndSettle();
      expect(saved, 0);
    });
  });
}

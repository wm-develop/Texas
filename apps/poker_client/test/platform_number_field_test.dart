import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';

void main() {
  testWidgets('OHOS keeps an empty numeric field label above its hint', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.ohos;
    final controller = TextEditingController();
    addTearDown(controller.dispose);
    try {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: PlatformNumberField(
              controller: controller,
              decoration: const InputDecoration(
                labelText: '6 位房间码',
                border: OutlineInputBorder(),
              ),
            ),
          ),
        ),
      );

      final decorator = tester.widget<InputDecorator>(
        find.byType(InputDecorator),
      );
      expect(
        decorator.decoration.floatingLabelBehavior,
        FloatingLabelBehavior.always,
      );
      expect(decorator.decoration.hintText, '点击输入');
      expect(find.text('6 位房间码'), findsOneWidget);
      expect(find.text('点击输入'), findsOneWidget);
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });

  testWidgets('OHOS compact keypad keeps every digit and confirm visible', (
    tester,
  ) async {
    debugDefaultTargetPlatformOverride = TargetPlatform.ohos;
    tester.view.physicalSize = const Size(800, 360);
    tester.view.devicePixelRatio = 1;
    final controller = TextEditingController();
    addTearDown(controller.dispose);
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    try {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Center(
              child: SizedBox(
                width: 280,
                child: PlatformNumberField(
                  controller: controller,
                  decoration: const InputDecoration(
                    labelText: '小盲',
                    border: OutlineInputBorder(),
                  ),
                ),
              ),
            ),
          ),
        ),
      );

      await tester.tap(find.text('点击输入'));
      await tester.pumpAndSettle();

      final screen = Offset.zero & const Size(800, 360);
      for (var digit = 0; digit <= 9; digit++) {
        final key = find.byKey(ValueKey('ohos-number-key-$digit'));
        expect(key, findsOneWidget);
        expect(key.hitTestable(), findsOneWidget);
        expect(screen.contains(tester.getCenter(key)), isTrue);
      }
      final confirm = find.byKey(const ValueKey('ohos-number-confirm'));
      expect(tester.widget<FilledButton>(confirm).onPressed, isNull);

      await tester.tap(find.byKey(const ValueKey('ohos-number-key-9')));
      await tester.tap(find.byKey(const ValueKey('ohos-number-key-0')));
      await tester.pump();
      expect(find.text('90'), findsOneWidget);
      expect(tester.widget<FilledButton>(confirm).onPressed, isNotNull);
      await tester.tap(confirm);
      await tester.pumpAndSettle();
      expect(controller.text, '90');
    } finally {
      debugDefaultTargetPlatformOverride = null;
    }
  });
}

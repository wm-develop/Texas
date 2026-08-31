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
}

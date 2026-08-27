import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/presentation/responsive_action_strip.dart';

void main() {
  testWidgets('pins primary and custom actions on a narrow table', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Center(
            child: SizedBox(
              key: const ValueKey('strip-bounds'),
              width: 650,
              height: 70,
              child: ResponsiveActionStrip(
                leadingActions: const [
                  SizedBox(
                    key: ValueKey('fold'),
                    width: 80,
                    child: TextButton(onPressed: null, child: Text('弃牌')),
                  ),
                  SizedBox(
                    key: ValueKey('call'),
                    width: 100,
                    child: TextButton(onPressed: null, child: Text('跟注')),
                  ),
                ],
                presetActions: [
                  for (var index = 0; index < 7; index++)
                    SizedBox(
                      key: ValueKey('preset-$index'),
                      width: 115,
                      child: Text('档位 $index'),
                    ),
                ],
                trailingAction: const SizedBox(
                  key: ValueKey('custom'),
                  width: 110,
                  child: TextButton(onPressed: null, child: Text('自定义')),
                ),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.pump();

    final bounds = tester.getRect(find.byKey(const ValueKey('strip-bounds')));
    final custom = tester.getRect(find.byKey(const ValueKey('custom')));
    expect(custom.left, greaterThanOrEqualTo(bounds.left));
    expect(custom.right, lessThanOrEqualTo(bounds.right));
    expect(find.byKey(const ValueKey('fold')).hitTestable(), findsOneWidget);
    expect(find.byKey(const ValueKey('call')).hitTestable(), findsOneWidget);
    expect(find.byKey(const ValueKey('custom')).hitTestable(), findsOneWidget);
    expect(
      find.byKey(const ValueKey('bet-presets-scroll-right')),
      findsOneWidget,
    );
  });

  testWidgets('keeps the centered single row on a wide table', (tester) async {
    tester.view.physicalSize = const Size(1500, 300);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SizedBox(
            width: 1400,
            height: 70,
            child: ResponsiveActionStrip(
              leadingActions: const [SizedBox(width: 180, child: Text('主要'))],
              presetActions: [
                for (var index = 0; index < 7; index++)
                  SizedBox(
                    key: ValueKey('wide-preset-$index'),
                    width: 110,
                    child: Text('档位 $index'),
                  ),
              ],
              trailingAction: const SizedBox(
                key: ValueKey('wide-custom'),
                width: 110,
                child: Text('自定义'),
              ),
            ),
          ),
        ),
      ),
    );

    expect(
      find.byKey(const ValueKey('bet-presets-scroll-right')),
      findsNothing,
    );
    expect(
      find.byKey(const ValueKey('wide-custom')).hitTestable(),
      findsOneWidget,
    );
    for (var index = 0; index < 7; index++) {
      expect(
        find.byKey(ValueKey('wide-preset-$index')).hitTestable(),
        findsOneWidget,
      );
    }
    expect(tester.takeException(), isNull);
  });
}

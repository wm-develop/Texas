import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/widgets/dialog_scroll_area.dart';

void main() {
  group('弹窗内容区高度按屏幕真实可用空间算', () {
    test('鸿蒙手机横屏（约 360 高）：内容区不到 150，而不是 70% 的 252', () {
      // 真机复现：按 70% 算出 252，弹窗里实际只能看见约 150，滚动范围按 252
      // 算，最后一项永远落在被裁掉的那一截里
      const media = MediaQueryData(size: Size(780, 360));
      final height = DialogScrollArea.maxHeightFor(media, 168);
      expect(height, 144);
      expect(height, lessThan(150));
    });

    test('系统避让区和键盘都要扣掉', () {
      const media = MediaQueryData(
        size: Size(780, 360),
        viewPadding: EdgeInsets.only(top: 24, bottom: 20),
        viewInsets: EdgeInsets.only(bottom: 100),
      );
      expect(DialogScrollArea.maxHeightFor(media, 168), 96, reason: '压到下限');
    });

    test('大屏给足空间', () {
      const media = MediaQueryData(size: Size(1280, 800));
      expect(DialogScrollArea.maxHeightFor(media, 168), 584);
    });

    testWidgets('内容区的约束就是算出来的高度，不多不少', (tester) async {
      tester.view.physicalSize = const Size(780, 360);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: Center(
              child: DialogScrollArea(
                child: SizedBox(height: 1000, width: 200),
              ),
            ),
          ),
        ),
      );
      expect(tester.getSize(find.byType(DialogScrollArea)).height, 144);
      expect(tester.takeException(), isNull);
    });
  });
}

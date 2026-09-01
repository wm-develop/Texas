import 'dart:ui';

import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/presentation/table_viewport_layout.dart';

void main() {
  test('preserves the existing 16:9 desktop composition', () {
    final layout = TableViewportLayout.fromSize(
      const Size(1280, 720),
      chatVisible: true,
    );

    expect(layout.canvasSize, const Size(1280, 720));
    expect(layout.supportsSideChat, isTrue);
    expect(layout.seatVerticalRadius, 0.88);
    expect(layout.seatHorizontalRadius, 0.94);
    expect(layout.tableRect, const Rect.fromLTWH(104, 62, 912, 526));
  });

  test('fills a wide phone while limiting poker table stretching', () {
    final layout = TableViewportLayout.fromSize(
      const Size(2400, 1080),
      chatVisible: true,
    );

    expect(
      layout.canvasSize.aspectRatio,
      closeTo(const Size(2400, 1080).aspectRatio, 0.001),
    );
    expect(layout.tableRect.width, TableViewportLayout.maxTableWidth);
    expect(layout.tableRect.height, 526);
  });

  test('enlarges content on a logical-size landscape phone', () {
    final layout = TableViewportLayout.fromSize(
      const Size(920, 420),
      chatVisible: true,
    );

    expect(layout.isCompactLandscape, isTrue);
    expect(layout.seatVerticalRadius, 0.80);
    expect(layout.seatHorizontalRadius, 0.76);
    expect(layout.canvasSize.height, TableViewportLayout.compactDesignHeight);
    expect(layout.supportsSideChat, isFalse);
    expect(
      layout.tableRect.width,
      closeTo(
        layout.canvasSize.width -
            TableViewportLayout.compactLeftRailWidth -
            TableViewportLayout.compactRightRailWidth,
        0.001,
      ),
    );
    expect(layout.tableRect.height, 532);
    expect(layout.tableRect.top, 8);
    expect(layout.boardRect.top, layout.tableRect.top + 128);
    expect(layout.boardRect.bottom, layout.tableRect.bottom - 128);
    expect(layout.boardRect.width, greaterThan(500));

    // Enlarged 216x116 player cards remain outside the board's protected
    // center region, including the top and local-player bottom positions.
    final topSeatCenter =
        layout.tableRect.center.dy -
        layout.tableRect.height * layout.seatVerticalRadius / 2;
    final bottomSeatCenter =
        layout.tableRect.center.dy +
        layout.tableRect.height * layout.seatVerticalRadius / 2;
    expect(topSeatCenter + 58, lessThan(layout.boardRect.top));
    expect(bottomSeatCenter - 58, greaterThan(layout.boardRect.bottom));

    // Side seats stay clear of the compact information rail.
    final leftSeatCenter =
        layout.tableRect.center.dx -
        layout.tableRect.width * layout.seatHorizontalRadius / 2;
    expect(leftSeatCenter - 108, greaterThan(layout.tableRect.left));

    for (var playerCount = 2; playerCount <= 10; playerCount++) {
      for (var index = 0; index < playerCount; index++) {
        final alignment = layout.seatAlignment(index, playerCount);
        final center = Offset(
          layout.tableRect.center.dx + alignment.x * layout.tableRect.width / 2,
          layout.tableRect.center.dy +
              alignment.y * layout.tableRect.height / 2,
        );
        final seatRect = Rect.fromCenter(
          center: center,
          width: 216,
          height: 116,
        );
        expect(
          seatRect.overlaps(layout.boardRect),
          isFalse,
          reason: '$playerCount 人牌桌的第 ${index + 1} 个座位遮挡公共牌区',
        );
      }
    }
  });

  test('centers the table when the side chat is closed', () {
    final layout = TableViewportLayout.fromSize(
      const Size(1600, 720),
      chatVisible: false,
    );

    expect(layout.tableRect.width, TableViewportLayout.maxTableWidth);
    expect(
      layout.tableRect.left,
      closeTo(layout.canvasSize.width - layout.tableRect.right, 0.001),
    );
  });

  test('clamps extreme portrait and ultrawide windows', () {
    final portrait = TableViewportLayout.fromSize(
      const Size(600, 1200),
      chatVisible: true,
    );
    final ultrawide = TableViewportLayout.fromSize(
      const Size(3200, 900),
      chatVisible: true,
    );

    expect(
      portrait.canvasSize.aspectRatio,
      TableViewportLayout.minCanvasAspect,
    );
    expect(
      ultrawide.canvasSize.aspectRatio,
      TableViewportLayout.maxCanvasAspect,
    );
    expect(portrait.supportsSideChat, isFalse);
  });

  test('compactOverride 固定布局族，键盘压缩高度不会切换布局', () {
    // 平板横屏被键盘压缩到 520 以下时，高度启发式会误判为紧凑布局；
    // 设备级 override 必须保持普通布局不变。
    final tabletWithKeyboard = TableViewportLayout.fromSize(
      const Size(1280, 480),
      chatVisible: true,
      compactOverride: false,
    );
    expect(tabletWithKeyboard.isCompactLandscape, isFalse);

    // 手机即使窗口高度暂时变大，也保持紧凑布局。
    final phoneOverride = TableViewportLayout.fromSize(
      const Size(920, 700),
      chatVisible: true,
      compactOverride: true,
    );
    expect(phoneOverride.isCompactLandscape, isTrue);

    // 不传 override 时保留原有高度启发式。
    final heuristic = TableViewportLayout.fromSize(
      const Size(920, 420),
      chatVisible: true,
    );
    expect(heuristic.isCompactLandscape, isTrue);
  });
}

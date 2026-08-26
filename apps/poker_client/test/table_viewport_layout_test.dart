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
    expect(layout.canvasSize.height, TableViewportLayout.compactDesignHeight);
    expect(layout.supportsSideChat, isFalse);
    expect(layout.tableRect.width, TableViewportLayout.compactMaxTableWidth);
    expect(layout.tableRect.height, 466);
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
}

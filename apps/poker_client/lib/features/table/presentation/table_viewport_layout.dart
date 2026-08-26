import 'dart:math' as math;

import 'package:flutter/widgets.dart';

/// Maps arbitrary window sizes onto the table's 720-unit design coordinate
/// system without stretching the poker table to the full viewport ratio.
class TableViewportLayout {
  const TableViewportLayout._({
    required this.canvasSize,
    required this.tableRect,
    required this.supportsSideChat,
  });

  static const double designHeight = 720;
  static const double minCanvasAspect = 1.45;
  static const double maxCanvasAspect = 2.35;
  static const double maxTableWidth = 1040;

  final Size canvasSize;
  final Rect tableRect;
  final bool supportsSideChat;

  factory TableViewportLayout.fromSize(
    Size availableSize, {
    required bool chatVisible,
  }) {
    final safeWidth = math.max(1.0, availableSize.width);
    final safeHeight = math.max(1.0, availableSize.height);
    final viewportAspect = safeWidth / safeHeight;
    final canvasAspect = viewportAspect.clamp(minCanvasAspect, maxCanvasAspect);
    final canvasSize = Size(designHeight * canvasAspect, designHeight);
    final supportsSideChat = canvasSize.width >= 1180;
    final reserveChat = chatVisible && supportsSideChat;
    final baseLeft = canvasSize.width < 1160 ? 64.0 : 104.0;
    final baseRight = reserveChat ? 264.0 : baseLeft;
    const top = 62.0;
    const bottom = 132.0;
    const tableHeight = designHeight - top - bottom;
    final availableTableWidth = math.max(
      1.0,
      canvasSize.width - baseLeft - baseRight,
    );
    final tableWidth = math.min(availableTableWidth, maxTableWidth);
    final unusedWidth = availableTableWidth - tableWidth;
    final tableRect = Rect.fromLTWH(
      baseLeft + unusedWidth / 2,
      top,
      tableWidth,
      tableHeight,
    );

    return TableViewportLayout._(
      canvasSize: canvasSize,
      tableRect: tableRect,
      supportsSideChat: supportsSideChat,
    );
  }
}

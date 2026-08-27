import 'dart:math' as math;

import 'package:flutter/widgets.dart';

/// Maps arbitrary window sizes onto the table's 720-unit design coordinate
/// system without stretching the poker table to the full viewport ratio.
class TableViewportLayout {
  const TableViewportLayout._({
    required this.canvasSize,
    required this.tableRect,
    required this.supportsSideChat,
    required this.isCompactLandscape,
  });

  static const double designHeight = 720;
  static const double compactDesignHeight = 620;
  static const double minCanvasAspect = 1.45;
  static const double maxCanvasAspect = 2.35;
  static const double maxTableWidth = 1040;
  static const double compactMaxTableWidth = 1160;

  final Size canvasSize;
  final Rect tableRect;
  final bool supportsSideChat;
  final bool isCompactLandscape;

  /// Pulls the top and bottom seats closer to the felt on short landscape
  /// screens, leaving a dedicated gap for the local hand panel.
  double get seatVerticalRadius => isCompactLandscape ? 0.70 : 0.88;

  factory TableViewportLayout.fromSize(
    Size availableSize, {
    required bool chatVisible,
  }) {
    final safeWidth = math.max(1.0, availableSize.width);
    final safeHeight = math.max(1.0, availableSize.height);
    final viewportAspect = safeWidth / safeHeight;
    final canvasAspect = viewportAspect.clamp(minCanvasAspect, maxCanvasAspect);
    final isCompactLandscape = safeHeight <= 520 && viewportAspect >= 1.7;
    final canvasHeight = isCompactLandscape
        ? compactDesignHeight
        : designHeight;
    final canvasSize = Size(canvasHeight * canvasAspect, canvasHeight);
    final supportsSideChat = !isCompactLandscape && canvasSize.width >= 1180;
    final reserveChat = chatVisible && supportsSideChat;
    final baseLeft = isCompactLandscape
        ? 42.0
        : canvasSize.width < 1160
        ? 64.0
        : 104.0;
    final baseRight = reserveChat ? 264.0 : baseLeft;
    final top = isCompactLandscape ? 48.0 : 62.0;
    final bottom = isCompactLandscape ? 106.0 : 132.0;
    final tableHeight = canvasHeight - top - bottom;
    final availableTableWidth = math.max(
      1.0,
      canvasSize.width - baseLeft - baseRight,
    );
    final tableWidth = math.min(
      availableTableWidth,
      isCompactLandscape ? compactMaxTableWidth : maxTableWidth,
    );
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
      isCompactLandscape: isCompactLandscape,
    );
  }
}

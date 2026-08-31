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
  static const double compactLeftRailWidth = 168;
  static const double compactRightRailWidth = 148;

  final Size canvasSize;
  final Rect tableRect;
  final bool supportsSideChat;
  final bool isCompactLandscape;

  /// Keeps the top and bottom seats close to the outer table edge. Compact
  /// layouts no longer reserve a separate local-hand panel below the table.
  double get seatVerticalRadius => isCompactLandscape ? 0.80 : 0.88;
  double get seatHorizontalRadius => isCompactLandscape ? 0.76 : 0.94;

  /// Places compact seats around a rounded-rectangle perimeter instead of an
  /// ellipse. This keeps every enlarged player card outside [boardRect] for
  /// all supported table sizes (2-10 players).
  Alignment seatAlignment(int relativeIndex, int seatCount) {
    final angle = math.pi / 2 + (math.pi * 2 * relativeIndex / seatCount);
    final x = math.cos(angle);
    final y = math.sin(angle);
    if (!isCompactLandscape) {
      return Alignment(x * seatHorizontalRadius, y * seatVerticalRadius);
    }

    const topRowHorizontalRadius = 0.46;
    const sideColumnVerticalRadius = 0.48;
    if (y.abs() >= x.abs()) {
      return Alignment(
        y == 0 ? 0 : x / y.abs() * topRowHorizontalRadius,
        y.isNegative ? -seatVerticalRadius : seatVerticalRadius,
      );
    }
    return Alignment(
      x.isNegative ? -seatHorizontalRadius : seatHorizontalRadius,
      x == 0 ? 0 : y / x.abs() * sideColumnVerticalRadius,
    );
  }

  /// The board owns this top-most center region. It is inset far enough that
  /// even enlarged top/bottom seat cards do not intersect it.
  Rect get boardRect => Rect.fromLTRB(
    tableRect.left + 240,
    tableRect.top + 128,
    tableRect.right - 240,
    tableRect.bottom - 128,
  );

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
        ? compactLeftRailWidth
        : canvasSize.width < 1160
        ? 64.0
        : 104.0;
    final baseRight = isCompactLandscape
        ? compactRightRailWidth
        : reserveChat
        ? 264.0
        : baseLeft;
    final top = isCompactLandscape ? 8.0 : 62.0;
    final bottom = isCompactLandscape ? 80.0 : 132.0;
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

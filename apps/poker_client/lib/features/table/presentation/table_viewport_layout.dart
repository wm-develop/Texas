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
  /// 右栏承载竖排下注区，因此比左栏宽；牌桌下方相应只留很窄的一条。
  static const double compactRightRailWidth = 216;

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

  /// [compactOverride] pins the phone/tablet layout family regardless of the
  /// momentary window height. Mobile platforms pass a device-derived value so
  /// a keyboard-shrunk viewport can never flip the layout mid-input; resizable
  /// desktop/web windows keep the height-based heuristic by passing null.
  factory TableViewportLayout.fromSize(
    Size availableSize, {
    required bool chatVisible,
    bool? compactOverride,
  }) {
    final safeWidth = math.max(1.0, availableSize.width);
    final safeHeight = math.max(1.0, availableSize.height);
    final viewportAspect = safeWidth / safeHeight;
    final canvasAspect = viewportAspect.clamp(minCanvasAspect, maxCanvasAspect);
    // 宽高比不能用来判断设备类别：1400x500 的桌面窗口比任何手机都更宽，
    // 但它显然不该用手机布局。短边才是与方向无关的判据，也与 Material 的
    // 600 断点一致；桌面窗口被拖到很小时切紧凑布局同样是想要的结果。
    final isCompactLandscape =
        compactOverride ?? (math.min(safeWidth, safeHeight) < 600);
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
    // 紧凑布局的动作区移到右栏后，底部只需留出本轮下注筹码的呼吸空间。
    final bottom = isCompactLandscape ? 24.0 : 132.0;
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

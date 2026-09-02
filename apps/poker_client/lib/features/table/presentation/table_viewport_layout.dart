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
  /// 大屏的下注区与手机同构：右侧竖排。左侧留给聊天，牌桌上下因此可以做满。
  /// 内容宽度与手机右栏一致（248-32 与 216-16 都是 200），两端手感相同。
  static const double betRailWidth = 232;
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
    // 右栏恒定占用后，只有画布足够宽才容得下「聊天 + 牌桌 + 下注区」三者：
    // 牌桌至少要能放下 5 张公共牌（boardRect 左右各内缩 240，故需 810），
    // 加上左右两栏即 810 + 264 + 232。达不到时聊天改为弹窗，与手机一致。
    final supportsSideChat = !isCompactLandscape && canvasSize.width >= 1320;
    final reserveChat = chatVisible && supportsSideChat;
    // 两种布局族都是「左信息、右操作」：右栏恒为下注区，左栏在大屏上留给
    // 聊天面板；聊天收起时左右对称，牌桌保持居中。
    final baseRight = isCompactLandscape ? compactRightRailWidth : betRailWidth;
    // 左栏只在聊天停靠时才需要宽预留；否则保持窄边距，把宽度让给牌桌，
    // 牌桌随后在「左边距到右栏」之间居中，视觉上仍然平衡。
    final baseLeft = isCompactLandscape
        ? compactLeftRailWidth
        : reserveChat
        ? 264.0
        : canvasSize.width < 1160
        ? 64.0
        : 104.0;
    final top = isCompactLandscape ? 8.0 : 62.0;
    // 紧凑布局的动作区移到右栏后，底部只需留出本轮下注筹码的呼吸空间。
    // 下注区移出底部后，两种布局都只需为本轮下注筹码留出呼吸空间。
    final bottom = isCompactLandscape ? 24.0 : 44.0;
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

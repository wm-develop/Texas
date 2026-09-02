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
    required this.seatVerticalAlignment,
    required this.seatHorizontalAlignment,
    required this.boardHorizontalInset,
    required this.boardVerticalInset,
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
  final double seatVerticalAlignment;
  final double seatHorizontalAlignment;
  final double boardHorizontalInset;
  final double boardVerticalInset;

  /// 玩家框尺寸。与 [TableSeatCard] 保持一致；布局据此推算落位与公共牌区内缩。
  static const Size seatCardSize = Size(216, 116);

  /// 玩家框落在牌桌内的比例，其余落在桌外空白区。
  ///
  /// 取 1/3 是为了把桌面让给本轮下注筹码和公共牌：玩家框原本整个压在桌内，
  /// 桌内被占的面积远大于桌外空白，中间反而局促。
  static const double seatInsideFraction = 1 / 3;

  /// 玩家框需要伸出牌桌边缘的长度。
  static double get _verticalOverhang =>
      seatCardSize.height * (1 - seatInsideFraction);

  static double get _horizontalOverhang =>
      seatCardSize.width * (1 - seatInsideFraction);

  /// 把「期望伸出多少」换算成 [Align] 需要的 alignment。
  ///
  /// [Align] 的语义不是「圆心落在半径处」，而是把子件放进
  /// `extent - card` 的空隙里：`childStart = (1 + a) / 2 * slack`。
  /// 两者差一个 `card`，直接按半径写常量会让框比预期更靠近中心。
  static double _edgeAlignment(double extent, double card, double overhang) {
    final slack = extent - card;
    if (slack <= 0) return 1;
    return (extent + overhang - card) / slack * 2 - 1;
  }

  /// 座位在牌桌坐标系中的实际矩形。
  ///
  /// 布局与测试共用这一处换算：此前测试自己按「圆心 × 半宽」另算了一份，
  /// 与 [Align] 的真实语义不符，导致侧边座位实际压住公共牌区却仍然通过。
  Rect seatRect(int relativeIndex, int seatCount) {
    final alignment = seatAlignment(relativeIndex, seatCount);
    final horizontalSlack = tableRect.width - seatCardSize.width;
    final verticalSlack = tableRect.height - seatCardSize.height;
    return Rect.fromLTWH(
      tableRect.left + (1 + alignment.x) / 2 * horizontalSlack,
      tableRect.top + (1 + alignment.y) / 2 * verticalSlack,
      seatCardSize.width,
      seatCardSize.height,
    );
  }

  /// 座位沿牌桌的圆角矩形周边分布，而不是沿椭圆分布。
  ///
  /// 椭圆分布会把斜角座位（例如 3 人桌的第 2 个座位）放在靠近中心的位置，
  /// 从而压住公共牌区；周边分布把它们推到最近的一条边上。两种布局族共用
  /// 这一套映射，桌面端此前用椭圆，正是它产生了遮挡。
  Alignment seatAlignment(int relativeIndex, int seatCount) {
    final angle = math.pi / 2 + (math.pi * 2 * relativeIndex / seatCount);
    final x = math.cos(angle);
    final y = math.sin(angle);
    final topRowHorizontalReach = seatHorizontalAlignment * 0.6;
    final sideColumnVerticalReach = seatVerticalAlignment * 0.6;
    if (y.abs() >= x.abs()) {
      return Alignment(
        y == 0 ? 0 : x / y.abs() * topRowHorizontalReach,
        y.isNegative ? -seatVerticalAlignment : seatVerticalAlignment,
      );
    }
    return Alignment(
      x.isNegative ? -seatHorizontalAlignment : seatHorizontalAlignment,
      x == 0 ? 0 : y / x.abs() * sideColumnVerticalReach,
    );
  }

  /// 公共牌与底池独占的中央区域。内缩量由玩家框留在桌内的部分推算，
  /// 玩家框越靠外，这块区域越大。
  Rect get boardRect => Rect.fromLTRB(
    tableRect.left + boardHorizontalInset,
    tableRect.top + boardVerticalInset,
    tableRect.right - boardHorizontalInset,
    tableRect.bottom - boardVerticalInset,
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
    // 上下各让出玩家框伸到桌外的那一段，让它真正落在空白区而不是压在桌面上。
    final verticalMargin = _verticalOverhang + 8;
    final top = verticalMargin;
    final bottom = verticalMargin;
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

    // 横向能伸出多少取决于牌桌两侧的空余：左右栏紧贴牌桌时不能再外扩，
    // 否则玩家框会压住聊天面板或下注区。没有空余时取 alignment 1，
    // 也就是紧贴桌沿的内侧，这仍比原先的 0.76/0.94 更靠外。
    final horizontalOverhang = math.min(
      _horizontalOverhang,
      math.max(0.0, unusedWidth / 2 - 8),
    );
    final seatHorizontalAlignment = _edgeAlignment(
      tableWidth,
      seatCardSize.width,
      horizontalOverhang,
    );
    final seatVerticalAlignment = _edgeAlignment(
      tableHeight,
      seatCardSize.height,
      _verticalOverhang,
    );

    return TableViewportLayout._(
      canvasSize: canvasSize,
      tableRect: tableRect,
      supportsSideChat: supportsSideChat,
      isCompactLandscape: isCompactLandscape,
      seatVerticalAlignment: seatVerticalAlignment,
      seatHorizontalAlignment: seatHorizontalAlignment,
      // 公共牌区退到玩家框留在桌内的那部分之外，再留一点呼吸空间。
      boardHorizontalInset: seatCardSize.width - horizontalOverhang + 24,
      boardVerticalInset:
          seatCardSize.height - _verticalOverhang + 24,
    );
  }
}

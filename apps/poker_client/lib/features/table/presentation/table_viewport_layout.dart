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
    required this.seatHorizontalOverhang,
    required this.seatVerticalOverhang,
  });

  static const double designHeight = 720;
  static const double compactDesignHeight = 620;
  static const double minCanvasAspect = 1.45;
  static const double maxCanvasAspect = 2.35;
  /// 大屏牌桌的最大宽高比。
  ///
  /// 此前用固定像素上限 1040，平板与宽窗口上可用宽度有 1300 以上，牌桌却
  /// 被卡在 1040，左侧白白空出一大片。改成按比例：牌桌先把可用宽度吃满，
  /// 只有在超宽画布上才被这个比例挡住，免得椭圆拉得太长、对家离得过远。
  /// 1.95 接近真实九人桌的长宽比。
  static const double maxTableAspect = 1.95;
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
  final double seatHorizontalOverhang;
  final double seatVerticalOverhang;

  /// 玩家框尺寸。与 [TableSeatCard] 保持一致；布局据此推算落位与公共牌区内缩。
  static const Size seatCardSize = Size(216, 116);

  /// 可见桌面（毛毡）相对 [tableRect] 的内缩。TableCanvas 按同一组数值绘制，
  /// 座位落位与公共牌区都以 [feltRect] 为基准——玩家看到的桌沿是它，不是
  /// [tableRect]。这两者相差 54/70，早先按 tableRect 计算落位，结果玩家框
  /// 整个落在桌外还差 15 像素才碰到桌沿。
  static const EdgeInsets feltInsets = EdgeInsets.symmetric(
    horizontal: 70,
    vertical: 54,
  );

  /// 玩家看到的桌面矩形。
  Rect get feltRect => feltInsets.deflateRect(tableRect);

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

  /// 座位中心所在的矩形。
  ///
  /// 由玩家框伸出桌沿的长度反推：框中心到桌沿的偏移 = 外露长度 - 框宽的一半。
  /// 完全不外扩时偏移为负，中心落在桌内半个框宽处。
  Rect get seatCenterRect {
    final offsetX = seatHorizontalOverhang - seatCardSize.width / 2;
    final offsetY = seatVerticalOverhang - seatCardSize.height / 2;
    return Rect.fromLTRB(
      feltRect.left - offsetX,
      feltRect.top - offsetY,
      feltRect.right + offsetX,
      feltRect.bottom + offsetY,
    );
  }

  /// 第 [relativeIndex] 个座位的中心，沿 [seatCenterRect] 的周长**等距**分布。
  ///
  /// 等距是必要的：按角度把座位投影到最近一条边会让间距忽宽忽窄，
  /// 10 人桌底边曾因此挤下三个玩家框而直接重叠。
  ///
  /// 起点是底边中点（本人座位），沿屏幕向左推进。
  Offset seatCenter(int relativeIndex, int seatCount) {
    final rect = seatCenterRect;
    final half = rect.width / 2;
    final perimeter = 2 * (rect.width + rect.height);
    var distance = perimeter * relativeIndex / seatCount;

    if (distance <= half) {
      return Offset(rect.center.dx - distance, rect.bottom);
    }
    distance -= half;
    if (distance <= rect.height) {
      return Offset(rect.left, rect.bottom - distance);
    }
    distance -= rect.height;
    if (distance <= rect.width) {
      return Offset(rect.left + distance, rect.top);
    }
    distance -= rect.width;
    if (distance <= rect.height) {
      return Offset(rect.right, rect.top + distance);
    }
    distance -= rect.height;
    return Offset(rect.right - distance, rect.bottom);
  }

  /// 把座位中心换算成 [Align] 需要的 alignment。
  Alignment seatAlignment(int relativeIndex, int seatCount) {
    final center = seatCenter(relativeIndex, seatCount);
    final horizontalSlack = tableRect.width - seatCardSize.width;
    final verticalSlack = tableRect.height - seatCardSize.height;
    final left = center.dx - seatCardSize.width / 2;
    final top = center.dy - seatCardSize.height / 2;
    return Alignment(
      horizontalSlack <= 0
          ? 0
          : (left - tableRect.left) / horizontalSlack * 2 - 1,
      verticalSlack <= 0 ? 0 : (top - tableRect.top) / verticalSlack * 2 - 1,
    );
  }

  /// 公共牌与底池独占的中央区域。内缩量由玩家框留在桌内的部分推算，
  /// 玩家框越靠外，这块区域越大。
  Rect get boardRect => Rect.fromLTRB(
    feltRect.left + boardHorizontalInset,
    feltRect.top + boardVerticalInset,
    feltRect.right - boardHorizontalInset,
    feltRect.bottom - boardVerticalInset,
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
    // 右栏恒定占用后，只有画布足够宽才容得下「聊天 + 牌桌 + 下注区」三者。
    // 门槛由最苛刻的情况决定：10 人桌要把 216 宽的玩家框沿桌沿排开而互不
    // 重叠，牌桌不能太窄。1320 时 9 人桌就会在拐角处叠住，因此取 1560；
    // 达不到时聊天改为弹窗，与手机一致。
    final supportsSideChat = !isCompactLandscape && canvasSize.width >= 1560;
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
    // 上下各让出玩家框伸到桌外的那一段。桌沿本身已经比 tableRect 内缩了
    // feltInsets.top，因此这里只需补足差额。
    final verticalMargin = math.max(
      8.0,
      _verticalOverhang - feltInsets.top + 8,
    );
    final top = verticalMargin;
    final bottom = verticalMargin;
    final tableHeight = canvasHeight - top - bottom;
    final availableTableWidth = math.max(
      1.0,
      canvasSize.width - baseLeft - baseRight,
    );
    final tableWidth = math.min(
      availableTableWidth,
      isCompactLandscape
          ? compactMaxTableWidth
          : tableHeight * maxTableAspect,
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
    // 横向外扩的真正约束是左右两栏的内缘，不是 tableRect 的边界：
    // 左栏放信息或聊天面板，右栏放下注区，玩家框不能压到它们上面。
    final feltLeft = tableRect.left + feltInsets.left;
    final feltRight = tableRect.right - feltInsets.right;
    final leftLimit = isCompactLandscape
        ? compactLeftRailWidth
        : reserveChat
        ? 256.0
        : 8.0;
    final railInnerEdge =
        canvasSize.width - (isCompactLandscape ? 208.0 : 216.0);
    final horizontalOverhang = math.min(
      _horizontalOverhang,
      math.max(
        0.0,
        math.min(feltLeft - leftLimit, railInnerEdge - 8 - feltRight),
      ),
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
      boardVerticalInset: seatCardSize.height - _verticalOverhang + 24,
      seatHorizontalOverhang: horizontalOverhang,
      seatVerticalOverhang: _verticalOverhang,
    );
  }
}

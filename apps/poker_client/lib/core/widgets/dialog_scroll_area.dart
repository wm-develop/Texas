import 'dart:math' as math;

import 'package:flutter/material.dart';

/// 弹窗里可滚动的内容区，高度按**屏幕真实可用空间**算，不依赖弹窗内部布局。
///
/// 上游 Flutter 的 AlertDialog 会把内容压到标题、按钮之外的剩余空间，所以内容
/// 区就算限高写错了也会被悄悄修正；OpenHarmony 那版没有这一步，写多少就是
/// 多少。设置弹窗曾按「屏幕高度 70%」限高：鸿蒙手机上屏幕约 360 高，70% 是
/// 252，而弹窗里实际只能看见约 150，滚动范围按 252 算，最后一项永远落在被
/// 裁掉的那一截里——普通房主滚到「自动加入语音」就到底，管理员因为多一项
/// 反而能滚到「房间管理」。安卓与鸿蒙平板走上游逻辑，一直正常。
///
/// [reservedHeight] 是弹窗自身的标题、按钮与内边距要占的高度，按 Material 3
/// 的尺寸取保守值：标题约 72，按钮行约 72，内容上下内边距约 24。
class DialogScrollArea extends StatelessWidget {
  const DialogScrollArea({
    required this.child,
    this.reservedHeight = 168,
    super.key,
  });

  final Widget child;
  final double reservedHeight;

  /// 弹窗外侧的默认留白（Dialog.insetPadding 上下各 24）。
  static const double dialogInsets = 48;

  /// 再矮的屏幕也至少给这么多，避免内容区被压成一条缝。
  static const double minimumHeight = 96;

  /// 内容区允许的最大高度，从 [media] 里的屏幕高度、系统避让区和键盘扣起。
  static double maxHeightFor(MediaQueryData media, double reservedHeight) {
    final available =
        media.size.height -
        media.viewPadding.top -
        media.viewPadding.bottom -
        media.viewInsets.bottom -
        dialogInsets -
        reservedHeight;
    return math.max(available, minimumHeight);
  }

  @override
  Widget build(BuildContext context) => ConstrainedBox(
    constraints: BoxConstraints(
      maxHeight: maxHeightFor(MediaQuery.of(context), reservedHeight),
    ),
    child: SingleChildScrollView(child: child),
  );
}

import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

const _displayCutoutChannel = MethodChannel(
  'com.texas.game.poker_client/display_cutout',
);

/// 原生上报的屏幕几何：挖孔与系统栏的避让区，以及屏幕圆角半径。
///
/// 圆角不属于挖孔也不属于系统栏，两套 inset 都不包含它，但它会实实在在地
/// 切掉贴边控件的一角——安卓手机上右上角的聊天按钮就被切过。因此单独上报。
class NativeScreenInsets {
  const NativeScreenInsets({
    this.cutout = EdgeInsets.zero,
    this.cornerTopLeft = 0,
    this.cornerTopRight = 0,
    this.cornerBottomLeft = 0,
    this.cornerBottomRight = 0,
  });

  final EdgeInsets cutout;
  final double cornerTopLeft;
  final double cornerTopRight;
  final double cornerBottomLeft;
  final double cornerBottomRight;

  static const NativeScreenInsets zero = NativeScreenInsets();

  @override
  bool operator ==(Object other) =>
      other is NativeScreenInsets &&
      other.cutout == cutout &&
      other.cornerTopLeft == cornerTopLeft &&
      other.cornerTopRight == cornerTopRight &&
      other.cornerBottomLeft == cornerBottomLeft &&
      other.cornerBottomRight == cornerBottomRight;

  @override
  int get hashCode => Object.hash(
    cutout,
    cornerTopLeft,
    cornerTopRight,
    cornerBottomLeft,
    cornerBottomRight,
  );
}

class NativeDisplayCutout {
  const NativeDisplayCutout._();

  static bool get _supported =>
      !kIsWeb &&
      (defaultTargetPlatform == TargetPlatform.android ||
          defaultTargetPlatform == TargetPlatform.ohos);

  static Future<NativeScreenInsets> read() async {
    if (!_supported) return NativeScreenInsets.zero;
    try {
      final value = await _displayCutoutChannel
          .invokeMapMethod<String, Object?>('getInsets');
      return decodeScreenInsets(value);
    } on MissingPluginException {
      return NativeScreenInsets.zero;
    } on PlatformException {
      return NativeScreenInsets.zero;
    }
  }

  @visibleForTesting
  static NativeScreenInsets decodeScreenInsets(Map<String, Object?>? value) =>
      NativeScreenInsets(
        cutout: decode(value),
        cornerTopLeft: _number(value?['cornerTopLeft']),
        cornerTopRight: _number(value?['cornerTopRight']),
        cornerBottomLeft: _number(value?['cornerBottomLeft']),
        cornerBottomRight: _number(value?['cornerBottomRight']),
      );

  @visibleForTesting
  static EdgeInsets decode(Map<String, Object?>? value) => EdgeInsets.fromLTRB(
    _number(value?['left']),
    _number(value?['top']),
    _number(value?['right']),
    _number(value?['bottom']),
  );

  /// SafeArea already consumes any inset exposed through MediaQuery. Apply
  /// only the native cutout portion that Flutter did not consume.
  static EdgeInsets remainingAfter(
    EdgeInsets nativeCutout,
    EdgeInsets mediaPadding,
  ) => EdgeInsets.fromLTRB(
    math.max(0, nativeCutout.left - mediaPadding.left),
    math.max(0, nativeCutout.top - mediaPadding.top),
    math.max(0, nativeCutout.right - mediaPadding.right),
    math.max(0, nativeCutout.bottom - mediaPadding.bottom),
  );

  /// 还需要自己让开的系统区域，取原生上报与 Flutter 自己的 inset 中较大的一份。
  ///
  /// 平板横屏时手势导航条压在屏幕底部，落在那片区域的按钮即使画出来了也点不动，
  /// 因为触摸归系统。原生通道现在会把导航条一起上报；但通道在某些设备上可能
  /// 拿不到值，[viewPadding] 与 [systemGestureInsets] 是 Flutter 侧的同源信息，
  /// 作为兜底。三者都减去 [mediaPadding]——SafeArea 已经消费掉的部分不能重复计。
  ///
  /// 侧边只看 [viewPadding]：[systemGestureInsets] 在左右边缘含返回手势的宽条，
  /// 那片区域点击其实是好用的，按它让开会白白牺牲横向空间。
  static EdgeInsets remainingSystemInsets({
    required EdgeInsets nativeCutout,
    required EdgeInsets mediaPadding,
    required EdgeInsets viewPadding,
    required EdgeInsets systemGestureInsets,
  }) {
    double remaining(double native, double view, double consumed) =>
        math.max(0, math.max(native, view) - consumed);
    return EdgeInsets.fromLTRB(
      remaining(nativeCutout.left, viewPadding.left, mediaPadding.left),
      remaining(nativeCutout.top, viewPadding.top, mediaPadding.top),
      remaining(nativeCutout.right, viewPadding.right, mediaPadding.right),
      remaining(
        nativeCutout.bottom,
        math.max(viewPadding.bottom, systemGestureInsets.bottom),
        mediaPadding.bottom,
      ),
    );
  }

  /// 圆角还需要自己让开多少。
  ///
  /// 圆角半径是相对屏幕物理边缘的，而 SafeArea 与挖孔内边距已经把内容推离了
  /// 边缘那么多，这部分不能重复计——否则贴边控件会被推得过分靠内。
  static double remainingCorner(double radius, double consumed) =>
      math.max(0, radius - consumed);

  static double _number(Object? value) => switch (value) {
    num number when number.isFinite => math.max(0, number.toDouble()),
    _ => 0,
  };
}

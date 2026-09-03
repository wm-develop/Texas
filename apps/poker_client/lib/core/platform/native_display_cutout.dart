import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

const _displayCutoutChannel = MethodChannel(
  'com.texas.game.poker_client/display_cutout',
);

class NativeDisplayCutout {
  const NativeDisplayCutout._();

  static bool get _supported =>
      !kIsWeb &&
      (defaultTargetPlatform == TargetPlatform.android ||
          defaultTargetPlatform == TargetPlatform.ohos);

  static Future<EdgeInsets> read() async {
    if (!_supported) return EdgeInsets.zero;
    try {
      final value = await _displayCutoutChannel
          .invokeMapMethod<String, Object?>('getInsets');
      return decode(value);
    } on MissingPluginException {
      return EdgeInsets.zero;
    } on PlatformException {
      return EdgeInsets.zero;
    }
  }

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

  static double _number(Object? value) => switch (value) {
    num number when number.isFinite => math.max(0, number.toDouble()),
    _ => 0,
  };
}

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

  static double _number(Object? value) => switch (value) {
    num number when number.isFinite => math.max(0, number.toDouble()),
    _ => 0,
  };
}

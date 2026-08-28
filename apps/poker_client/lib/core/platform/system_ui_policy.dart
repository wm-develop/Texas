import 'package:flutter/foundation.dart';

/// The Dart `SystemChrome` immersive APIs are only used by the Android host.
///
/// The OpenHarmony embedding manages full-screen layout from `FlutterAbility`.
/// Calling the generic platform channel before its Flutter view has obtained a
/// UIContext can leave the application attached to a blank native window.
bool shouldUseDartSystemUi({
  required bool isWeb,
  required TargetPlatform platform,
}) {
  return !isWeb && platform == TargetPlatform.android;
}

bool get shouldUseCurrentPlatformDartSystemUi =>
    shouldUseDartSystemUi(isWeb: kIsWeb, platform: defaultTargetPlatform);

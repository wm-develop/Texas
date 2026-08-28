import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/platform/system_ui_policy.dart';

void main() {
  test('Dart immersive system UI is enabled only for native Android', () {
    expect(
      shouldUseDartSystemUi(isWeb: false, platform: TargetPlatform.android),
      isTrue,
    );
    expect(
      shouldUseDartSystemUi(isWeb: false, platform: TargetPlatform.ohos),
      isFalse,
    );
    expect(
      shouldUseDartSystemUi(isWeb: true, platform: TargetPlatform.android),
      isFalse,
    );
  });
}

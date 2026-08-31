import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/platform/native_display_cutout.dart';

void main() {
  test('decodes non-negative native cutout values', () {
    expect(
      NativeDisplayCutout.decode({
        'left': 42,
        'top': 3.5,
        'right': -8,
        'bottom': null,
      }),
      const EdgeInsets.fromLTRB(42, 3.5, 0, 0),
    );
  });

  test('only applies the cutout portion not consumed by SafeArea', () {
    expect(
      NativeDisplayCutout.remainingAfter(
        const EdgeInsets.fromLTRB(48, 0, 20, 0),
        const EdgeInsets.fromLTRB(16, 0, 20, 0),
      ),
      const EdgeInsets.only(left: 32),
    );
  });
}

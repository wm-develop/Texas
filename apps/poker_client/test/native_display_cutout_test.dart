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
  group('还需要让开的系统区域', () {
    const zero = EdgeInsets.zero;

    test('手势导航条不在 padding 里时要自己让开', () {
      // 平板横屏的导航条压在屏幕底部，落在那片区域的按钮点不动
      expect(
        NativeDisplayCutout.remainingSystemInsets(
          nativeCutout: zero,
          mediaPadding: zero,
          viewPadding: const EdgeInsets.only(bottom: 24),
          systemGestureInsets: const EdgeInsets.only(bottom: 32),
        ),
        const EdgeInsets.only(bottom: 32),
        reason: '取 viewPadding 与手势区中较大的一份',
      );
    });

    test('SafeArea 已消费的部分不重复计算', () {
      expect(
        NativeDisplayCutout.remainingSystemInsets(
          nativeCutout: zero,
          mediaPadding: const EdgeInsets.only(bottom: 32),
          viewPadding: const EdgeInsets.only(bottom: 32),
          systemGestureInsets: const EdgeInsets.only(bottom: 32),
        ),
        zero,
      );
    });

    test('原生上报与 Flutter inset 取较大者，互为兜底', () {
      // 原生通道在某些设备上拿不到值，反之 Flutter 也可能报 0
      expect(
        NativeDisplayCutout.remainingSystemInsets(
          nativeCutout: const EdgeInsets.only(bottom: 48),
          mediaPadding: zero,
          viewPadding: const EdgeInsets.only(bottom: 24),
          systemGestureInsets: zero,
        ).bottom,
        48,
      );
      expect(
        NativeDisplayCutout.remainingSystemInsets(
          nativeCutout: zero,
          mediaPadding: zero,
          viewPadding: const EdgeInsets.only(bottom: 24),
          systemGestureInsets: zero,
        ).bottom,
        24,
      );
    });

    test('左右不按手势区让开，避免白白牺牲横向空间', () {
      // 返回手势的宽条上点击其实是好用的
      final insets = NativeDisplayCutout.remainingSystemInsets(
        nativeCutout: zero,
        mediaPadding: zero,
        viewPadding: zero,
        systemGestureInsets: const EdgeInsets.symmetric(horizontal: 32),
      );
      expect(insets.left, 0);
      expect(insets.right, 0);
    });

    test('挖孔仍然被让开', () {
      expect(
        NativeDisplayCutout.remainingSystemInsets(
          nativeCutout: const EdgeInsets.only(left: 40, top: 10),
          mediaPadding: const EdgeInsets.only(top: 10),
          viewPadding: const EdgeInsets.only(top: 10),
          systemGestureInsets: zero,
        ),
        const EdgeInsets.only(left: 40),
      );
    });
  });
}

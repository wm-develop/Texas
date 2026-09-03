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
  group('屏幕圆角', () {
    test('解析原生上报的四角半径', () {
      // 圆角不属于挖孔也不属于系统栏，两套 inset 都不包含它
      final insets = NativeDisplayCutout.decodeScreenInsets({
        'left': 30.0,
        'top': 0.0,
        'right': 0.0,
        'bottom': 24.0,
        'cornerTopLeft': 48.0,
        'cornerTopRight': 48.0,
        'cornerBottomLeft': 40.0,
        'cornerBottomRight': 40.0,
      });

      expect(insets.cutout, const EdgeInsets.fromLTRB(30, 0, 0, 24));
      expect(insets.cornerTopRight, 48);
      expect(insets.cornerBottomLeft, 40);
    });

    test('旧宿主不上报圆角时按 0 处理', () {
      final insets = NativeDisplayCutout.decodeScreenInsets({
        'left': 0.0,
        'top': 0.0,
        'right': 0.0,
        'bottom': 0.0,
      });
      expect(insets.cornerTopLeft, 0);
      expect(insets.cornerTopRight, 0);
    });

    test('已被安全区推开的部分不重复让开', () {
      // 否则贴边控件会被推得过分靠内
      expect(NativeDisplayCutout.remainingCorner(48, 0), 48);
      expect(NativeDisplayCutout.remainingCorner(48, 20), 28);
      expect(NativeDisplayCutout.remainingCorner(48, 48), 0);
      expect(NativeDisplayCutout.remainingCorner(48, 60), 0);
      expect(NativeDisplayCutout.remainingCorner(0, 0), 0);
    });

    test('相同的屏幕几何视为相等，避免无谓重建', () {
      const first = NativeScreenInsets(
        cutout: EdgeInsets.only(left: 30),
        cornerTopRight: 48,
      );
      const same = NativeScreenInsets(
        cutout: EdgeInsets.only(left: 30),
        cornerTopRight: 48,
      );
      const different = NativeScreenInsets(
        cutout: EdgeInsets.only(left: 30),
        cornerTopRight: 12,
      );
      expect(first, same);
      expect(first.hashCode, same.hashCode);
      expect(first, isNot(different));
    });
  });
}

import 'dart:ui';

import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/presentation/table_viewport_layout.dart';

/// 两种布局族的代表性画布。座位几何必须在两者上都成立。
final _layouts = <String, TableViewportLayout>{
  '桌面 1280x720 聊天收起': TableViewportLayout.fromSize(
    const Size(1280, 720),
    chatVisible: false,
  ),
  '桌面 2000x900 聊天停靠': TableViewportLayout.fromSize(
    const Size(2000, 900),
    chatVisible: true,
  ),
  '桌面 1600x720 聊天收起': TableViewportLayout.fromSize(
    const Size(1600, 720),
    chatVisible: false,
  ),
  '手机 920x420': TableViewportLayout.fromSize(
    const Size(920, 420),
    chatVisible: true,
  ),
  '手机 2400x1080': TableViewportLayout.fromSize(
    const Size(2400, 1080),
    chatVisible: true,
    compactOverride: true,
  ),
};

/// supportsSideChat 只表示画布放得下聊天栏，不代表它已经展开；
/// 只有真正停靠时左栏才被占用。
const _chatDocked = {'桌面 2000x900 聊天停靠'};

void main() {
  test('preserves the desktop composition', () {
    final layout = TableViewportLayout.fromSize(
      const Size(1280, 720),
      chatVisible: true,
    );

    expect(layout.canvasSize, const Size(1280, 720));
    // 1280 宽同时停靠聊天与下注区会把公共牌区挤得放不下 5 张公共牌，
    // 因此这个宽度下聊天改为弹窗，牌桌拿回宽度。
    expect(layout.supportsSideChat, isFalse);
    expect(layout.boardRect.width, greaterThan(5 * 66));
  });

  test('fills a wide phone while limiting poker table stretching', () {
    final layout = TableViewportLayout.fromSize(
      const Size(2400, 1080),
      chatVisible: true,
    );

    expect(
      layout.canvasSize.aspectRatio,
      closeTo(const Size(2400, 1080).aspectRatio, 0.001),
    );
    expect(layout.tableRect.width, TableViewportLayout.maxTableWidth);
  });

  test('enlarges content on a logical-size landscape phone', () {
    final layout = TableViewportLayout.fromSize(
      const Size(920, 420),
      chatVisible: true,
    );

    expect(layout.isCompactLandscape, isTrue);
    expect(layout.canvasSize.height, TableViewportLayout.compactDesignHeight);
    expect(layout.supportsSideChat, isFalse);
    expect(
      layout.tableRect.width,
      closeTo(
        layout.canvasSize.width -
            TableViewportLayout.compactLeftRailWidth -
            TableViewportLayout.compactRightRailWidth,
        0.001,
      ),
    );
    expect(layout.boardRect.width, greaterThan(5 * 66));
  });

  test('聊天收起时牌桌在下注区左侧的空间内居中', () {
    final layout = TableViewportLayout.fromSize(
      const Size(1600, 720),
      chatVisible: false,
    );

    expect(layout.tableRect.width, TableViewportLayout.maxTableWidth);
    // 右栏恒定被下注区占用，所以牌桌不再相对整幅画布居中，
    // 而是在「左边距 ~ 右栏内缘」之间居中；两侧留白应当相等。
    const leftMargin = 104.0;
    final railInnerEdge =
        layout.canvasSize.width - TableViewportLayout.betRailWidth;
    expect(
      layout.tableRect.left - leftMargin,
      closeTo(railInnerEdge - layout.tableRect.right, 0.001),
    );
  });

  test('clamps extreme portrait and ultrawide windows', () {
    final portrait = TableViewportLayout.fromSize(
      const Size(600, 1200),
      chatVisible: true,
    );
    final ultrawide = TableViewportLayout.fromSize(
      const Size(3200, 900),
      chatVisible: true,
    );

    expect(
      portrait.canvasSize.aspectRatio,
      TableViewportLayout.minCanvasAspect,
    );
    expect(
      ultrawide.canvasSize.aspectRatio,
      TableViewportLayout.maxCanvasAspect,
    );
    expect(portrait.supportsSideChat, isFalse);
  });

  test('compactOverride 固定布局族，键盘压缩高度不会切换布局', () {
    final tabletWithKeyboard = TableViewportLayout.fromSize(
      const Size(1280, 480),
      chatVisible: true,
      compactOverride: false,
    );
    expect(tabletWithKeyboard.isCompactLandscape, isFalse);

    final phoneOverride = TableViewportLayout.fromSize(
      const Size(920, 700),
      chatVisible: true,
      compactOverride: true,
    );
    expect(phoneOverride.isCompactLandscape, isTrue);
  });

  group('紧凑判定使用短边而不是宽高比', () {
    test('被拖矮的桌面窗口不会翻成手机布局', () {
      // 1400x620 宽高比 2.26，比多数手机还宽，但短边 620 不是手机
      final layout = TableViewportLayout.fromSize(
        const Size(1400, 620),
        chatVisible: false,
      );
      expect(layout.isCompactLandscape, isFalse);
    });

    test('窗口被拖得足够小时才切紧凑布局', () {
      final layout = TableViewportLayout.fromSize(
        const Size(1000, 480),
        chatVisible: false,
      );
      expect(layout.isCompactLandscape, isTrue);
    });

    test('compactOverride 仍然优先', () {
      final pinned = TableViewportLayout.fromSize(
        const Size(1400, 620),
        chatVisible: false,
        compactOverride: true,
      );
      expect(pinned.isCompactLandscape, isTrue);
    });
  });

  group('座位落位', () {
    // 这是本文件最重要的一组断言。此前测试自己按「圆心 × 半宽」另算了一份座位
    // 位置，与 Align 的真实语义（按 extent - card 的空隙插值）不符，结果侧边
    // 座位实际压住公共牌区 60 余像素却依然通过。现在改为直接使用布局自己
    // 提供的 seatRect，实现与断言不可能再各算各的。
    for (final entry in _layouts.entries) {
      test('${entry.key}：2～10 人座位都不遮挡公共牌区', () {
        final layout = entry.value;
        for (var seatCount = 2; seatCount <= 10; seatCount++) {
          for (var index = 0; index < seatCount; index++) {
            final seat = layout.seatRect(index, seatCount);
            expect(
              seat.overlaps(layout.boardRect),
              isFalse,
              reason:
                  '$seatCount 人牌桌第 ${index + 1} 个座位 $seat '
                  '压住公共牌区 ${layout.boardRect}',
            );
          }
        }
      });

      test('${entry.key}：2～10 人座位彼此不重叠', () {
        // 10 人是最密的一档。按角度投影的旧分布会在底边挤下三个玩家框，
        // 相邻两框中心只差 159 像素而框宽 216，直接叠在一起。
        final layout = entry.value;
        for (var seatCount = 2; seatCount <= 10; seatCount++) {
          final seats = [
            for (var index = 0; index < seatCount; index++)
              layout.seatRect(index, seatCount),
          ];
          for (var a = 0; a < seats.length; a++) {
            for (var b = a + 1; b < seats.length; b++) {
              expect(
                seats[a].overlaps(seats[b]),
                isFalse,
                reason:
                    '$seatCount 人牌桌第 ${a + 1} 与第 ${b + 1} 个座位重叠：'
                    '${seats[a]} / ${seats[b]}',
              );
            }
          }
        }
      });

      test('${entry.key}：座位不超出画布', () {
        final layout = entry.value;
        final canvas = Offset.zero & layout.canvasSize;
        for (var seatCount = 2; seatCount <= 10; seatCount++) {
          for (var index = 0; index < seatCount; index++) {
            final seat = layout.seatRect(index, seatCount);
            expect(
              canvas.contains(seat.topLeft) &&
                  canvas.contains(seat.bottomRight - const Offset(1, 1)),
              isTrue,
              reason: '$seatCount 人牌桌第 ${index + 1} 个座位 $seat 被画布裁切',
            );
          }
        }
      });
    }

    test('顶部与底部玩家框约 1/3 落在桌内、2/3 落在桌外', () {
      // 基准必须是玩家看得见的桌沿 feltRect，不是更大的 tableRect：
      // 两者相差 54 像素，按 tableRect 计算会让玩家框整个落在桌外。
      final expected =
          TableViewportLayout.seatCardSize.height *
          TableViewportLayout.seatInsideFraction;
      for (final entry in _layouts.entries) {
        final layout = entry.value;
        // 座位 0 恒为本人，落在正下方；两人桌的座位 1 落在正上方。
        final bottom = layout.seatRect(0, 2);
        final top = layout.seatRect(1, 2);
        expect(
          layout.feltRect.bottom - bottom.top,
          closeTo(expected, 1),
          reason: '${entry.key}：底部玩家框落在桌内的比例不对',
        );
        expect(
          top.bottom - layout.feltRect.top,
          closeTo(expected, 1),
          reason: '${entry.key}：顶部玩家框落在桌内的比例不对',
        );
        expect(
          bottom.top,
          lessThan(layout.feltRect.bottom),
          reason: '${entry.key}：底部玩家框必须真的压在桌沿上，不能整个悬在桌外',
        );
      }
    });

    test('座位不侵入左右两栏', () {
      // 左栏放房间信息或聊天面板，右栏放下注区。玩家框可以伸出桌沿，
      // 但绝不能压到这两栏上面，否则会挡住聊天或让人点不到下注按钮。
      for (final entry in _layouts.entries) {
        final layout = entry.value;
        final compact = layout.isCompactLandscape;
        final betRail = Rect.fromLTRB(
          layout.canvasSize.width - (compact ? 208 : 216),
          0,
          layout.canvasSize.width,
          layout.canvasSize.height,
        );
        final leftRail = compact
            ? Rect.fromLTRB(0, 0, 160, layout.canvasSize.height)
            : _chatDocked.contains(entry.key)
            ? const Rect.fromLTRB(18, 116, 248, 10000)
            : Rect.zero;
        for (var seatCount = 2; seatCount <= 10; seatCount++) {
          for (var index = 0; index < seatCount; index++) {
            final seat = layout.seatRect(index, seatCount);
            expect(
              seat.overlaps(betRail),
              isFalse,
              reason: '${entry.key}：$seatCount 人第 ${index + 1} 个座位压住下注区',
            );
            if (leftRail != Rect.zero) {
              expect(
                seat.overlaps(leftRail),
                isFalse,
                reason: '${entry.key}：$seatCount 人第 ${index + 1} 个座位压住左栏',
              );
            }
          }
        }
      }
    });

    test('画布够宽、牌桌被上限限住时侧边座位才向外让出空间', () {
      final layout = TableViewportLayout.fromSize(
        const Size(2400, 900),
        chatVisible: false,
      );
      expect(layout.tableRect.width, TableViewportLayout.maxTableWidth);

      final side = layout.seatRect(1, 4);
      expect(
        side.left,
        lessThan(layout.tableRect.left),
        reason: '有余量时侧边座位应当伸出桌沿，把桌面让给公共牌区',
      );
    });
  });
}

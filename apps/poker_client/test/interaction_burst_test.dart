import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/table/presentation/table_seat_widgets.dart';

void main() {
  group('赞赏/嘲讽气泡的轨迹', () {
    test('从玩家框底部出发向上飘，全程不离开玩家框的竖向范围', () {
      // 玩家框高 116，中心上下各 58。顶排座位的框顶就是画布顶，气泡一旦
      // 飘出这个范围就会被裁掉——此前从中心上方 48 起飘再向上 48，一出来
      // 就在画布外面。
      const halfHeight = 58.0;
      double? previous;
      for (var step = 0; step <= 20; step++) {
        final progress = step / 20;
        final offset = TablePlayerInteractionBurst.offsetFor(
          progress,
          praise: true,
        );
        expect(offset.dy, lessThanOrEqualTo(halfHeight), reason: '$progress');
        expect(
          offset.dy,
          greaterThanOrEqualTo(-halfHeight),
          reason: '$progress',
        );
        if (previous != null) {
          expect(offset.dy, lessThan(previous), reason: '必须一直向上飘');
        }
        previous = offset.dy;
      }
    });

    test('起点在框中心以下（底部），终点在中心以上', () {
      expect(
        TablePlayerInteractionBurst.offsetFor(0, praise: true).dy,
        greaterThan(0),
      );
      expect(
        TablePlayerInteractionBurst.offsetFor(1, praise: true).dy,
        lessThan(0),
      );
    });

    test('赞赏不抖动，嘲讽左右抖动但幅度有限', () {
      for (var step = 0; step <= 20; step++) {
        final progress = step / 20;
        expect(
          TablePlayerInteractionBurst.offsetFor(progress, praise: true).dx,
          0,
        );
        expect(
          TablePlayerInteractionBurst.offsetFor(progress, praise: false).dx.abs(),
          lessThanOrEqualTo(8),
        );
      }
    });
  });

  testWidgets('气泡组件能在玩家框大小的区域内渲染而不报错', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Center(
            child: SizedBox(
              width: 216,
              height: 116,
              child: Stack(
                children: [
                  Align(
                    child: TablePlayerInteractionBurst(
                      interaction: TablePlayerInteraction(
                        interactionId: 'i1',
                        fromUserId: 'a',
                        fromDisplayName: '甲',
                        targetUserId: 'b',
                        targetDisplayName: '乙',
                        kind: 'praise',
                        sentAt: DateTime.fromMillisecondsSinceEpoch(0),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
    await tester.pump(const Duration(milliseconds: 800));
    expect(tester.takeException(), isNull);
    expect(find.textContaining('甲 赞赏'), findsOneWidget);
  });
}

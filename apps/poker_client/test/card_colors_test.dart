import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/presentation/table_card_widgets.dart';
import 'package:poker_client/features/table/presentation/table_deal_controller.dart';
import 'package:poker_client/features/table/presentation/table_seat_widgets.dart';

void main() {
  group('四色牌', () {
    test('四种花色四种颜色，黑桃与梅花不再同色', () {
      // 试玩反馈：玩家框里的牌太小，黑桃和梅花分不清
      final colors = {
        for (final suit in const ['♠', '♥', '♦', '♣']) suit: suitColor(suit),
      };
      expect(colors.values.toSet().length, 4);
      expect(suitColor('♠'), isNot(suitColor('♣')));
    });

    test('按牌面文字取色，两位数点数也能识别花色', () {
      expect(suitColorForLabel('10♣'), suitColor('♣'));
      expect(suitColorForLabel('A♠'), suitColor('♠'));
      expect(suitColorForLabel(''), suitColor(''));
    });

    testWidgets('玩家框小牌与公共牌大牌使用同一套花色颜色', (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: Row(
              children: [
                TableMiniCard(label: '7♣', compact: true),
                TablePlayingCard(rank: '7', suit: '♣'),
              ],
            ),
          ),
        ),
      );
      final mini = tester.widget<Text>(find.text('7♣'));
      final big = tester.widget<Text>(find.text('7\n♣'));
      expect(mini.style?.color, suitColor('♣'));
      expect(big.style?.color, suitColor('♣'));
    });
  });

  testWidgets('轮到某人行动时玩家框里没有倒计时也没有文字', (tester) async {
    // 轮到谁由高亮边框表示，倒计时只在公共牌区显示一处
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Center(
            child: SizedBox(
              width: 216,
              height: 116,
              child: TableSeatCard(
                seat: const TableSeat(
                  number: 3,
                  userId: 'other',
                  displayName: '对手',
                  chips: 1000,
                  isCurrentActor: true,
                ),
                actionRemaining: const Duration(seconds: 12),
                showReadyStatus: false,
                winnerAmount: 0,
                onAvatarTap: () {},
                onUseTimeExtension: () {},
                deal: const SeatDealState.settled(),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.pump();

    expect(find.text('行动中'), findsNothing);
    expect(find.textContaining('剩余'), findsNothing);
    expect(find.textContaining('秒'), findsNothing);
    expect(tester.takeException(), isNull);
  });
}

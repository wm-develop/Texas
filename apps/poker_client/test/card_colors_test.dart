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

    testWidgets('小牌与大牌的点数和花色图形都用同一套花色颜色', (tester) async {
      // 花色符号在 Android / HarmonyOS 上会被彩色 emoji 字体接管，文字颜色
      // 对它不起作用，所以花色必须是自绘图形，颜色才能和点数一致
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
      final ranks = tester.widgetList<Text>(find.text('7')).toList();
      expect(ranks, hasLength(2));
      for (final rank in ranks) {
        expect(rank.style?.color, suitColor('♣'));
      }
      final glyphs = tester.widgetList<SuitGlyph>(find.byType(SuitGlyph)).toList();
      expect(glyphs, hasLength(2));
      for (final glyph in glyphs) {
        expect(glyph.suit, '♣');
        expect(glyph.color, suitColor('♣'));
      }
      expect(find.text('7♣'), findsNothing, reason: '不再依赖字体渲染花色字符');
    });

    testWidgets('「10」在玩家框小牌里和别的牌一样上下居中，不溢出', (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: Row(
              children: [
                TableMiniCard(label: '10♦', compact: true),
                TableMiniCard(label: '7♣', compact: true),
              ],
            ),
          ),
        ),
      );
      expect(find.text('10'), findsOneWidget);
      expect(find.text('7'), findsOneWidget);
      expect(tester.takeException(), isNull);
      // 两张牌的点数文字都在各自牌面的水平中线上
      for (final label in const ['10♦', '7♣']) {
        final card = find.byWidgetPredicate(
          (widget) => widget is TableMiniCard && widget.label == label,
        );
        final rank = find.descendant(of: card, matching: find.byType(Text));
        final cardCenter = tester.getCenter(card).dx;
        expect((tester.getCenter(rank).dx - cardCenter).abs(), lessThan(1.5));
      }
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

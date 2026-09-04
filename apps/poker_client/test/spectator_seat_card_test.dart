import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/presentation/table_card_widgets.dart';
import 'package:poker_client/features/table/presentation/table_deal_controller.dart';
import 'package:poker_client/features/table/presentation/table_seat_widgets.dart';

/// 别人的座位。观战者付费后服务端会把手牌填进这些座位，界面要照本人的牌
/// 一样正面展示；普通玩家拿不到这些数据，座位里就没有牌。
TableSeat _otherSeat({List<String> holeCards = const []}) => TableSeat(
  number: 3,
  userId: 'other',
  displayName: '对手',
  chips: 1000,
  isCurrentUser: false,
  isFolded: false,
  streetBet: 0,
  holeCards: holeCards,
  revealedCards: const [],
  handCategory: '',
  isParticipating: true,
  isConnected: true,
);

Future<void> _pump(WidgetTester tester, TableSeat seat, SeatDealState deal) =>
    tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Center(
            child: SizedBox(
              width: 216,
              height: 116,
              child: TableSeatCard(
                seat: seat,
                actionRemaining: Duration.zero,
                showReadyStatus: false,
                winnerAmount: 0,
                onAvatarTap: () {},
                onUseTimeExtension: () {},
                deal: deal,
              ),
            ),
          ),
        ),
      ),
    );

void main() {
  testWidgets('观战者拿到别人的手牌时，发牌演出翻开而不是淡出牌背', (tester) async {
    // 此前只有本人的牌会翻开，别人的一律以牌背淡出；观战者的座位数据里有牌，
    // 也要按同样的方式翻出来
    await _pump(
      tester,
      _otherSeat(holeCards: const ['As', 'Kd']),
      const SeatDealState(dealtCards: 2, flipProgress: 1, isDealing: true),
    );
    await tester.pump();

    expect(find.byType(TableMiniFlipCard), findsNWidgets(2));
    expect(find.text('A♠'), findsOneWidget);
    expect(find.text('K♦'), findsOneWidget);
  });

  testWidgets('普通玩家看别人的座位仍然只有牌背', (tester) async {
    // 服务端没给数据，客户端也不该凭空显示
    await _pump(
      tester,
      _otherSeat(),
      const SeatDealState(dealtCards: 2, flipProgress: 1, isDealing: true),
    );
    await tester.pump();

    expect(find.byType(TableMiniFlipCard), findsNothing);
    expect(find.byType(TableMiniCardBack), findsNWidgets(2));
  });

  testWidgets('演出结束后观战者看到的是正面的小牌', (tester) async {
    await _pump(
      tester,
      _otherSeat(holeCards: const ['7c', '7h']),
      const SeatDealState.settled(),
    );
    await tester.pump();

    expect(find.byType(TableMiniCard), findsNWidgets(2));
    expect(find.byType(TableMiniCardBack), findsNothing);
  });
}

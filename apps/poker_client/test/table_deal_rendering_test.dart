import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/presentation/table_card_widgets.dart';
import 'package:poker_client/features/table/presentation/table_deal_controller.dart';
import 'package:poker_client/features/table/presentation/table_seat_widgets.dart';

TableSeat _seat({
  bool isCurrentUser = false,
  List<String> holeCards = const [],
}) => TableSeat(
  number: 1,
  userId: 'me',
  displayName: '玩家',
  chips: 1000,
  isCurrentUser: isCurrentUser,
  isFolded: false,
  streetBet: 0,
  holeCards: holeCards,
  revealedCards: const [],
  handCategory: '',
  isParticipating: true,
  isConnected: true,
);

Future<void> _pump(
  WidgetTester tester,
  TableSeat seat,
  SeatDealState deal,
) => tester.pumpWidget(
  MaterialApp(
    home: Scaffold(
      body: Center(
        child: SizedBox(
          width: 260,
          height: 160,
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
  testWidgets('本人底牌先落牌背，再翻开成正面', (tester) async {
    final seat = _seat(isCurrentUser: true, holeCards: const ['As', 'Kd']);

    // 只发了一张，且还没到翻牌阶段
    await _pump(
      tester,
      seat,
      const SeatDealState(dealtCards: 1, flipProgress: 0, isDealing: true),
    );
    expect(find.byType(TableMiniCardBack), findsOneWidget);
    expect(find.byType(TableMiniCard), findsNothing);

    // 两张都发完并翻开
    await _pump(
      tester,
      seat,
      const SeatDealState(dealtCards: 2, flipProgress: 1, isDealing: true),
    );
    expect(find.byType(TableMiniCard), findsNWidgets(2));
    expect(find.byType(TableMiniCardBack), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('别人的底牌只出现牌背，翻牌阶段淡出', (tester) async {
    final seat = _seat();

    await _pump(
      tester,
      seat,
      const SeatDealState(dealtCards: 2, flipProgress: 0, isDealing: true),
    );
    expect(find.byType(TableMiniCardBack), findsNWidgets(2));
    // 服务端从不下发别人的底牌，这里也不该出现正面
    expect(find.byType(TableMiniCard), findsNothing);

    final opacity = tester.widget<Opacity>(
      find
          .ancestor(
            of: find.byType(TableMiniCardBack).first,
            matching: find.byType(Opacity),
          )
          .first,
    );
    expect(opacity.opacity, 1);

    await _pump(
      tester,
      seat,
      const SeatDealState(dealtCards: 2, flipProgress: 1, isDealing: true),
    );
    final faded = tester.widget<Opacity>(
      find
          .ancestor(
            of: find.byType(TableMiniCardBack).first,
            matching: find.byType(Opacity),
          )
          .first,
    );
    expect(faded.opacity, 0, reason: '别人的牌不常驻玩家框，演出结束应完全淡出');
  });

  testWidgets('未参与本手的观战者不出现任何牌', (tester) async {
    // 牌局进行中加入的玩家 dealtCards 恒为 0
    await _pump(
      tester,
      _seat(),
      const SeatDealState(dealtCards: 0, flipProgress: 0, isDealing: true),
    );
    expect(find.byType(TableMiniCardBack), findsNothing);
    expect(find.byType(TableMiniCard), findsNothing);
  });

  testWidgets('没有演出时按快照原样显示本人底牌', (tester) async {
    await _pump(
      tester,
      _seat(isCurrentUser: true, holeCards: const ['As', 'Kd']),
      const SeatDealState.settled(),
    );
    expect(find.byType(TableMiniCard), findsNWidgets(2));
    expect(find.byType(TableMiniCardBack), findsNothing);
  });
}

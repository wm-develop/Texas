import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';
import 'package:poker_client/features/history/presentation/recent_hands_page.dart';
import 'package:poker_client/features/table/presentation/table_card_widgets.dart';

RecentHand _hand({
  List<String> ownCards = const ['As', 'Kh'],
  List<String> opponentCards = const [],
  List<RecentHandAction> actions = const [],
}) => RecentHand(
  handId: 'hand_1',
  roomCode: '123456',
  endedAt: DateTime.utc(2026, 9, 8, 12),
  board: const ['Ac', 'Kd', 'Qh', 'Js', 'Ts'],
  showdown: false,
  players: [
    RecentHandPlayer(
      userId: 'me',
      displayName: '我',
      seat: 1,
      startingStack: 2000,
      endingStack: 2200,
      delta: 200,
      holeCards: ownCards,
    ),
    RecentHandPlayer(
      userId: 'other',
      displayName: '对手',
      seat: 2,
      startingStack: 2000,
      endingStack: 1800,
      delta: -200,
      holeCards: opponentCards,
    ),
  ],
  actions: actions,
);

Future<void> _pump(WidgetTester tester, RecentHand hand) async {
  await tester.pumpWidget(
    MaterialApp(
      home: RecentHandsPage(
        userId: 'me',
        loadHands: () async => [hand],
      ),
    ),
  );
  await tester.pumpAndSettle();
  // 展开详情
  await tester.tap(find.byType(ExpansionTile).first);
  await tester.pumpAndSettle();
}

void main() {
  group('手牌回放', () {
    test('解析动作序列，此前客户端把它丢掉了', () {
      final hand = RecentHand.fromJson({
        'handId': 'hand_1',
        'roomCode': '123456',
        'endedAt': '2026-09-08T12:00:00Z',
        'board': ['Ac', 'Kd', 'Qh'],
        'players': <dynamic>[],
        'showdown': false,
        'actions': [
          {
            'userId': 'me',
            'street': 'preflop',
            'type': 'raise',
            'committed': 60,
            'raiseTo': 60,
          },
        ],
      });
      expect(hand.actions, hasLength(1));
      expect(hand.actions.single.street, 'preflop');
      expect(hand.actions.single.raiseTo, 60);
    });

    test('老服务端不下发动作时按空处理', () {
      final hand = RecentHand.fromJson({
        'handId': 'hand_1',
        'roomCode': '123456',
        'endedAt': '2026-09-08T12:00:00Z',
        'board': <dynamic>[],
        'players': <dynamic>[],
        'showdown': false,
      });
      expect(hand.actions, isEmpty);
    });

    testWidgets('对手没亮牌时写明「未亮牌」，而不是留一片空白', (tester) async {
      // 服务端只下发本人的牌与摊牌时亮过的牌；空白看着像加载失败
      await _pump(tester, _hand());
      expect(find.text('未亮牌'), findsOneWidget);
    });

    testWidgets('对手摊过牌就照常显示', (tester) async {
      await _pump(tester, _hand(opponentCards: const ['2c', '2d']));
      expect(find.text('未亮牌'), findsNothing);
    });

    testWidgets('按街显示动作过程', (tester) async {
      await _pump(
        tester,
        _hand(
          actions: const [
            RecentHandAction(
              userId: 'me',
              street: 'preflop',
              type: 'raise',
              committed: 60,
              raiseTo: 60,
            ),
            RecentHandAction(
              userId: 'other',
              street: 'preflop',
              type: 'call',
              committed: 40,
              raiseTo: 0,
            ),
            RecentHandAction(
              userId: 'other',
              street: 'flop',
              type: 'fold',
              committed: 0,
              raiseTo: 0,
            ),
          ],
        ),
      );

      expect(find.text('过程'), findsOneWidget);
      expect(find.textContaining('翻牌前：我 加注到 60，对手 跟注 40'), findsOneWidget);
      expect(find.textContaining('翻牌：对手 弃牌'), findsOneWidget);
      // 没有动作的街不占地方
      expect(find.byKey(const ValueKey('hand-street-turn')), findsNothing);
    });

    testWidgets('牌面与牌桌共用四色和自绘花色', (tester) async {
      // 花色符号在 Android / HarmonyOS 上会被彩色 emoji 字体接管；回放页此前
      // 自己画一份红黑二色，与牌桌不一致
      await _pump(tester, _hand(ownCards: const ['Ac', 'Kh']));
      final glyphs = tester.widgetList<SuitGlyph>(find.byType(SuitGlyph));
      expect(glyphs, isNotEmpty);
      for (final glyph in glyphs) {
        expect(glyph.color, suitColor(glyph.suit));
      }
    });
  });
}

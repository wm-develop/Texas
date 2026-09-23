import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/features/history/domain/hand_replay.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';
import 'package:poker_client/features/history/presentation/hand_replay_page.dart';
import 'package:poker_client/features/history/presentation/recent_hands_page.dart';
import 'package:poker_client/features/table/presentation/table_canvas.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/table_seat_widgets.dart';

Map<String, dynamic> _seat(
  String userId,
  int stack, {
  int streetBet = 0,
  int totalBet = 0,
  bool folded = false,
  String lastAction = '',
  List<String> holeCards = const [],
}) => {
  'userId': userId,
  'stack': stack,
  'streetBet': streetBet,
  'totalBet': totalBet,
  'folded': folded,
  'lastAction': lastAction,
  'holeCards': holeCards,
};

/// 与服务端 replay.Timeline 同形的一手：三人，庄位 3 号，小盲 1 号、大盲 2 号。
/// 翻牌前 3 号弃牌、1 号跟注、2 号过牌，翻牌两人过牌后摊牌，2 号赢。
/// 以「我」（1 号）的视角：对手 2 号的牌只在摊牌之后出现，3 号的牌永远不出现。
Map<String, dynamic> _timelineJson() {
  const mine = ['As', 'Kd'];
  const shown = ['Qh', 'Qc'];
  const board = ['2c', '7d', '9h', '3s', '4s'];
  return {
    'handId': 'hand_1',
    'roomCode': '123456',
    'startedAt': '2026-09-23T12:00:00Z',
    'endedAt': '2026-09-23T12:01:00Z',
    'smallBlind': 10,
    'bigBlind': 20,
    'dealerSeat': 3,
    'smallBlindSeat': 1,
    'bigBlindSeat': 2,
    'showdown': true,
    'players': [
      {
        'userId': 'me',
        'displayName': '我',
        'seat': 1,
        'position': 'SB',
        'startingStack': 1000,
        'endingStack': 980,
        'delta': -20,
        'isViewer': true,
      },
      {
        'userId': 'rival',
        'displayName': '对手',
        'seat': 2,
        'position': 'BB',
        'startingStack': 1000,
        'endingStack': 1020,
        'delta': 20,
      },
      {
        'userId': 'folder',
        'displayName': '弃牌者',
        'seat': 3,
        'position': 'BTN',
        'startingStack': 1000,
        'endingStack': 1000,
        'delta': 0,
      },
    ],
    // 与引擎一致：摊牌时没弃牌的人都会亮牌，本人也在里面
    'revealedHands': [
      {'playerId': 'me', 'holeCards': mine, 'category': 'high_card'},
      {'playerId': 'rival', 'holeCards': shown, 'category': 'one_pair'},
    ],
    'potAwards': <dynamic>[],
    'steps': [
      {
        'index': 0,
        'kind': 'blinds',
        'street': 'preflop',
        'board': <String>[],
        'pot': 30,
        'seats': [
          _seat(
            'me',
            990,
            streetBet: 10,
            totalBet: 10,
            lastAction: 'small_blind',
            holeCards: mine,
          ),
          _seat(
            'rival',
            980,
            streetBet: 20,
            totalBet: 20,
            lastAction: 'big_blind',
          ),
          _seat('folder', 1000),
        ],
      },
      {
        'index': 1,
        'kind': 'action',
        'street': 'preflop',
        'actorId': 'folder',
        'action': 'fold',
        'timedOut': true,
        'board': <String>[],
        'pot': 30,
        'seats': [
          _seat(
            'me',
            990,
            streetBet: 10,
            totalBet: 10,
            lastAction: 'small_blind',
            holeCards: mine,
          ),
          _seat(
            'rival',
            980,
            streetBet: 20,
            totalBet: 20,
            lastAction: 'big_blind',
          ),
          _seat('folder', 1000, folded: true, lastAction: 'fold'),
        ],
      },
      {
        'index': 2,
        'kind': 'action',
        'street': 'preflop',
        'actorId': 'me',
        'action': 'call',
        'amount': 10,
        'board': <String>[],
        'pot': 40,
        'seats': [
          _seat(
            'me',
            980,
            streetBet: 20,
            totalBet: 20,
            lastAction: 'call',
            holeCards: mine,
          ),
          _seat(
            'rival',
            980,
            streetBet: 20,
            totalBet: 20,
            lastAction: 'big_blind',
          ),
          _seat('folder', 1000, folded: true, lastAction: 'fold'),
        ],
      },
      {
        'index': 3,
        'kind': 'action',
        'street': 'preflop',
        'actorId': 'rival',
        'action': 'check',
        'board': <String>[],
        'pot': 40,
        'seats': [
          _seat(
            'me',
            980,
            streetBet: 20,
            totalBet: 20,
            lastAction: 'call',
            holeCards: mine,
          ),
          _seat('rival', 980, streetBet: 20, totalBet: 20, lastAction: 'check'),
          _seat('folder', 1000, folded: true, lastAction: 'fold'),
        ],
      },
      {
        'index': 4,
        'kind': 'street',
        'street': 'flop',
        'board': board.sublist(0, 3),
        'pot': 40,
        'seats': [
          _seat('me', 980, totalBet: 20, holeCards: mine),
          _seat('rival', 980, totalBet: 20),
          _seat('folder', 1000, folded: true, lastAction: 'fold'),
        ],
      },
      {
        'index': 5,
        'kind': 'street',
        'street': 'turn',
        'board': board.sublist(0, 4),
        'pot': 40,
        'seats': [
          _seat('me', 980, totalBet: 20, holeCards: mine),
          _seat('rival', 980, totalBet: 20),
          _seat('folder', 1000, folded: true, lastAction: 'fold'),
        ],
      },
      {
        'index': 6,
        'kind': 'street',
        'street': 'river',
        'board': board,
        'pot': 40,
        'seats': [
          _seat('me', 980, totalBet: 20, holeCards: mine),
          _seat('rival', 980, totalBet: 20),
          _seat('folder', 1000, folded: true, lastAction: 'fold'),
        ],
      },
      {
        'index': 7,
        'kind': 'showdown',
        'street': 'showdown',
        'board': board,
        'pot': 40,
        'seats': [
          _seat('me', 980, totalBet: 20, holeCards: mine),
          _seat('rival', 980, totalBet: 20, holeCards: shown),
          _seat('folder', 1000, folded: true, lastAction: 'fold'),
        ],
      },
      {
        'index': 8,
        'kind': 'settle',
        'street': 'showdown',
        'board': board,
        'pot': 0,
        'seats': [
          _seat('me', 980, holeCards: mine),
          _seat('rival', 1020, holeCards: shown),
          _seat('folder', 1000, folded: true, lastAction: 'fold'),
        ],
        'awards': [
          {
            'potIndex': 0,
            'amount': 40,
            'winnerPlayerIds': ['rival'],
            'payouts': [
              {'playerId': 'rival', 'displayName': '对手', 'amount': 40},
            ],
          },
        ],
      },
    ],
  };
}

HandReplay _replay() => HandReplay.fromJson(_timelineJson());

Future<void> _pumpReplay(
  WidgetTester tester, {
  Size size = const Size(1280, 720),
}) async {
  tester.view.devicePixelRatio = 1;
  tester.view.physicalSize = size;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(
    MaterialApp(
      home: HandReplayPage(userId: 'me', loadReplay: () async => _replay()),
    ),
  );
  await tester.pumpAndSettle();
}

String _stepText(WidgetTester tester) =>
    tester.widget<Text>(find.byKey(const ValueKey('replay-step-text'))).data!;

void main() {
  group('回放数据', () {
    test('解析时间轴', () {
      final replay = _replay();
      expect(replay.steps, hasLength(9));
      expect(replay.player('me')!.isViewer, isTrue);
      expect(replay.steps[1].timedOut, isTrue);
      expect(replay.steps.last.awards.single.amount, 40);
    });

    test('每一步都有读得懂的中文说明', () {
      final replay = _replay();
      final labels = [
        for (final step in replay.steps) replayStepLabel(replay, step),
      ];
      expect(labels[0], '发牌。我（SB） 小盲，对手（BB） 大盲（10/20）');
      expect(labels[1], '弃牌者（BTN） 弃牌（超时自动）');
      expect(labels[2], '我（SB） 跟注至 20');
      expect(labels[3], '对手（BB） 过牌');
      expect(labels[4], '发出翻牌');
      expect(labels[7], '摊牌');
      expect(labels[8], '结算，我 -20');
    });

    test('对手的牌只在摊牌之后出现在座位上；没亮过的牌永远不出现', () {
      final replay = _replay();
      for (var index = 0; index < replay.steps.length; index++) {
        final seats = replaySeats(replay, index, 'me');
        final me = seats.firstWhere((seat) => seat.userId == 'me');
        final rival = seats.firstWhere((seat) => seat.userId == 'rival');
        final folder = seats.firstWhere((seat) => seat.userId == 'folder');
        expect(me.holeCards, ['As', 'Kd'], reason: '本人的牌每一帧都在');
        expect(folder.revealedCards, isEmpty);
        expect(folder.holeCards, isEmpty);
        final afterShowdown = index >= 7;
        expect(
          rival.revealedCards.isNotEmpty,
          afterShowdown,
          reason: 'step $index',
        );
        // 本人打到摊牌：摊牌之前不能提前按亮牌样式显示、透露最终牌型
        expect(
          me.revealedCards.isNotEmpty,
          afterShowdown,
          reason: 'step $index',
        );
        expect(
          me.handCategory.isNotEmpty,
          afterShowdown,
          reason: 'step $index',
        );
      }
    });

    test('分池结果只在最后一帧出现', () {
      final replay = _replay();
      for (var index = 0; index < replay.steps.length - 1; index++) {
        expect(
          replaySnapshot(replay, index).settlement?.potAwards ?? const [],
          isEmpty,
        );
      }
      final last = replaySnapshot(replay, replay.steps.length - 1);
      expect(last.settlement!.potAwards.single.payouts.single.userId, 'rival');
      expect(replaySnapshot(replay, 4).phase, 'FLOP');
      expect(replaySnapshot(replay, 0).totalPot, 30);
    });

    test('结算帧的底池是分出去的总额，阶段写「本手结算」而不是「摊牌」', () {
      final replay = _replay();
      final last = replaySnapshot(replay, replay.steps.length - 1);
      expect(last.totalPot, 40);
      expect(last.phase, 'SETTLEMENT');
      expect(phaseLabel(last.phase), '本手结算');
    });

    test('换街后带金额的动作不再显示，免得写成「跟注至 0」', () {
      final replay = _replay();
      // 第 4 帧（翻牌）：我与对手本街都没有投入
      final seats = replaySeats(replay, 4, 'me');
      expect(seats.firstWhere((seat) => seat.userId == 'me').lastAction, '');
      // 弃牌的人一直标着弃牌
      expect(
        seats.firstWhere((seat) => seat.userId == 'folder').lastAction,
        'fold',
      );
      // 本街还有投入时照常显示
      final preflop = replaySeats(replay, 2, 'me');
      final me = preflop.firstWhere((seat) => seat.userId == 'me');
      expect(actionLabel(me.lastAction, me.lastActionTo), '跟注至 20');
    });

    test('本街没有投入时不显示带金额的动作（旧服务端可能留下全下者的动作）', () {
      final json = _timelineJson();
      (json['steps'] as List).add({
        'index': 9,
        'kind': 'street',
        'street': 'turn',
        'board': const ['2c', '7d', '9h', '3s'],
        'pot': 40,
        'seats': [
          _seat(
            'me',
            0,
            totalBet: 20,
            lastAction: 'all_in',
            holeCards: const ['As', 'Kd'],
          ),
          _seat('rival', 980, totalBet: 20, lastAction: 'call'),
          _seat('folder', 1000, folded: true, lastAction: 'fold'),
        ],
      });
      final replay = HandReplay.fromJson(json);
      final seats = replaySeats(replay, 9, 'me');
      expect(seats.firstWhere((seat) => seat.userId == 'me').lastAction, '');
      expect(seats.firstWhere((seat) => seat.userId == 'rival').lastAction, '');
      expect(
        seats.firstWhere((seat) => seat.userId == 'folder').lastAction,
        'fold',
      );
      final snapshot = replaySnapshot(replay, 9);
      expect(
        snapshot.seats.firstWhere((seat) => seat.userId == 'rival').lastAction,
        '',
      );
    });

    test('盲注、下注、加注、全下、退回、发两次、抽水都有中文说明', () {
      expect(actionLabel('small_blind', 10), '小盲 10');
      expect(actionLabel('big_blind', 20), '大盲 20');
      final json = _timelineJson();
      json['rake'] = 3;
      final base = HandReplay.fromJson(json);
      ReplayStep step(Map<String, dynamic> fields) => ReplayStep.fromJson({
        'board': <String>[],
        'seats': [_seat('rival', 900, streetBet: 120, totalBet: 120)],
        ...fields,
      });
      expect(
        replayStepLabel(
          base,
          step({
            'kind': 'action',
            'actorId': 'rival',
            'action': 'bet',
            'amount': 120,
          }),
        ),
        '对手（BB） 下注至 120',
      );
      expect(
        replayStepLabel(
          base,
          step({
            'kind': 'action',
            'actorId': 'rival',
            'action': 'raise',
            'amount': 100,
          }),
        ),
        '对手（BB） 加注至 120',
      );
      expect(
        replayStepLabel(
          base,
          step({
            'kind': 'action',
            'actorId': 'rival',
            'action': 'all_in',
            'amount': 100,
          }),
        ),
        '对手（BB） 全下至 120',
      );
      expect(
        replayStepLabel(
          base,
          step({'kind': 'refund', 'actorId': 'rival', 'amount': 80}),
        ),
        '无人跟注，退回 对手（BB） 80',
      );
      expect(replayStepLabel(base, step({'kind': 'runout'})), '全下后发两次公共牌');
      expect(
        replayStepLabel(base, step({'kind': 'settle'})),
        '结算，我 -20（本手抽水 3）',
      );
    });

    test('服务端错误码翻译成中文', () {
      expect(
        replayErrorMessage(
          const GameApiException('replay_unavailable', statusCode: 422),
        ),
        '这手牌的记录不完整，无法回放',
      );
      expect(
        replayErrorMessage(
          const GameApiException('hand_not_found', statusCode: 404),
        ),
        '找不到这手牌',
      );
      // 不认识的错误码也不能把英文枚举露给玩家
      expect(
        replayErrorMessage(
          const GameApiException('internal_error', statusCode: 500),
        ),
        '读取回放失败，请稍后重试',
      );
      expect(
        replayErrorMessage(const FormatException('bad json')),
        '回放数据无法识别，请更新客户端后重试',
      );
    });
  });

  group('回放页', () {
    for (final size in const [
      Size(1280, 720),
      Size(740, 340),
      Size(900, 430),
    ]) {
      testWidgets(
        '逐步前进、后退与拖动，${size.width.toInt()}x${size.height.toInt()} 不溢出',
        (tester) async {
          await _pumpReplay(tester, size: size);
          expect(find.byType(TableCanvas), findsOneWidget);
          expect(_stepText(tester), startsWith('发牌'));
          await tester.tap(find.byKey(const ValueKey('replay-next')));
          await tester.pump();
          expect(_stepText(tester), contains('弃牌'));
          await tester.tap(find.byKey(const ValueKey('replay-previous')));
          await tester.pump();
          expect(_stepText(tester), startsWith('发牌'));
          // 拖到最后
          final slider = tester.widget<Slider>(
            find.byKey(const ValueKey('replay-slider')),
          );
          slider.onChanged!(8);
          await tester.pump();
          expect(_stepText(tester), startsWith('结算'));
          expect(find.textContaining('第 9 / 9 步'), findsOneWidget);
          expect(tester.takeException(), isNull);
        },
      );
    }

    testWidgets('手机横屏：不占标题栏，返回键在栏内，按钮不跟牌桌一起缩小', (tester) async {
      await _pumpReplay(tester, size: const Size(740, 340));
      expect(find.byType(AppBar), findsNothing);
      expect(find.byKey(const ValueKey('replay-back')), findsOneWidget);
      final play = tester.getSize(find.byKey(const ValueKey('replay-play')));
      expect(play.width, greaterThanOrEqualTo(40));
      expect(play.height, greaterThanOrEqualTo(40));
      final text = tester.renderObject<RenderParagraph>(
        find.byKey(const ValueKey('replay-step-text')),
      );
      expect(text.text.style?.fontSize ?? 14, greaterThanOrEqualTo(12));
      expect(tester.takeException(), isNull);
    });

    // 九人桌、长昵称，手机、小平板与桌面各尺寸：说明面板与控制面板都不能压住
    // 任何一个玩家框
    for (final size in const [
      Size(740, 340),
      Size(800, 360),
      Size(915, 412),
      Size(1024, 600),
      Size(1100, 640),
      Size(1280, 720),
      Size(480, 900),
    ]) {
      testWidgets('九人桌 ${size.width.toInt()}x${size.height.toInt()} 面板不压玩家框', (
        tester,
      ) async {
        tester.view.devicePixelRatio = 1;
        tester.view.physicalSize = size;
        addTearDown(tester.view.reset);
        await tester.pumpWidget(
          MaterialApp(
            home: HandReplayPage(
              userId: 'p1',
              loadReplay: () async => HandReplay.fromJson(_nineHandedJson()),
            ),
          ),
        );
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
        final panels = [
          for (final key in const [
            'replay-info-panel',
            'replay-controls-panel',
          ])
            tester.getRect(find.byKey(ValueKey(key))),
        ];
        final seats = find.byType(TableSeatCard).evaluate().toList();
        expect(seats, hasLength(9));
        for (final element in seats) {
          final seat = tester.getRect(find.byWidget(element.widget));
          for (final panel in panels) {
            expect(
              seat.intersect(panel).isEmpty ||
                  seat.intersect(panel).width <= 0 ||
                  seat.intersect(panel).height <= 0,
              isTrue,
              reason: 'seat $seat overlaps panel $panel',
            );
          }
        }
      });
    }

    testWidgets('播放会自动逐步前进，放到最后自己停下', (tester) async {
      await _pumpReplay(tester);
      await tester.tap(find.byKey(const ValueKey('replay-play')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 1250));
      expect(_stepText(tester), contains('弃牌'));
      await tester.pump(const Duration(seconds: 20));
      expect(_stepText(tester), startsWith('结算'));
      expect(find.byTooltip('播放'), findsOneWidget, reason: '放完之后回到可播放状态');
    });

    testWidgets('读取失败时说明原因并可重试', (tester) async {
      var attempts = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: HandReplayPage(
            userId: 'me',
            loadReplay: () async {
              attempts++;
              if (attempts == 1) {
                throw const GameApiException(
                  'replay_unavailable',
                  statusCode: 422,
                );
              }
              return _replay();
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('这手牌的记录不完整，无法回放'), findsOneWidget);
      await tester.tap(find.text('重试'));
      await tester.pumpAndSettle();
      expect(find.byType(TableCanvas), findsOneWidget);
    });
  });

  group('牌局记录', () {
    RecentHand hand(int index) => RecentHand(
      handId: 'hand_$index',
      roomCode: '123456',
      endedAt: DateTime.utc(2026, 9, 23, 12).subtract(Duration(minutes: index)),
      board: const [],
      showdown: false,
      players: const [
        RecentHandPlayer(
          userId: 'me',
          displayName: '我',
          seat: 1,
          startingStack: 1000,
          endingStack: 990,
          delta: -10,
          holeCards: ['As', 'Kd'],
        ),
        RecentHandPlayer(
          userId: 'rival',
          displayName: '对手',
          seat: 2,
          startingStack: 1000,
          endingStack: 1010,
          delta: 10,
          holeCards: [],
        ),
      ],
    );

    testWidgets('一页满了才出现「加载更早的牌局」，按上一页最后一手往前翻', (tester) async {
      final cursors = <String?>[];
      await tester.pumpWidget(
        MaterialApp(
          home: RecentHandsPage(
            userId: 'me',
            loadHands: ({before}) async {
              cursors.add(before);
              if (before == null) return [for (var i = 0; i < 20; i++) hand(i)];
              return [for (var i = 20; i < 25; i++) hand(i)];
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(
        find.byKey(const ValueKey('recent-hands-more')),
        400,
      );
      await tester.tap(find.byKey(const ValueKey('recent-hands-more')));
      await tester.pumpAndSettle();
      expect(cursors, [null, 'hand_19']);
      expect(
        find.byKey(const ValueKey('recent-hands-more')),
        findsNothing,
        reason: '不满一页说明到头了',
      );
    });

    testWidgets('展开一手后可以回放它', (tester) async {
      String? requested;
      await tester.pumpWidget(
        MaterialApp(
          home: RecentHandsPage(
            userId: 'me',
            loadHands: ({before}) async => [hand(0)],
            loadReplay: (handId) async {
              requested = handId;
              return _replay();
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byType(ExpansionTile).first);
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('replay-hand_0')));
      await tester.pumpAndSettle();
      expect(requested, 'hand_0');
      expect(find.byType(HandReplayPage), findsOneWidget);
    });

    testWidgets('没有回放能力时不显示回放入口', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: RecentHandsPage(
            userId: 'me',
            loadHands: ({before}) async => [hand(0)],
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byType(ExpansionTile).first);
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('replay-hand_0')), findsNothing);
    });
  });
}

/// 九人桌、长昵称的一帧，专门用来检查布局。
Map<String, dynamic> _nineHandedJson() => {
  'handId': 'hand_9',
  'roomCode': '999999',
  'endedAt': '2026-09-23T12:01:00Z',
  'smallBlind': 10,
  'bigBlind': 20,
  'dealerSeat': 9,
  'smallBlindSeat': 1,
  'bigBlindSeat': 2,
  'players': [
    for (var seat = 1; seat <= 9; seat++)
      {
        'userId': 'p$seat',
        'displayName': '一位名字非常非常长的玩家$seat号座位',
        'seat': seat,
        'position': seat == 9 ? 'BTN' : 'UTG',
        'startingStack': 2000,
        'endingStack': 2000,
        'isViewer': seat == 1,
      },
  ],
  'steps': [
    {
      'index': 0,
      'kind': 'blinds',
      'street': 'preflop',
      'board': <String>[],
      'pot': 30,
      'seats': [
        for (var seat = 1; seat <= 9; seat++)
          _seat(
            'p$seat',
            1990,
            streetBet: seat == 1 ? 10 : (seat == 2 ? 20 : 0),
            lastAction: seat == 1
                ? 'small_blind'
                : (seat == 2 ? 'big_blind' : ''),
            holeCards: seat == 1 ? const ['As', 'Kd'] : const [],
          ),
      ],
    },
  ],
};

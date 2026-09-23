import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/features/history/domain/hand_replay.dart';
import 'package:poker_client/features/history/domain/hand_review.dart';
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
      // 开通复盘后控制面板多一个按钮、说明面板多一段点评，同样不能压住玩家框
      for (final withReview in const [false, true]) {
        testWidgets('九人桌 ${size.width.toInt()}x${size.height.toInt()}'
            '${withReview ? '（带复盘）' : ''} 面板不压玩家框', (tester) async {
          tester.view.devicePixelRatio = 1;
          tester.view.physicalSize = size;
          addTearDown(tester.view.reset);
          await tester.pumpWidget(
            MaterialApp(
              home: HandReplayPage(
                userId: 'p1',
                loadReplay: () async {
                  final json = _nineHandedJson();
                  if (withReview) {
                    // 复盘入口只给本人行动过的手：补一步本人的过牌
                    final steps = json['steps'] as List<dynamic>;
                    json['steps'] = <dynamic>[
                      ...steps,
                      <String, dynamic>{
                        ...(steps.first as Map<String, dynamic>),
                        'index': 1,
                        'kind': 'action',
                        'actorId': 'p1',
                        'action': 'check',
                      },
                    ];
                  }
                  return HandReplay.fromJson(json);
                },
                loadReviewApi: withReview
                    ? () async => HandReviewApi(
                        request: (_) async => throw StateError('unused'),
                        load: (handId) async => HandReview.fromJson({
                          'handId': handId,
                          'status': 'done',
                          'result': {
                            'summary': '总评',
                            'decisions': [
                              {
                                'step': 0,
                                'verdict': '有争议',
                                'reasoning': '很长的点评' * 30,
                              },
                            ],
                          },
                        }),
                      )
                    : null,
              ),
            ),
          );
          await tester.pumpAndSettle();
          expect(tester.takeException(), isNull);
          if (withReview) {
            expect(find.byKey(const ValueKey('replay-review')), findsOneWidget);
            expect(
              find.byKey(const ValueKey('replay-step-review')),
              findsOneWidget,
            );
            final review = tester.getSize(
              find.byKey(const ValueKey('replay-review')),
            );
            expect(review.height, greaterThanOrEqualTo(32), reason: '按钮要点得中');
          }
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

  group('AI 复盘', () {
    const doneJson = {
      'handId': 'hand_1',
      'status': 'done',
      'model': 'deepseek-reasoner',
      'result': {
        'summary': '整体偏被动',
        'decisions': [
          {
            'step': 2,
            'verdict': '失误',
            'reasoning': 'AKo 在小盲只跟注太被动',
            'bestAction': '加注到 60',
            'equityVsRangePercent': 58.4,
            'evTakenBB': 0.4,
            'evBestBB': 1.2,
            'facts': {
              'potBefore': 30,
              'toCall': 10,
              'potOddsPercent': 25,
              'stackToPotRatio': 33,
              'equityVsRandomHandsPercent': 64.2,
            },
          },
        ],
        'keyLessons': ['小盲拿强牌要主动'],
        'opponentNotes': ['BB 翻前很少 3bet'],
        'hindsight': '对手摊牌亮出一对 Q',
      },
    };

    Future<void> pumpWithReview(WidgetTester tester, HandReviewApi? api) async {
      tester.view.devicePixelRatio = 1;
      tester.view.physicalSize = const Size(1280, 720);
      addTearDown(tester.view.reset);
      await tester.pumpWidget(
        MaterialApp(
          home: HandReplayPage(
            userId: 'me',
            loadReplay: () async => _replay(),
            loadReviewApi: () async => api,
          ),
        ),
      );
      await tester.pumpAndSettle();
    }

    testWidgets('没开通时不显示复盘入口', (tester) async {
      await pumpWithReview(tester, null);
      expect(find.byKey(const ValueKey('replay-review')), findsNothing);
      await _pumpReplay(tester);
      expect(find.byKey(const ValueKey('replay-review')), findsNothing);
    });

    testWidgets('发起后排队、轮询出结果，点一条点评跳到那一步', (tester) async {
      var requests = 0;
      var loads = 0;
      final api = HandReviewApi(
        request: (handId) async {
          requests++;
          return HandReview.fromJson({'handId': handId, 'status': 'queued'});
        },
        load: (handId) async {
          loads++;
          // 第一次是打开回放时问有没有旧结果；之后是轮询
          if (loads == 1) return null;
          if (loads == 2) {
            return HandReview.fromJson({'handId': handId, 'status': 'running'});
          }
          return HandReview.fromJson(doneJson);
        },
      );
      await pumpWithReview(tester, api);
      expect(find.text('AI 复盘'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      expect(requests, 1);
      expect(find.textContaining('正在分析'), findsOneWidget);
      await tester.pump(const Duration(seconds: 3));
      expect(find.textContaining('正在分析'), findsOneWidget, reason: '还在分析');
      await tester.pump(const Duration(seconds: 3));
      await tester.pump();
      expect(find.text('整体偏被动'), findsOneWidget);
      expect(find.text('最佳行动：加注到 60'), findsOneWidget);
      // 精确数字由服务端给出，估算明确标成 AI 估算；不再单独列牌局概况
      expect(find.byKey(const ValueKey('review-situation')), findsNothing);
      expect(find.textContaining('需跟注 10，至少要 25.0% 胜率'), findsOneWidget);
      expect(
        tester
            .widget<Text>(find.byKey(const ValueKey('review-estimate-2')))
            .data,
        'AI 估算：对对手范围胜率约 58% · 本次 EV +0.4 BB · 最佳 EV +1.2 BB（多 +0.8 BB）',
      );
      expect(find.text('对手摊牌亮出一对 Q'), findsOneWidget);
      final loadsWhenDone = loads;
      await tester.pump(const Duration(seconds: 10));
      expect(loads, loadsWhenDone, reason: '出结果后停止轮询');

      await tester.tap(find.byKey(const ValueKey('review-decision-2')));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('review-panel')), findsNothing);
      expect(_stepText(tester), contains('跟注'));
      expect(find.text('AI：失误'), findsOneWidget, reason: '当前一步显示点评结论');
      expect(find.text('查看 AI 复盘'), findsOneWidget);

      // 再点入口只打开结果，不重复发起
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pumpAndSettle();
      expect(requests, 1);
      expect(find.text('整体偏被动'), findsOneWidget);
    });

    testWidgets('打开回放时已有结果：直接可看，其他步骤不显示点评', (tester) async {
      final api = HandReviewApi(
        request: (_) async => throw StateError('不该重新发起'),
        load: (_) async => HandReview.fromJson(doneJson),
      );
      await pumpWithReview(tester, api);
      expect(find.text('查看 AI 复盘'), findsOneWidget);
      expect(find.byKey(const ValueKey('replay-step-review')), findsNothing);
      await tester.tap(find.byKey(const ValueKey('replay-next')));
      await tester.tap(find.byKey(const ValueKey('replay-next')));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('replay-step-review')), findsOneWidget);
      // 点摘要打开完整复盘，关闭只关抽屉、不退出回放
      await tester.tap(find.byKey(const ValueKey('replay-step-review')));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('review-panel')), findsOneWidget);
      await tester.tap(find.byTooltip('关闭'));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('review-panel')), findsNothing);
      expect(find.byType(TableCanvas), findsOneWidget);
    });

    testWidgets('额度用完时说明原因；分析失败可以重新分析', (tester) async {
      var requests = 0;
      final api = HandReviewApi(
        request: (handId) async {
          requests++;
          if (requests == 1) {
            throw const GameApiException('review_daily_limit', statusCode: 429);
          }
          return HandReview.fromJson(doneJson);
        },
        load: (handId) async => HandReview.fromJson({
          'handId': handId,
          'status': 'failed',
          'failure': 'model_error',
        }),
      );
      await pumpWithReview(tester, api);
      expect(find.text('AI 复盘（重试）'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pumpAndSettle();
      expect(requests, 1);
      expect(find.text(reviewErrorLabel('review_daily_limit')), findsOneWidget);
      await tester.tap(find.text('重试'));
      await tester.pumpAndSettle();
      expect(requests, 2);
      expect(find.text('整体偏被动'), findsOneWidget);
    });

    testWidgets('本人没做过决定的手不显示复盘入口', (tester) async {
      tester.view.devicePixelRatio = 1;
      tester.view.physicalSize = const Size(1280, 720);
      addTearDown(tester.view.reset);
      var asked = false;
      await tester.pumpWidget(
        MaterialApp(
          home: HandReplayPage(
            // 「弃牌者」只有一个弃牌动作；换一个完全没行动过的人来看
            userId: 'nobody',
            loadReplay: () async => _replay(),
            loadReviewApi: () async {
              asked = true;
              return HandReviewApi(
                request: (_) async => throw StateError('unused'),
                load: (_) async => null,
              );
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('replay-review')), findsNothing);
      expect(asked, isFalse);
    });

    testWidgets('分析中被收回权限：停止轮询并说明原因，AppBar 不多出菜单键', (tester) async {
      var loads = 0;
      final api = HandReviewApi(
        request: (handId) async =>
            HandReview.fromJson({'handId': handId, 'status': 'queued'}),
        load: (handId) async {
          loads++;
          if (loads == 1) return null;
          throw const GameApiException('review_not_allowed', statusCode: 403);
        },
      );
      await pumpWithReview(tester, api);
      expect(find.byType(EndDrawerButton), findsNothing);
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pump();
      await tester.pump(const Duration(seconds: 3));
      await tester.pump();
      expect(find.text(reviewErrorLabel('review_not_allowed')), findsOneWidget);
      final loadsAfterError = loads;
      await tester.pump(const Duration(seconds: 12));
      expect(loads, loadsAfterError, reason: '服务端明确拒绝后不再轮询');
    });

    testWidgets('部署重启时的 502 不停止轮询，恢复后照常出结果', (tester) async {
      var loads = 0;
      final api = HandReviewApi(
        request: (handId) async =>
            HandReview.fromJson({'handId': handId, 'status': 'queued'}),
        load: (handId) async {
          loads++;
          if (loads == 1) return null;
          if (loads == 2) {
            // 反代的 502 是 HTML 页，客户端解析不了，记为状态码 0
            throw const GameApiException(
              'invalid_server_response',
              statusCode: 0,
            );
          }
          if (loads == 3) {
            throw const GameApiException('bad_gateway', statusCode: 502);
          }
          return HandReview.fromJson(doneJson);
        },
      );
      await pumpWithReview(tester, api);
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pump();
      await tester.pump(const Duration(seconds: 3));
      await tester.pump();
      expect(find.textContaining('正在分析'), findsOneWidget);
      await tester.pump(const Duration(seconds: 3));
      await tester.pump();
      expect(find.textContaining('正在分析'), findsOneWidget);
      await tester.pump(const Duration(seconds: 3));
      await tester.pump();
      expect(find.text('整体偏被动'), findsOneWidget);
    });

    testWidgets('打开时的查询比发起还晚返回：不能把排队中盖回没分析', (tester) async {
      final initial = Completer<HandReview?>();
      var loads = 0;
      final api = HandReviewApi(
        request: (handId) async =>
            HandReview.fromJson({'handId': handId, 'status': 'queued'}),
        load: (handId) {
          loads++;
          if (loads == 1) return initial.future;
          return Future.value(
            HandReview.fromJson({'handId': handId, 'status': 'running'}),
          );
        },
      );
      await pumpWithReview(tester, api);
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      initial.complete(null);
      await tester.pump();
      expect(find.text('AI 分析中…'), findsOneWidget);
      await tester.pump(const Duration(seconds: 3));
    });

    testWidgets('停在本人操作过的步上打开面板：滚到这一步的点评并高亮', (tester) async {
      final api = HandReviewApi(
        request: (_) async => throw StateError('不该重新发起'),
        load: (_) async => HandReview.fromJson(doneJson),
      );
      await pumpWithReview(tester, api);
      // 第 0 步不是本人的决策：从头看，不高亮
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('review-focused')), findsNothing);
      await tester.tap(find.byTooltip('关闭'));
      await tester.pumpAndSettle();
      // 走到第 2 步（本人跟注）再打开：这一步的卡片被高亮
      await tester.tap(find.byKey(const ValueKey('replay-next')));
      await tester.tap(find.byKey(const ValueKey('replay-next')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('review-focused')), findsOneWidget);
      final card = tester.getRect(
        find.byKey(const ValueKey('review-decision-2')),
      );
      final panel = tester.getRect(find.byKey(const ValueKey('review-panel')));
      expect(card.top, greaterThanOrEqualTo(panel.top));
      expect(card.top, lessThan(panel.bottom), reason: '卡片要滚到看得见的地方');
    });

    testWidgets('只有旧版结果：先显示旧版并可用新版重新分析', (tester) async {
      var requests = 0;
      final api = HandReviewApi(
        request: (handId) async {
          requests++;
          return HandReview.fromJson({'handId': handId, 'status': 'queued'});
        },
        load: (handId) async =>
            HandReview.fromJson({...doneJson, 'outdated': true}),
      );
      await pumpWithReview(tester, api);
      expect(find.text('查看 AI 复盘'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pumpAndSettle();
      expect(requests, 0, reason: '有旧版结果时点按钮只是查看');
      expect(find.byKey(const ValueKey('review-outdated')), findsOneWidget);
      expect(find.text('整体偏被动'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('review-reanalyse')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      expect(requests, 1);
      expect(find.textContaining('正在分析'), findsOneWidget);
    });

    testWidgets('大模型服务繁忙、正在自动重试时说明重试次数', (tester) async {
      final api = HandReviewApi(
        request: (_) async => throw StateError('unused'),
        load: (handId) async => HandReview.fromJson({
          'handId': handId,
          'status': 'queued',
          'attempts': 2,
        }),
      );
      await pumpWithReview(tester, api);
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      expect(
        tester.widget<Text>(find.byKey(const ValueKey('review-progress'))).data,
        contains('正在自动重试（第 2 次）'),
      );
    });

    testWidgets('用新版重新分析被拒：旧结果照样显示，重试会再次发起', (tester) async {
      var requests = 0;
      final api = HandReviewApi(
        request: (handId) async {
          requests++;
          if (requests == 1) {
            throw const GameApiException('review_daily_limit', statusCode: 429);
          }
          return HandReview.fromJson({'handId': handId, 'status': 'queued'});
        },
        load: (handId) async =>
            HandReview.fromJson({...doneJson, 'outdated': true}),
      );
      await pumpWithReview(tester, api);
      await tester.tap(find.byKey(const ValueKey('replay-review')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('review-reanalyse')));
      await tester.pumpAndSettle();
      expect(requests, 1);
      expect(find.text(reviewErrorLabel('review_daily_limit')), findsOneWidget);
      expect(find.text('整体偏被动'), findsOneWidget, reason: '旧结果不能被错误盖掉');
      await tester.tap(find.byKey(const ValueKey('review-error-retry')));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      expect(requests, 2, reason: '重试要真的再发起一次');
    });

    testWidgets('新版分析中或失败时，下面接着显示旧版结果', (tester) async {
      final previous = doneJson['result'];
      for (final status in ['running', 'failed']) {
        final api = HandReviewApi(
          request: (_) async => throw StateError('unused'),
          load: (handId) async => HandReview.fromJson({
            'handId': handId,
            'status': status,
            'failure': status == 'failed' ? 'model_busy' : '',
            'previous': previous,
          }),
        );
        await pumpWithReview(tester, api);
        await tester.tap(find.byKey(const ValueKey('replay-review')));
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 400));
        expect(find.byKey(const ValueKey('review-previous')), findsOneWidget);
        expect(find.text('整体偏被动'), findsOneWidget, reason: status);
        await tester.pumpWidget(const SizedBox());
      }
    });

    test('失败原因码都有中文说明', () {
      for (final code in [
        'review_unavailable',
        'review_not_allowed',
        'review_daily_limit',
        'review_budget_exhausted',
        'model_error',
        'invalid_output',
        'replay_unavailable',
        'history_unavailable',
        'hand_not_found',
        'review_no_decisions',
        'internal_error',
        'output_truncated',
        'review_in_flight_limit',
        'model_busy',
        'model_timeout',
        'model_insufficient_balance',
        'model_unauthorized',
      ]) {
        expect(reviewErrorLabel(code), isNot('复盘失败，请稍后重试'), reason: code);
      }
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

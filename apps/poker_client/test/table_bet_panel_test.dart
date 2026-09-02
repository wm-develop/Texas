import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/table/presentation/table_action_bar.dart';

/// 构造一份轮到自己行动的快照。
/// `stack` 与 `streetBet` 决定全下额度；`maxRaiseTo` 模拟服务端向下取整的上界。
String _turnSnapshot({
  required bool canCheck,
  int toCall = 0,
  int minRaiseTo = 40,
  int maxRaiseTo = 260,
  int stack = 265,
  int streetBet = 0,
  bool canBet = false,
  bool canRaise = true,
  bool canAllIn = true,
  List<Map<String, Object>> suggestions = const [],
}) => jsonEncode({
  'version': 1,
  'type': 'table.snapshot',
  'tableId': 'room_1',
  'sequence': 1,
  'payload': {
    'roomId': 'room_1',
    'roomCode': '123456',
    'tableRevision': 3,
    'phase': 'FLOP',
    'handId': 'hand_1',
    'maxBuyIn': 2000,
    'seats': [
      {
        'userId': 'me',
        'displayName': '我',
        'seat': 1,
        'stack': stack,
        'ready': true,
        'connected': true,
        'participating': true,
        'folded': false,
        'allIn': false,
        'streetBet': streetBet,
        'totalBet': streetBet,
        'position': 'BTN',
        'lastAction': '',
        'lastCommitted': 0,
        'lastActionTo': 0,
        'timeExtensions': 2,
      },
    ],
    'currentAction': {
      'userId': 'me',
      'seat': 1,
      'options': {
        'toCall': toCall,
        'canFold': true,
        'canCheck': canCheck,
        'canCall': !canCheck && toCall > 0,
        'canBet': canBet,
        'canRaise': canRaise,
        'canAllIn': canAllIn,
        'minRaiseTo': minRaiseTo,
        'maxRaiseTo': maxRaiseTo,
      },
      'suggestions': suggestions,
    },
  },
});

Future<GameSocketClient> _pumpPanel(
  WidgetTester tester,
  String snapshotMessage,
) async {
  final client = GameSocketClient(
    accessTokenProvider: ({bool forceRefresh = false}) async => 'token',
    roomId: 'room_1',
    userId: 'me',
  );
  client.debugHandleMessage(
    jsonEncode({
      'version': 1,
      'type': 'table.joined',
      'payload': {'roomId': 'room_1'},
    }),
  );
  client.debugHandleMessage(snapshotMessage);
  await tester.pumpWidget(
    MaterialApp(
      home: Scaffold(
        body: SizedBox(
          width: 470,
          child: TableActionBar(
            client: client,
            userId: 'me',
            smallBlind: 10,
            onRebuy: () {},
          ),
        ),
      ),
    ),
  );
  return client;
}

void main() {
  testWidgets('能过牌时是 弃牌 / 过牌 / 下注 三个按钮', (tester) async {
    await _pumpPanel(
      tester,
      _turnSnapshot(canCheck: true, canBet: true, canRaise: false),
    );

    expect(find.text('弃牌'), findsOneWidget);
    expect(find.text('过牌'), findsOneWidget);
    expect(find.text('下注 40'), findsOneWidget);
    // 能过牌时不存在跟注，也不该出现独立的全下按钮
    expect(find.textContaining('跟注'), findsNothing);
    expect(find.text('全下'), findsNothing);
  });

  testWidgets('不能过牌时是 弃牌 / 跟注 / 加注 三个按钮', (tester) async {
    await _pumpPanel(tester, _turnSnapshot(canCheck: false, toCall: 20));

    expect(find.text('弃牌'), findsOneWidget);
    expect(find.text('跟注 20'), findsOneWidget);
    expect(find.text('加注 40'), findsOneWidget);
    expect(find.text('过牌'), findsNothing);
  });

  testWidgets('滑块推到最右变成全下，且用的是真实全下额度而非取整上界', (tester) async {
    await _pumpPanel(tester, _turnSnapshot(canCheck: false, toCall: 20));

    // 服务端 maxRaiseTo = 260（向下取整），真实全下 = streetBet 0 + stack 265
    expect(find.text('加注 40'), findsOneWidget);
    await tester.drag(
      find.byKey(const ValueKey('bet-amount-slider')),
      const Offset(600, 0),
    );
    await tester.pump();

    expect(find.text('All in 265'), findsOneWidget);
    expect(find.text('加注 260'), findsNothing);
  });

  testWidgets('点预设档只改额度，不直接提交动作', (tester) async {
    final client = await _pumpPanel(
      tester,
      _turnSnapshot(
        canCheck: false,
        toCall: 20,
        suggestions: [
          {'label': 'half_pot', 'action': 'raise', 'raiseTo': 120},
        ],
      ),
    );

    expect(find.text('加注 40'), findsOneWidget);
    await tester.tap(find.byKey(const ValueKey('bet-preset-half_pot-120')));
    await tester.pump();

    expect(find.text('加注 120'), findsOneWidget);
    expect(
      client.actionPending,
      isFalse,
      reason: '改额度是第一步，必须再点加注按钮才提交',
    );
  });

  testWidgets('只能全下时不渲染滑块，第三个按钮直接是全下', (tester) async {
    await _pumpPanel(
      tester,
      _turnSnapshot(
        canCheck: false,
        toCall: 20,
        canRaise: false,
        canBet: false,
        stack: 55,
        maxRaiseTo: 50,
      ),
    );

    expect(find.text('All in 55'), findsOneWidget);
    expect(find.byKey(const ValueKey('bet-amount-slider')), findsNothing);
  });

  testWidgets('步进按钮从最大常规额度跨到全下', (tester) async {
    await _pumpPanel(tester, _turnSnapshot(canCheck: false, toCall: 20));

    await tester.tap(find.byKey(const ValueKey('bet-amount-value')));
    await tester.pumpAndSettle();
    // 直接输入超过全下的数值同样落到全下
    await tester.enterText(find.byType(TextField), '9999');
    await tester.tap(find.text('确定'));
    await tester.pumpAndSettle();

    expect(find.text('All in 265'), findsOneWidget);
  });
  testWidgets('手机右栏宽度下竖排下注区不溢出', (tester) async {
    // 紧凑右栏可用宽度 = compactRightRailWidth(216) - 16 = 200
    final client = GameSocketClient(
      accessTokenProvider: ({bool forceRefresh = false}) async => 'token',
      roomId: 'room_1',
      userId: 'me',
    );
    client.debugHandleMessage(
      jsonEncode({
        'version': 1,
        'type': 'table.joined',
        'payload': {'roomId': 'room_1'},
      }),
    );
    client.debugHandleMessage(
      _turnSnapshot(
        canCheck: false,
        toCall: 20,
        suggestions: const [
          {'label': 'half_pot', 'action': 'raise', 'raiseTo': 120},
          {'label': 'pot', 'action': 'raise', 'raiseTo': 200},
          {'label': 'all_in', 'action': 'all_in', 'raiseTo': 265},
        ],
      ),
    );
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Align(
            alignment: Alignment.centerRight,
            child: SizedBox(
              width: 200,
              child: TableActionBar(
                client: client,
                userId: 'me',
                smallBlind: 10,
                onRebuy: () {},
                vertical: true,
              ),
            ),
          ),
        ),
      ),
    );

    expect(tester.takeException(), isNull);
    expect(find.text('弃牌'), findsOneWidget);
    expect(find.text('跟注 20'), findsOneWidget);
    expect(find.text('加注 40'), findsOneWidget);
  });
}

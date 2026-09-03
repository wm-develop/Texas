import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/table/presentation/table_action_bar.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/table/presentation/table_bet_panel.dart';
import 'package:poker_client/features/table/presentation/table_status_widgets.dart';

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

/// 只建客户端并喂进快照，不铺界面：右栏的测试要自己搭布局。
GameSocketClient _clientWith(String snapshotMessage) {
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
  return client;
}

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

FriendRoom _room() => const FriendRoom(
  roomId: 'room_1',
  code: '123456',
  ownerUserId: 'me',
  preset: 'standard',
  rules: RoomRules(
    startingChips: 2000,
    smallBlind: 10,
    bigBlind: 20,
    actionSeconds: 30,
    maxBuyIn: 2000,
  ),
  maxPlayers: 10,
  members: [],
  revision: 1,
);

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
  group('按钮配色', () {
    test('三个可用态都明显亮于面板底色，不会被当成禁用键', () {
      final background = TableActionPalette.panelBackground.computeLuminance();
      for (final tone in TableActionPalette.enabledTones) {
        expect(
          tone.computeLuminance(),
          greaterThan(background + 0.08),
          reason: '$tone 与面板底色对比不足，看起来像不可点击',
        );
      }
    });

    test('禁用态与每个可用态都拉开距离', () {
      final disabled = TableActionPalette.disabledBackground.computeLuminance();
      for (final tone in TableActionPalette.enabledTones) {
        expect(
          (tone.computeLuminance() - disabled).abs(),
          greaterThan(0.05),
          reason: '$tone 与禁用色太接近，无法一眼分辨能不能点',
        );
      }
    });
  });
  testWidgets('发牌演出期间不接受行动输入', (tester) async {
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
    client.debugHandleMessage(_turnSnapshot(canCheck: false, toCall: 20));
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
              blocked: true,
            ),
          ),
        ),
      ),
    );

    // 牌还没翻开时不能下决定，三个按钮都必须是禁用态
    for (final key in const [
      ValueKey('bet-fold-action'),
      ValueKey('bet-check-call-action'),
      ValueKey('bet-aggressive-action'),
    ]) {
      // key 挂在私有包装组件上，真正的按钮是它的后代
      final button = tester.widget<FilledButton>(
        find.descendant(
          of: find.byKey(key),
          matching: find.byType(FilledButton),
        ),
      );
      expect(button.onPressed, isNull, reason: '$key 在发牌演出期间应当禁用');
    }
  });
  group('右栏空间不足时不裁掉按钮', () {
    /// 复刻牌桌右栏：定高，顶部有房间信息占位，下注区贴底。
    Future<void> pumpRail(
      WidgetTester tester,
      GameSocketClient client, {
      required double height,
    }) => tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Align(
            alignment: Alignment.centerRight,
            child: SizedBox(
              width: 200,
              height: height,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  const SizedBox(height: 120, child: Placeholder()),
                  Expanded(
                    child: SingleChildScrollView(
                      reverse: true,
                      child: TableActionBar(
                        client: client,
                        userId: 'me',
                        smallBlind: 10,
                        onRebuy: () {},
                        vertical: true,
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

    testWidgets('平板横屏这类矮右栏不溢出，主操作仍贴在底部', (tester) async {
      // 此前多出来的部分被直接裁掉：最下面的按钮看不见也点不到
      final client = _clientWith(
        _turnSnapshot(
          canCheck: false,
          toCall: 20,
          suggestions: const [
            {'label': 'quarter_pot', 'action': 'raise', 'raiseTo': 60},
            {'label': 'half_pot', 'action': 'raise', 'raiseTo': 120},
            {'label': 'pot', 'action': 'raise', 'raiseTo': 200},
            {'label': 'all_in', 'action': 'all_in', 'raiseTo': 265},
          ],
        ),
      );
      await pumpRail(tester, client, height: 360);

      expect(tester.takeException(), isNull, reason: '不能再出现溢出');

      // 三个大按钮都在，且完整落在右栏范围内
      final rail = tester.getRect(find.byType(SingleChildScrollView).first);
      for (final label in const ['弃牌', '跟注 20', '加注 40']) {
        final button = find.text(label);
        expect(button, findsOneWidget, reason: label);
        final box = tester.getRect(button);
        expect(
          box.bottom,
          lessThanOrEqualTo(rail.bottom + 0.5),
          reason: '$label 不能落到右栏之外',
        );
      }
      client.dispose();
    });

    testWidgets('空间充足时行为与原来一致：贴底', (tester) async {
      final client = _clientWith(
        _turnSnapshot(canCheck: true, canBet: true, canRaise: false),
      );
      await pumpRail(tester, client, height: 900);

      expect(tester.takeException(), isNull);
      final rail = tester.getRect(find.byType(SingleChildScrollView).first);
      final lastButton = tester.getRect(find.text('下注 40'));
      expect(
        rail.bottom - lastButton.bottom,
        lessThan(60),
        reason: '主操作应仍然贴在底部，便于够到',
      );
      client.dispose();
    });
  });
  group('文字聊天入口', () {
    testWidgets('聊天做成房间信息栏里的图标，把右栏竖向空间让给下注区', (tester) async {
      // 平板横屏右栏高度紧张，整条「文字聊天」按钮占掉的那一行更值钱
      var toggled = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SizedBox(
              width: 220,
              child: TableRoomHeader(
                room: _room(),
                currentPlayers: 2,
                compact: true,
                onLeave: () async {},
                onSettings: () {},
                onShowResult: () {},
                onToggleChat: () => toggled++,
                unreadChatCount: 3,
              ),
            ),
          ),
        ),
      );

      expect(tester.takeException(), isNull);
      // 不再有占一整行的文字按钮
      expect(find.text('文字聊天'), findsNothing);
      final button = find.byKey(const ValueKey('chat-toggle-button'));
      expect(button, findsOneWidget);
      // 未读数仍然看得见
      expect(find.text('3'), findsOneWidget);

      await tester.tap(button);
      expect(toggled, 1);
    });
  });
}

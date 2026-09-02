import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/table/presentation/table_action_bar.dart';
import 'package:poker_client/features/table/presentation/table_status_widgets.dart';

GameSocketClient _client() => GameSocketClient(
  accessTokenProvider: ({bool forceRefresh = false}) async => 'token',
  roomId: 'room_1',
  userId: 'me',
);

String _envelope(String type, Map<String, dynamic> payload, {int sequence = 0}) =>
    jsonEncode({
      'version': 1,
      'type': type,
      'tableId': 'room_1',
      if (sequence > 0) 'sequence': sequence,
      'payload': payload,
    });

Map<String, dynamic> _snapshot({
  String handId = '',
  bool settled = false,
  bool draining = false,
  bool ready = false,
}) => {
  'roomId': 'room_1',
  'roomCode': '123456',
  'tableRevision': 1,
  'phase': handId.isEmpty ? 'WAITING' : 'FLOP',
  'handId': handId,
  'seats': [
    {
      'userId': 'me',
      'displayName': '我',
      'seat': 1,
      'stack': 1000,
      'ready': ready,
      'connected': true,
      'participating': true,
      'folded': false,
      'allIn': false,
      'streetBet': 0,
      'totalBet': 0,
      'position': '',
      'lastAction': '',
      'lastCommitted': 0,
      'lastActionTo': 0,
      'timeExtensions': 0,
    },
  ],
  'draining': draining,
  if (settled)
    'settlement': {
      'handId': handId,
      'showdown': false,
      'revealedHands': <dynamic>[],
      'potAwards': <dynamic>[],
    },
};

void _authenticated(GameSocketClient client, String instance) =>
    client.debugHandleMessage(
      _envelope('session.authenticated', {'serverInstanceId': instance}),
    );

void main() {
  group('服务端重启识别', () {
    test('进程标识变化且上一手未结算时记录作废的手', () {
      final client = _client();
      _authenticated(client, 'inst_a');
      client.debugHandleMessage(
        _envelope('table.snapshot', _snapshot(handId: 'hand_7'), sequence: 1),
      );
      expect(client.takeVoidedHandId(), isNull);

      _authenticated(client, 'inst_b');
      expect(client.serverInstanceId, 'inst_b');
      expect(client.takeVoidedHandId(), 'hand_7');
      // 只提示一次
      expect(client.takeVoidedHandId(), isNull);
    });

    test('同一进程重连、手已结算或没有手时不提示', () {
      final same = _client();
      _authenticated(same, 'inst_a');
      same.debugHandleMessage(
        _envelope('table.snapshot', _snapshot(handId: 'hand_1'), sequence: 1),
      );
      _authenticated(same, 'inst_a');
      expect(same.takeVoidedHandId(), isNull);

      final settled = _client();
      _authenticated(settled, 'inst_a');
      settled.debugHandleMessage(
        _envelope(
          'table.snapshot',
          _snapshot(handId: 'hand_1', settled: true),
          sequence: 1,
        ),
      );
      _authenticated(settled, 'inst_b');
      expect(settled.takeVoidedHandId(), isNull);

      final idle = _client();
      _authenticated(idle, 'inst_a');
      idle.debugHandleMessage(
        _envelope('table.snapshot', _snapshot(), sequence: 1),
      );
      _authenticated(idle, 'inst_b');
      expect(idle.takeVoidedHandId(), isNull);

      // 首次连接没有「上一次」可比较，即使快照在手中也不提示
      final fresh = _client();
      fresh.debugHandleMessage(
        _envelope('table.snapshot', _snapshot(handId: 'hand_9'), sequence: 1),
      );
      _authenticated(fresh, 'inst_a');
      expect(fresh.takeVoidedHandId(), isNull);
    });
  });

  group('停机排空提示', () {
    testWidgets('状态条说明原因，准备按钮禁用', (tester) async {
      final client = _client();
      _authenticated(client, 'inst_a');
      client.debugHandleMessage(_envelope('table.joined', {'roomId': 'room_1'}));
      client.debugHandleMessage(
        _envelope('table.snapshot', _snapshot(draining: true), sequence: 1),
      );
      expect(client.snapshot?.draining, isTrue);

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Column(
              children: [
                TableConnectionStatusBar(client: client),
                TableActionBar(
                  client: client,
                  userId: 'me',
                  smallBlind: 10,
                  onRebuy: () {},
                ),
              ],
            ),
          ),
        ),
      );
      expect(find.text('服务器即将更新，本手结束后暂停'), findsOneWidget);
      final ready = tester.widget<FilledButton>(
        find.widgetWithText(FilledButton, '准备开始'),
      );
      expect(ready.onPressed, isNull);
    });
  });
}

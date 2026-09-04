import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/room_management_dialog.dart';
import 'package:poker_client/features/table/presentation/table_action_bar.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/table_roster_dialog.dart';
import 'package:poker_client/features/table/presentation/table_status_widgets.dart';

Map<String, dynamic> _seat(
  String userId, {
  int seat = 1,
  int stack = 2000,
  List<String> holeCards = const [],
  bool pendingSpectate = false,
}) => {
  'userId': userId,
  'displayName': '玩家$userId',
  'seat': seat,
  'stack': stack,
  'ready': false,
  'connected': true,
  'participating': true,
  'folded': false,
  'allIn': false,
  'streetBet': 0,
  'totalBet': 0,
  if (holeCards.isNotEmpty) 'holeCards': holeCards,
  if (pendingSpectate) 'pendingSpectate': true,
};

Map<String, dynamic> _spectator(
  String userId, {
  int stack = 2000,
  bool canSee = true,
  bool pendingSeat = false,
}) => {
  'userId': userId,
  'displayName': '观众$userId',
  'connected': true,
  'stack': stack,
  'canSeeHoleCards': canSee,
  'pendingSeat': pendingSeat,
};

Map<String, dynamic> _payload({
  String phase = 'WAITING_NEXT_HAND',
  List<Map<String, dynamic>> seats = const [],
  List<Map<String, dynamic>> spectators = const [],
  bool spectating = false,
  int feeBigBlinds = 10,
  int? spectatorFee,
  Map<String, dynamic>? spectatorFees,
}) => {
  'roomId': 'room_1',
  'roomCode': '123456',
  'ownerUserId': 'owner',
  'tableRevision': 3,
  'phase': phase,
  'handId': phase == 'WAITING_NEXT_HAND' ? '' : 'hand_1',
  'maxBuyIn': 2000,
  'seats': seats,
  'spectators': spectators,
  'spectatorSettings': {
    'feeBigBlinds': feeBigBlinds,
    'voiceAllowed': true,
    'chatAllowed': false,
    'emoteAllowed': true,
  },
  'spectating': spectating,
  // 默认按 20 的大盲算：10 个大盲 = 200
  'spectatorFee': spectatorFee ?? feeBigBlinds * 20,
  'spectatorFees': ?spectatorFees,
};

GameSocketClient _clientWith(Map<String, dynamic> payload, {String me = 'me'}) {
  final client = GameSocketClient(
    accessTokenProvider: ({bool forceRefresh = false}) async => 'token',
    roomId: 'room_1',
    userId: me,
  );
  client.debugHandleMessage(
    jsonEncode({
      'version': 1,
      'type': 'table.joined',
      'payload': {'roomId': 'room_1'},
    }),
  );
  client.debugHandleMessage(
    jsonEncode({
      'version': 1,
      'type': 'table.snapshot',
      'tableId': 'room_1',
      'sequence': 1,
      'payload': payload,
    }),
  );
  return client;
}

FriendRoom _room() => const FriendRoom(
  roomId: 'room_1',
  code: '123456',
  ownerUserId: 'owner',
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

Future<void> _pumpBar(WidgetTester tester, GameSocketClient client) =>
    tester.pumpWidget(
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

void main() {
  group('快照解析', () {
    test('观战位字段全部落到模型上', () {
      final snapshot = TableSnapshot.fromJson(
        _payload(
          phase: 'FLOP',
          seats: [
            _seat('a', holeCards: const ['As', 'Kd'], pendingSpectate: true),
          ],
          spectators: [_spectator('s', canSee: true, pendingSeat: true)],
          spectating: true,
          feeBigBlinds: 5,
          spectatorFees: {
            'handId': 'hand_1',
            'feePerSpectator': 100,
            'payers': [
              {'userId': 's', 'displayName': '观众s', 'amount': 100},
            ],
            'recipients': [
              {'userId': 'a', 'displayName': '玩家a', 'amount': 100},
            ],
          },
        ),
      );

      expect(snapshot.spectating, isTrue);
      expect(snapshot.spectators.single.displayName, '观众s');
      expect(snapshot.spectators.single.pendingSeat, isTrue);
      expect(snapshot.spectatorSettings.feeBigBlinds, 5);
      expect(snapshot.spectatorSettings.chatAllowed, isFalse);
      expect(snapshot.seats.single.holeCards, ['As', 'Kd']);
      expect(snapshot.seats.single.pendingSpectate, isTrue);
      expect(snapshot.spectatorFees!.recipients.single.amount, 100);
      expect(snapshot.seatedCount, 1);
    });

    test('老服务端不下发观战字段时按默认值处理', () {
      final payload = _payload()
        ..remove('spectators')
        ..remove('spectatorSettings')
        ..remove('spectating');
      final snapshot = TableSnapshot.fromJson(payload);
      expect(snapshot.spectators, isEmpty);
      expect(snapshot.spectating, isFalse);
      expect(snapshot.spectatorSettings.feeBigBlinds, 10);
      expect(snapshot.spectatorSettings.chatAllowed, isTrue);
    });
  });

  group('下注区：观战者面板', () {
    testWidgets('已付费的观战者看到状态、上桌与补码', (tester) async {
      final client = _clientWith(
        _payload(
          phase: 'FLOP',
          seats: [_seat('a')],
          spectators: [_spectator('me', canSee: true)],
          spectating: true,
        ),
      );
      await _pumpBar(tester, client);

      expect(find.text('观战中 · 本手可查看所有玩家手牌'), findsOneWidget);
      expect(find.byKey(const ValueKey('take-seat-button')), findsOneWidget);
      expect(find.byKey(const ValueKey('spectator-rebuy-button')), findsOneWidget);
      // 观战者没有准备按钮
      expect(find.text('准备开始'), findsNothing);
      client.dispose();
    });

    testWidgets('筹码不足时明确提示并把补码做成主按钮（D4）', (tester) async {
      final client = _clientWith(
        _payload(
          phase: 'FLOP',
          seats: [_seat('a')],
          spectators: [_spectator('me', stack: 50, canSee: false)],
          spectating: true,
        ),
      );
      await _pumpBar(tester, client);

      expect(find.textContaining('筹码不足以支付看牌费'), findsOneWidget);
      expect(find.textContaining('10 个大盲'), findsOneWidget);
      final rebuy = find.byKey(const ValueKey('spectator-rebuy-button'));
      expect(tester.widget(rebuy), isA<FilledButton>(), reason: '补码应被突出');
      client.dispose();
    });

    testWidgets('手间还没收费时不能说筹码不足，而是告诉他下一手可看', (tester) async {
      // 真机复现：带入 100 个大盲、刚进观战就被提示筹码不足——看牌权是
      // 开局收费时才发放的，手间 canSee 必然为 false，与筹码多少无关
      final client = _clientWith(
        _payload(
          seats: [_seat('a')],
          spectators: [_spectator('me', stack: 2000, canSee: false)],
          spectating: true,
        ),
      );
      await _pumpBar(tester, client);

      expect(find.textContaining('筹码不足'), findsNothing);
      expect(find.textContaining('下一手开始时支付看牌费'), findsOneWidget);
      final rebuy = find.byKey(const ValueKey('spectator-rebuy-button'));
      expect(tester.widget(rebuy), isA<OutlinedButton>(), reason: '不必突出补码');
      client.dispose();
    });

    testWidgets('牌局中途进入的观战者：本手没付费，说明下一手起可看', (tester) async {
      final client = _clientWith(
        _payload(
          phase: 'FLOP',
          seats: [_seat('a')],
          spectators: [_spectator('me', stack: 2000, canSee: false)],
          spectating: true,
        ),
      );
      await _pumpBar(tester, client);

      expect(find.textContaining('筹码不足'), findsNothing);
      expect(find.textContaining('本手中途进入'), findsOneWidget);
      client.dispose();
    });

    testWidgets('免费观战时无论筹码多少都不提示不足', (tester) async {
      final client = _clientWith(
        _payload(
          seats: [_seat('a')],
          spectators: [_spectator('me', stack: 0, canSee: false)],
          spectating: true,
          feeBigBlinds: 0,
        ),
      );
      await _pumpBar(tester, client);

      expect(find.textContaining('筹码不足'), findsNothing);
      expect(find.textContaining('免费观战'), findsOneWidget);
      client.dispose();
    });

    testWidgets('筹码不足的判断以看牌费的筹码数为准，与是否在牌局中无关', (tester) async {
      // 手间、筹码 150 < 看牌费 200：这才是真正的不足
      final client = _clientWith(
        _payload(
          seats: [_seat('a')],
          spectators: [_spectator('me', stack: 150, canSee: false)],
          spectating: true,
        ),
      );
      await _pumpBar(tester, client);

      expect(find.textContaining('筹码不足以支付看牌费'), findsOneWidget);
      expect(find.textContaining('共 200'), findsOneWidget);
      client.dispose();
    });

    testWidgets('已申请上桌时按钮改为「本手结束后上桌」并禁用', (tester) async {
      final client = _clientWith(
        _payload(
          phase: 'FLOP',
          seats: [_seat('a')],
          spectators: [_spectator('me', pendingSeat: true)],
          spectating: true,
        ),
      );
      await _pumpBar(tester, client);

      expect(find.text('本手结束后上桌'), findsOneWidget);
      final button = tester.widget<FilledButton>(
        find.byKey(const ValueKey('take-seat-button')),
      );
      expect(button.onPressed, isNull);
      client.dispose();
    });
  });

  group('下注区：准备区的进入观战', () {
    testWidgets('手间的准备区有「进入观战」按钮', (tester) async {
      final client = _clientWith(_payload(seats: [_seat('me'), _seat('a', seat: 2)]));
      await _pumpBar(tester, client);

      expect(find.text('进入观战'), findsOneWidget);
      final button = tester.widget<OutlinedButton>(
        find.byKey(const ValueKey('enter-spectate-button')),
      );
      expect(button.onPressed, isNotNull);
      client.dispose();
    });

    testWidgets('观战位满（10 人）时按钮禁用', (tester) async {
      final client = _clientWith(
        _payload(
          seats: [_seat('me'), _seat('a', seat: 2)],
          spectators: [for (var i = 0; i < 10; i++) _spectator('s$i')],
        ),
      );
      await _pumpBar(tester, client);

      final button = tester.widget<OutlinedButton>(
        find.byKey(const ValueKey('enter-spectate-button')),
      );
      expect(button.onPressed, isNull);
      client.dispose();
    });
  });

  group('房间信息栏', () {
    testWidgets('人数显示为 X/10（OB: Y）且可点击打开名单', (tester) async {
      var opened = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SizedBox(
              width: 240,
              child: TableRoomHeader(
                room: _room(),
                currentPlayers: 3,
                spectatorCount: 2,
                compact: true,
                onLeave: () async {},
                onSettings: () {},
                onShowResult: () {},
                onToggleChat: null,
                onShowRoster: () => opened++,
              ),
            ),
          ),
        ),
      );

      expect(find.text('3/10（OB: 2）'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('roster-label')));
      expect(opened, 1);
    });

    testWidgets('分母是房间的最大人数，不写死 10', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SizedBox(
              width: 240,
              child: TableRoomHeader(
                room: _room(),
                currentPlayers: 3,
                spectatorCount: 1,
                maxPlayers: 6,
                compact: true,
                onLeave: () async {},
                onSettings: () {},
                onShowResult: () {},
                onToggleChat: null,
                onShowRoster: () {},
              ),
            ),
          ),
        ),
      );
      expect(find.text('3/6（OB: 1）'), findsOneWidget);
    });

    test('stackForUser 对座位与观战位都有效：补码弹窗靠它拿观战者的筹码', () {
      // 真机复现路径：观战者点补码，页面只在座位里找自己 → 找不到 → 静默返回
      final snapshot = TableSnapshot.fromJson(
        _payload(
          seats: [_seat('a', stack: 1500)],
          spectators: [_spectator('me', stack: 300)],
          spectating: true,
        ),
      );
      expect(snapshot.stackForUser('a'), 1500);
      expect(snapshot.stackForUser('me'), 300);
      expect(snapshot.stackForUser('nobody'), isNull);
    });
  });

  group('房间名单', () {
    testWidgets('分列上桌玩家与观战者，并标出看牌与待上桌状态', (tester) async {
      final snapshot = TableSnapshot.fromJson(
        _payload(
          seats: [_seat('me'), _seat('a', seat: 2, pendingSpectate: true)],
          spectators: [
            _spectator('s1', canSee: true),
            _spectator('s2', stack: 10, canSee: false, pendingSeat: true),
          ],
        ),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: TableRosterDialog(snapshot: snapshot, currentUserId: 'me'),
          ),
        ),
      );

      expect(find.text('房间名单 · 2/10（OB: 2）'), findsOneWidget);
      expect(find.text('玩家me（我）'), findsOneWidget);
      expect(find.textContaining('本手结束后进入观战'), findsOneWidget);
      expect(find.textContaining('本手可看牌'), findsOneWidget);
      expect(find.textContaining('本手不可看牌'), findsOneWidget);
      expect(find.textContaining('本手结束后上桌'), findsOneWidget);
    });
  });

  group('房主的观战位设置', () {
    Future<List<SpectatorSettings>> pump(WidgetTester tester) async {
      final updates = <SpectatorSettings>[];
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: RoomManagementDialog(
              snapshot: TableSnapshot.fromJson(_payload(seats: [_seat('me')])),
              currentUserId: 'me',
              joinLocked: false,
              onSetJoinLocked: (locked) async => locked,
              onRemoveMember: (_) async {},
              spectatorSettings: const SpectatorSettings(feeBigBlinds: 10),
              onUpdateSpectatorSettings: (settings) {
                updates.add(settings);
                return true;
              },
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      return updates;
    }

    testWidgets('三个开关各自独立地更新设置', (tester) async {
      final updates = await pump(tester);

      await tester.tap(find.byKey(const ValueKey('spectator-voice-switch')));
      await tester.pumpAndSettle();
      expect(updates.last.voiceAllowed, isFalse);
      expect(updates.last.chatAllowed, isTrue, reason: '只改动了语音');

      await tester.tap(find.byKey(const ValueKey('spectator-chat-switch')));
      await tester.pumpAndSettle();
      expect(updates.last.chatAllowed, isFalse);
      expect(updates.last.voiceAllowed, isFalse, reason: '之前的改动要保留');
    });

    testWidgets('看牌费提交后更新，超范围时提示且不发送', (tester) async {
      final updates = await pump(tester);
      final field = find.byKey(const ValueKey('spectator-fee-field'));

      await tester.enterText(field, '0');
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await tester.pumpAndSettle();
      expect(updates.last.feeBigBlinds, 0, reason: '0 表示免费');

      await tester.enterText(field, '101');
      await tester.testTextInput.receiveAction(TextInputAction.done);
      await tester.pumpAndSettle();
      expect(find.textContaining('需在 0～100 之间'), findsOneWidget);
      expect(updates.last.feeBigBlinds, 0, reason: '非法值不能发出去');
    });
  });

  group('文案', () {
    test('看牌费明细写清谁付了多少、每人分到多少', () {
      final label = spectatorFeeLabel(
        const SpectatorFees(
          handId: 'hand_1',
          feePerSpectator: 200,
          payers: [
            SpectatorFeeShare(userId: 's', displayName: '小王', amount: 200),
          ],
          recipients: [
            SpectatorFeeShare(userId: 'a', displayName: '甲', amount: 100),
            SpectatorFeeShare(userId: 'b', displayName: '乙', amount: 100),
          ],
        ),
      );
      expect(label, contains('观战看牌费 200'));
      expect(label, contains('小王 各付 200'));
      expect(label, contains('每人 +100'));
    });

    test('新增错误码都有中文', () {
      for (final code in const [
        'spectators_full',
        'room_full',
        'spectator_cannot_ready',
        'spectator_chat_disabled',
        'spectator_emote_disabled',
        'spectator_voice_disabled',
        'invalid_spectator_settings',
      ]) {
        expect(gameErrorLabel(code), isNot(contains(code)), reason: code);
      }
    });
  });
  group('客户端侧的观战限制', () {
    // 服务端才是权威，客户端提前拦下只是为了少一次往返、多一句人话提示
    test('房主关闭文字聊天后，观战者发消息被本地拦下并给出错误码', () {
      final client = _clientWith(
        _payload(spectating: true, spectators: [_spectator('me')]),
      );
      client.sendChat('hello');
      expect(client.errorMessage, 'spectator_chat_disabled');
      client.dispose();
    });

    test('允许的动作不会被误拦：表情默认放开，上桌玩家不受观战限制', () {
      final spectator = _clientWith(
        _payload(spectating: true, spectators: [_spectator('me')]),
      );
      spectator.interactWithPlayer('a', 'praise');
      expect(spectator.errorMessage, isNot('spectator_emote_disabled'));
      spectator.dispose();

      // 同样的「不允许聊天」设置，对上桌玩家没有任何影响
      final player = _clientWith(_payload(seats: [_seat('me')]));
      player.sendChat('hello');
      expect(player.errorMessage, isNot('spectator_chat_disabled'));
      player.dispose();
    });
  });

  group('错误提示不吞第二次', () {
    test('同一个错误码连续两次，序号也要递增两次', () {
      // 观战者连点两次「上桌」都被拒：文本相同，界面靠序号才能再提示一次
      final client = _clientWith(_payload(spectating: true, spectators: [_spectator('me')]));
      final before = client.errorSequence;
      for (var round = 0; round < 2; round++) {
        client.debugHandleMessage(
          jsonEncode({
            'version': 1,
            'type': 'system.error',
            'payload': {'code': 'room_full'},
          }),
        );
      }
      expect(client.errorMessage, 'room_full');
      expect(client.errorSequence, before + 2);
      client.dispose();
    });
  });

  group('房主设置在未连接时不假装成功', () {
    test('没连上牌桌时 setSpectatorSettings 返回 false', () {
      final client = GameSocketClient(
        accessTokenProvider: ({bool forceRefresh = false}) async => 'token',
        roomId: 'room_1',
        userId: 'owner',
      );
      expect(client.setSpectatorSettings(const SpectatorSettings()), isFalse);
      client.dispose();
    });

    testWidgets('发送失败时开关不拨过去，并给出提示', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: RoomManagementDialog(
              snapshot: TableSnapshot.fromJson(_payload(seats: [_seat('owner')])),
              currentUserId: 'owner',
              joinLocked: false,
              onSetJoinLocked: (locked) async => locked,
              onRemoveMember: (_) async {},
              spectatorSettings: const SpectatorSettings(),
              onUpdateSpectatorSettings: (_) => false,
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      final voice = find.byKey(const ValueKey('spectator-voice-switch'));
      await tester.ensureVisible(voice);
      await tester.tap(voice);
      await tester.pumpAndSettle();
      expect(tester.widget<SwitchListTile>(voice).value, isTrue, reason: '仍是默认允许');
      expect(find.text('还没连上牌桌，请稍后再试'), findsOneWidget);
    });
  });

  group('观战者补码上限', () {
    testWidgets('筹码已到带入上限时补码按钮禁用', (tester) async {
      // 与上桌玩家同一条规则：最多补到 maxBuyIn
      final client = _clientWith(
        _payload(
          phase: 'FLOP',
          seats: [_seat('a')],
          spectators: [_spectator('me', stack: 2000, canSee: true)],
          spectating: true,
        ),
      );
      await _pumpBar(tester, client);
      final button = tester.widget<ButtonStyleButton>(
        find.byKey(const ValueKey('spectator-rebuy-button')),
      );
      expect(button.onPressed, isNull);
      client.dispose();
    });

    testWidgets('筹码低于上限时可以补码', (tester) async {
      var rebuys = 0;
      final client = _clientWith(
        _payload(
          phase: 'FLOP',
          seats: [_seat('a')],
          spectators: [_spectator('me', stack: 1500, canSee: true)],
          spectating: true,
        ),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SizedBox(
              width: 200,
              child: TableActionBar(
                client: client,
                userId: 'me',
                smallBlind: 10,
                onRebuy: () => rebuys++,
                vertical: true,
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.byKey(const ValueKey('spectator-rebuy-button')));
      expect(rebuys, 1);
      client.dispose();
    });
  });
}

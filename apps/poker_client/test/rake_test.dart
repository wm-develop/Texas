import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/admin/domain/audit_event.dart';
import 'package:poker_client/features/admin/domain/rake.dart';
import 'package:poker_client/features/admin/presentation/admin_audit_page.dart';
import 'package:poker_client/features/admin/presentation/admin_rake_page.dart';
import 'package:poker_client/features/admin/presentation/rake_settings_dialog.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_board_center.dart';
import 'package:poker_client/features/table/presentation/table_chat_panel.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/table_status_widgets.dart';

const _room = AdminRoom(
  roomId: 'room_1',
  roomCode: '123456',
  ownerName: '房主',
  smallBlind: 10,
  bigBlind: 20,
  seatedCount: 3,
  spectatorCount: 1,
  rake: RakeSettings(),
  rakeTotal: 0,
  rakeHands: 0,
);

http.Response _json(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

/// 打开设置窗口，返回一个读取窗口结果的函数（未关闭或取消时为 null）。
Future<RakeSettings? Function()> _openDialog(
  WidgetTester tester,
  AdminRoom room,
) async {
  RakeSettings? result;
  await tester.pumpWidget(
    MaterialApp(
      home: Builder(
        builder: (context) => TextButton(
          onPressed: () async {
            result = await RakeSettingsDialog.show(context, room);
          },
          child: const Text('open'),
        ),
      ),
    ),
  );
  await tester.tap(find.text('open'));
  await tester.pumpAndSettle();
  return () => result;
}

void main() {
  group('抽水的数据与文案', () {
    test('结算与牌谱带上本手抽水，缺省为 0', () {
      final settlement = TableSettlement.fromJson(const {
        'handId': 'hand_1',
        'showdown': true,
        'rake': 21,
      });
      expect(settlement.rake, 21);
      expect(
        TableSettlement.fromJson(const {'handId': 'hand_2'}).rake,
        0,
        reason: '没抽水时服务端省略该字段',
      );
      expect(rakeLabel(21), '本手抽水 21（已从底池扣除）');

      final hand = RecentHand.fromJson(const {
        'handId': 'hand_1',
        'endedAt': '2026-09-18T00:00:00Z',
        'rake': 5,
      });
      expect(hand.rake, 5);
    });

    test('万分比写成百分数', () {
      expect(formatBasisPoints(500), '5%');
      expect(formatBasisPoints(250), '2.5%');
      expect(formatBasisPoints(25), '0.25%');
      expect(formatBasisPoints(1000), '10%');
      expect(formatBasisPoints(5), '0.05%');
    });

    test('规则的一句话概括', () {
      expect(const RakeSettings().summary, '不抽水');
      expect(
        const RakeSettings(enabled: true, basisPoints: 250, cap: 100).summary,
        '2.5%，最多 100',
      );
      expect(
        const RakeSettings(
          enabled: true,
          basisPoints: 500,
          postflopEnabled: true,
          postflopAmount: 20,
        ).summary,
        '5%，不封顶；翻后加抽 20',
      );
      expect(const RakeSettings(enabled: true).summary, '已开启，但实际不抽');
    });

    test('系统公告按 kind 识别', () {
      final message = TableChatMessage.fromJson(const {
        'messageId': 'm1',
        'userId': 'admin',
        'displayName': '系统公告',
        'kind': 'system',
        'content': '管理员已调整本房间的抽水',
        'sentAt': 0,
      });
      expect(message.isSystem, isTrue);
    });

    test('审计记录把抽水变更翻译成中文', () {
      final event = AuditEvent.fromJson(const {
        'eventId': 'e1',
        'actorUserId': 'admin',
        'eventType': 'admin.rake_changed',
        'createdAt': '2026-09-18T00:00:00Z',
        'metadata': {
          'roomId': 'room_1',
          'roomCode': '123456',
          'enabled': true,
          'basisPoints': 250,
          'cap': 100,
          'postflopEnabled': false,
          'postflopAmount': 0,
        },
      });
      expect(
        describeAuditEvent(event, const AuditLog(events: [], users: {})),
        '把房间 123456 的抽水设为：2.5%，最多 100',
      );
    });
  });

  group('抽水接口', () {
    test('设置规则时五个字段全部传齐，走 POST', () async {
      final client = GameApiClient(
        serverBaseUri: Uri.parse('http://game.test'),
        httpClient: MockClient((request) async {
          // Web 端的 CORS 只放行 GET/POST
          expect(request.method, 'POST');
          expect(request.url.path, '/v1/admin/rooms/room_1/rake');
          expect(request.headers['authorization'], 'Bearer token');
          final body = jsonDecode(request.body) as Map<String, dynamic>;
          expect(body, {
            'enabled': true,
            'basisPoints': 250,
            'cap': 0,
            'postflopEnabled': false,
            'postflopAmount': 0,
          });
          return _json({'roomId': 'room_1', 'roomCode': '123456', 'rake': body});
        }),
      );
      final saved = await client.adminSetRoomRake(
        accessToken: 'token',
        roomId: 'room_1',
        settings: const RakeSettings(enabled: true, basisPoints: 250),
      );
      expect(saved.enabled, isTrue);
      expect(saved.basisPoints, 250);
    });

    test('解析房间列表与累计抽水', () async {
      final client = GameApiClient(
        serverBaseUri: Uri.parse('http://game.test'),
        httpClient: MockClient((request) async {
          if (request.url.path == '/v1/admin/rooms') {
            return _json({
              'rooms': [
                {
                  'roomId': 'room_1',
                  'roomCode': '123456',
                  'ownerName': '房主',
                  'smallBlind': 10,
                  'bigBlind': 20,
                  'seatedCount': 3,
                  'spectatorCount': 0,
                  'rake': {'enabled': true, 'basisPoints': 500, 'cap': 60},
                  'rakeTotal': 42,
                  'rakeHands': 2,
                },
              ],
            });
          }
          expect(request.url.path, '/v1/admin/rake');
          return _json({
            'rooms': [
              {
                'roomId': 'room_1',
                'roomCode': '123456',
                'closed': false,
                'hands': 2,
                'total': 42,
              },
              {
                'roomId': 'room_0',
                'roomCode': '654321',
                'closed': true,
                'hands': 10,
                'total': 300,
              },
            ],
            'total': 342,
            'hands': 12,
          });
        }),
      );
      final rooms = await client.adminRooms('token');
      expect(rooms.single.rake.cap, 60);
      expect(rooms.single.rakeTotal, 42);
      final summary = await client.adminRakeSummary('token');
      expect(summary.total, 342);
      expect(summary.rooms.last.closed, isTrue);
    });
  });

  group('抽水设置窗口', () {
    testWidgets('翻后加抽超过一个大盲时不提交', (tester) async {
      final result = await _openDialog(tester, _room);
      await tester.tap(find.byKey(const ValueKey('rake-enabled')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('rake-postflop-enabled')));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const ValueKey('rake-postflop-amount')),
        '21',
      );
      await tester.tap(find.byKey(const ValueKey('rake-save')));
      await tester.pumpAndSettle();
      expect(find.text('翻后加抽不能超过一个大盲（20）'), findsOneWidget);
      expect(result(), isNull);

      await tester.enterText(
        find.byKey(const ValueKey('rake-postflop-amount')),
        '20',
      );
      await tester.tap(find.byKey(const ValueKey('rake-save')));
      await tester.pumpAndSettle();
      final saved = result()!;
      expect(saved.enabled, isTrue);
      expect(saved.basisPoints, 0);
      expect(saved.postflopEnabled, isTrue);
      expect(saved.postflopAmount, 20);
    });

    testWidgets('开启但什么都不抽时给出提示', (tester) async {
      final result = await _openDialog(tester, _room);
      await tester.tap(find.byKey(const ValueKey('rake-enabled')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('rake-save')));
      await tester.pumpAndSettle();
      expect(find.textContaining('实际不会抽'), findsOneWidget);
      expect(result(), isNull);
    });

    testWidgets('关闭抽水时保留原来的比例与封顶', (tester) async {
      const configured = AdminRoom(
        roomId: 'room_1',
        roomCode: '123456',
        ownerName: '房主',
        smallBlind: 10,
        bigBlind: 20,
        seatedCount: 3,
        spectatorCount: 0,
        rake: RakeSettings(
          enabled: true,
          basisPoints: 500,
          cap: 60,
          postflopEnabled: true,
          postflopAmount: 10,
        ),
        rakeTotal: 0,
        rakeHands: 0,
      );
      final result = await _openDialog(tester, configured);
      await tester.tap(find.byKey(const ValueKey('rake-enabled')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('rake-save')));
      await tester.pumpAndSettle();
      final saved = result()!;
      expect(saved.enabled, isFalse);
      expect(saved.basisPoints, 500);
      expect(saved.cap, 60);
      expect(saved.postflopAmount, 10);
    });

    testWidgets('手机横屏的矮窗口里不溢出', (tester) async {
      tester.view.physicalSize = const Size(740, 340);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      const configured = AdminRoom(
        roomId: 'room_1',
        roomCode: '123456',
        ownerName: '房主',
        smallBlind: 10,
        bigBlind: 20,
        seatedCount: 3,
        spectatorCount: 0,
        rake: RakeSettings(
          enabled: true,
          basisPoints: 500,
          cap: 60,
          postflopEnabled: true,
          postflopAmount: 10,
        ),
        rakeTotal: 0,
        rakeHands: 0,
      );
      await _openDialog(tester, configured);
      expect(tester.takeException(), isNull);
      expect(find.byKey(const ValueKey('rake-postflop-amount')), findsOneWidget);
    });
  });

  group('抽水管理页', () {
    testWidgets('列出房间、累计与换算，保存后刷新', (tester) async {
      final saved = <RakeSettings>[];
      var loads = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: AdminRakePage(
            loadRooms: () async {
              loads++;
              return const [_room];
            },
            loadSummary: () async => const RakeSummary(
              rooms: [
                RoomRakeTotal(
                  roomId: 'room_1',
                  roomCode: '123456',
                  closed: false,
                  hands: 2,
                  total: 400,
                ),
                RoomRakeTotal(
                  roomId: 'room_0',
                  roomCode: '654321',
                  closed: true,
                  hands: 8,
                  total: 1600,
                ),
              ],
              total: 2000,
              hands: 10,
            ),
            saveRake: (roomId, settings) async {
              expect(roomId, 'room_1');
              saved.add(settings);
              return settings;
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('房间 123456 · 10/20'), findsOneWidget);
      expect(find.textContaining('不抽水 · 已抽 0'), findsOneWidget);
      expect(find.text('合计 2000'), findsOneWidget);
      // 默认 10 元 = 2000 筹码
      expect(find.text('≈ 10.00 元'), findsOneWidget);
      expect(find.textContaining('房间 654321 · 已关闭'), findsOneWidget);
      expect(find.textContaining('≈ 8.00 元'), findsOneWidget);

      await tester.tap(find.text('设置'));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('rake-enabled')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('rake-postflop-enabled')));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const ValueKey('rake-postflop-amount')),
        '10',
      );
      await tester.tap(find.byKey(const ValueKey('rake-save')));
      await tester.pumpAndSettle();
      expect(saved.single.postflopAmount, 10);
      expect(loads, 2, reason: '保存后要重新拉取，列表才会显示新规则');
    });

    testWidgets('累计取不到时仍能设置房间，且不把「没取到」说成「没有」', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: AdminRakePage(
            loadRooms: () async => const [_room],
            loadSummary: () async => throw const GameApiException(
              'internal_error',
              statusCode: 500,
            ),
            saveRake: (_, settings) async => settings,
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('读取累计抽水失败：服务端出错'), findsOneWidget);
      expect(find.text('设置'), findsOneWidget);
      expect(find.text('还没有任何抽水记录'), findsNothing);
      expect(find.text('合计 —'), findsOneWidget);
    });

    testWidgets('窄窗口里天文数字的累计不会把一行撑破', (tester) async {
      tester.view.devicePixelRatio = 1;
      tester.view.physicalSize = const Size(360, 640);
      addTearDown(tester.view.reset);
      await tester.pumpWidget(
        MaterialApp(
          home: AdminRakePage(
            loadRooms: () async => const [_room],
            loadSummary: () async => const RakeSummary(
              rooms: [
                RoomRakeTotal(
                  roomId: 'room_1',
                  roomCode: '123456',
                  closed: false,
                  hands: 99999,
                  total: 9000000000000000,
                ),
              ],
              total: 9000000000000000,
              hands: 99999,
            ),
            saveRake: (_, settings) async => settings,
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(
        find.byKey(const ValueKey('rake-total-room_1')),
        200,
        // 页面里的输入框自己也带 Scrollable，要指明滚的是列表
        scrollable: find.byType(Scrollable).first,
      );
      expect(tester.takeException(), isNull);
    });

    testWidgets('服务端拒绝时显示中文原因', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: AdminRakePage(
            loadRooms: () async =>
                throw const GameApiException('admin_required', statusCode: 403),
            loadSummary: () async =>
                const RakeSummary(rooms: [], total: 0, hands: 0),
            saveRake: (_, settings) async => settings,
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('需要管理员权限'), findsOneWidget);
      expect(find.text('当前没有开着的房间'), findsNothing);
    });
  });

  group('玩家看到的抽水', () {
    GameSocketClient chatClient(List<Map<String, dynamic>> messages) {
      final client = GameSocketClient(
        accessTokenProvider: ({bool forceRefresh = false}) async => 'token',
        roomId: 'room_1',
        userId: 'me',
      );
      var sequence = 0;
      for (final message in messages) {
        client.debugHandleMessage(
          jsonEncode({
            'version': 1,
            'type': 'table.chat.message',
            'tableId': 'room_1',
            'sequence': ++sequence,
            'payload': message,
          }),
        );
      }
      return client;
    }

    testWidgets('聊天面板：公告不受屏蔽名单影响、没有屏蔽入口，玩家消息照常被屏蔽', (tester) async {
      final client = chatClient([
        {
          'messageId': 'm1',
          'userId': 'admin',
          'displayName': '系统公告',
          'kind': 'system',
          'content': '下一手起按 5% 抽水',
          'sentAt': 0,
        },
        {
          'messageId': 'm2',
          'userId': 'admin',
          'displayName': '管理员本人',
          'kind': 'text',
          'content': '这条是被屏蔽的人说的',
          'sentAt': 1,
        },
      ]);
      addTearDown(client.dispose);
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SizedBox(
              width: 360,
              height: 480,
              child: TableChatPanel(
                client: client,
                currentUserId: 'me',
                // 公告借用管理员的用户 ID 入库；屏蔽了他也必须看得到公告
                blockedUserIds: const {'admin'},
                onBlockChanged: (_, _) {},
                onClose: () {},
              ),
            ),
          ),
        ),
      );
      expect(find.byType(TableChatSystemLine), findsOneWidget);
      expect(find.textContaining('下一手起按 5% 抽水'), findsOneWidget);
      expect(find.textContaining('这条是被屏蔽的人说的'), findsNothing);
      expect(find.byTooltip('消息选项'), findsNothing);
    });

    testWidgets('结算区写明本手抽水；没抽水的手不多出这一行', (tester) async {
      TableSnapshot snapshot(int rake) => TableSnapshot.fromJson({
        'roomId': 'room_1',
        'roomCode': '123456',
        'tableRevision': 9,
        'phase': 'SETTLEMENT',
        'handId': 'hand_1',
        'totalPot': 950,
        'board': const ['As', 'Kd', 'Qh', 'Jc', 'Ts'],
        'seats': const <dynamic>[],
        'settlement': {
          'handId': 'hand_1',
          'showdown': true,
          'revealedHands': <dynamic>[],
          'potAwards': <dynamic>[],
          'rake': ?(rake > 0 ? rake : null),
        },
      });
      Future<void> pump(int rake) => tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Center(
              child: SizedBox(
                width: 700,
                height: 400,
                child: TableBoardCenter(
                  snapshot: snapshot(rake),
                  actionRemaining: Duration.zero,
                ),
              ),
            ),
          ),
        ),
      );
      await pump(50);
      await tester.pump();
      expect(find.text('本手抽水 50（已从底池扣除）'), findsOneWidget);
      await pump(0);
      await tester.pump();
      expect(find.textContaining('本手抽水'), findsNothing);
    });

    testWidgets('牌桌信息栏随时显示当前抽水规则，不抽时不占地方', (tester) async {
      const room = FriendRoom(
        roomId: 'room_1',
        code: '123456',
        ownerUserId: 'owner',
        preset: 'standard',
        maxPlayers: 10,
        rules: RoomRules(
          startingChips: 1000,
          maxBuyIn: 2000,
          smallBlind: 10,
          bigBlind: 20,
          actionSeconds: 30,
        ),
        members: [],
        revision: 1,
      );
      Future<void> pump(RakeSettings rake) => tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SizedBox(
              width: 240,
              child: TableRoomHeader(
                room: room,
                currentPlayers: 3,
                compact: true,
                rake: rake,
                onLeave: () async {},
                onSettings: () {},
                onShowResult: () {},
                onToggleChat: null,
              ),
            ),
          ),
        ),
      );
      await pump(
        const RakeSettings(
          enabled: true,
          basisPoints: 500,
          cap: 50,
          postflopEnabled: true,
          postflopAmount: 10,
        ),
      );
      expect(find.text('抽水 5%，最多 50；翻后加抽 10'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await pump(const RakeSettings());
      expect(find.byKey(const ValueKey('room-header-rake')), findsNothing);
      // 开着但比例与加抽都是 0：实际不抽，也不显示
      await pump(const RakeSettings(enabled: true));
      expect(find.byKey(const ValueKey('room-header-rake')), findsNothing);
    });

    test('快照与加入前的预览都带着房间的抽水规则', () {
      final snapshot = TableSnapshot.fromJson(const {
        'roomId': 'room_1',
        'roomCode': '123456',
        'tableRevision': 1,
        'phase': 'WAITING',
        'seats': <dynamic>[],
        'rake': {'enabled': true, 'basisPoints': 250, 'cap': 100},
      });
      expect(snapshot.rake.summary, '2.5%，最多 100');
      final preview = RoomPreview.fromJson(const {
        'code': '123456',
        'rules': {
          'startingChips': 1000,
          'maxBuyIn': 2000,
          'smallBlind': 10,
          'bigBlind': 20,
          'actionSeconds': 30,
        },
        'maxPlayers': 10,
        'currentPlayers': 2,
        'passwordRequired': false,
        'rake': {'enabled': true, 'basisPoints': 500},
      });
      expect(preview.rake.takesChips, isTrue);
      // 旧服务端没有这个字段
      expect(
        RoomPreview.fromJson(const {
          'code': '123456',
          'rules': {
            'startingChips': 1000,
            'maxBuyIn': 2000,
            'smallBlind': 10,
            'bigBlind': 20,
            'actionSeconds': 30,
          },
          'maxPlayers': 10,
          'currentPlayers': 2,
          'passwordRequired': false,
        }).rake.takesChips,
        isFalse,
      );
    });
  });
}

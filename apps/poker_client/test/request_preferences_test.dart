import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/core/settings/settings_dialog.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/request_preferences_dialog.dart';
import 'package:poker_client/features/table/presentation/table_automation_coordinator.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/table_request_dialog.dart';

GameSocketClient _client() => GameSocketClient(
  accessTokenProvider: ({bool forceRefresh = false}) async => 'token',
  roomId: 'room_1',
  userId: 'me',
);

String _envelope(String type, Map<String, dynamic> payload) => jsonEncode({
  'version': 1,
  'type': type,
  'tableId': 'room_1',
  'payload': payload,
});

class _Notifier extends ChangeNotifier {
  void ping() => notifyListeners();
}

void main() {
  group('RequestPreferences 解析', () {
    test('缺字段时默认全部允许', () {
      expect(RequestPreferences.fromJson(null), const RequestPreferences());
      expect(
        RequestPreferences.fromJson(const {}),
        const RequestPreferences(
          allowSeatSwapRequests: true,
          allowHoleCardViewRequests: true,
        ),
      );
    });

    test('往返保持两个开关', () {
      const preferences = RequestPreferences(
        allowSeatSwapRequests: false,
        allowHoleCardViewRequests: true,
      );
      expect(RequestPreferences.fromJson(preferences.toJson()), preferences);
      expect(
        preferences.copyWith(allowHoleCardViewRequests: false),
        const RequestPreferences(
          allowSeatSwapRequests: false,
          allowHoleCardViewRequests: false,
        ),
      );
    });
  });

  group('申请相关文案', () {
    test('服务端当场拒绝时带上对方昵称', () {
      expect(
        requestErrorLabel('seat_swap_requests_disabled', '张三'),
        '张三不接受换座申请',
      );
      expect(
        requestErrorLabel('seat_swap_requester_blocked', '张三'),
        '张三不再接受你的换座申请',
      );
      expect(
        requestErrorLabel('hole_card_view_requests_disabled', '张三'),
        '张三不接受看牌申请',
      );
      expect(
        requestErrorLabel('hole_card_view_requester_blocked', '张三'),
        '张三本手不再接受你的看牌申请',
      );
      expect(
        requestErrorLabel('hole_card_view_blocked_this_hand', '张三'),
        '张三本手不接受任何看牌申请',
      );
      expect(
        requestErrorLabel('hole_card_view_already_granted', '张三'),
        '你本手已经看过张三的牌',
      );
      expect(requestErrorLabel('hand_in_progress', '张三'), isNull);
      expect(requestErrorLabel('seat_swap_requests_disabled', ''), '对方不接受换座申请');
    });

    test('对方在弹窗里拒绝后的提示按范围区分', () {
      expect(
        requestDeclinedLabel(
          kind: 'seat_swap',
          scope: 'once',
          targetName: '张三',
        ),
        '张三拒绝了这次换座',
      );
      expect(
        requestDeclinedLabel(
          kind: 'seat_swap',
          scope: 'requester',
          targetName: '张三',
        ),
        '张三不再接受你的换座申请',
      );
      expect(
        requestDeclinedLabel(
          kind: 'hole_card_view',
          scope: 'once',
          targetName: '张三',
        ),
        '张三拒绝了这次看牌',
      );
      expect(
        requestDeclinedLabel(
          kind: 'hole_card_view',
          scope: 'requester',
          targetName: '张三',
        ),
        '张三本手不再接受你的看牌申请',
      );
      expect(
        requestDeclinedLabel(
          kind: 'hole_card_view',
          scope: 'everyone',
          targetName: '张三',
        ),
        '张三本手不接受任何看牌申请',
      );
    });

    test('新错误码都有中文映射', () {
      for (final code in const [
        'seat_swap_requests_disabled',
        'seat_swap_requester_blocked',
        'hole_card_view_requests_disabled',
        'hole_card_view_requester_blocked',
        'hole_card_view_blocked_this_hand',
        'hole_card_view_already_granted',
        'invalid_decline_scope',
      ]) {
        expect(gameErrorLabel(code), isNot(contains(code)), reason: code);
      }
    });
  });

  test('table.request.declined 只更新申请被拒通知，按序号去重', () {
    final client = _client();
    var notified = 0;
    client.addListener(() => notified++);
    expect(client.latestDeclinedRequest, isNull);
    client.debugHandleMessage(
      _envelope('table.request.declined', {
        'kind': 'hole_card_view',
        'requestId': 'view-1',
        'targetUserId': 'u2',
        'targetDisplayName': '张三',
        'scope': 'everyone',
      }),
    );
    expect(client.declinedRequestSequence, 1);
    expect(client.latestDeclinedRequest?.kind, 'hole_card_view');
    expect(client.latestDeclinedRequest?.targetDisplayName, '张三');
    expect(client.latestDeclinedRequest?.scope, 'everyone');
    expect(client.errorMessage, isNull, reason: '被拒不是错误，不能走错误提示');
    expect(notified, 1);
    client.dispose();
  });

  group('申请答复弹窗', () {
    Future<RequestDecision?> open(WidgetTester tester, bool holeCards) async {
      RequestDecision? result;
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: Center(
                child: FilledButton(
                  onPressed: () async {
                    result = await showDialog<RequestDecision>(
                      context: context,
                      builder: (_) => TableRequestDialog(
                        title: '申请',
                        description: '说明',
                        holeCards: holeCards,
                      ),
                    );
                  },
                  child: const Text('打开'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开'));
      await tester.pumpAndSettle();
      return result;
    }

    testWidgets('换座申请没有「本手不再接受任何人」，不再接受此人返回 requester', (tester) async {
      await open(tester, false);
      expect(
        find.byKey(const ValueKey('request-decline-everyone')),
        findsNothing,
      );
      expect(find.text('不再接受此人的换座申请'), findsOneWidget);
      await tester.tap(find.byKey(const ValueKey('request-decline-requester')));
      await tester.pumpAndSettle();
      // 弹窗已关闭
      expect(find.byType(TableRequestDialog), findsNothing);
    });

    testWidgets('看牌申请四个选项各自返回对应的决定', (tester) async {
      for (final (key, decision) in const [
        ('request-decline-once', RequestDecision.declineOnce),
        ('request-decline-requester', RequestDecision.declineRequester),
        ('request-decline-everyone', RequestDecision.declineEveryone),
        ('request-accept', RequestDecision.accept),
      ]) {
        RequestDecision? result;
        await tester.pumpWidget(
          MaterialApp(
            home: Builder(
              builder: (context) => Scaffold(
                body: Center(
                  child: FilledButton(
                    onPressed: () async {
                      result = await showDialog<RequestDecision>(
                        context: context,
                        builder: (_) => const TableRequestDialog(
                          title: '申请',
                          description: '说明',
                          holeCards: true,
                        ),
                      );
                    },
                    child: const Text('打开'),
                  ),
                ),
              ),
            ),
          ),
        );
        await tester.tap(find.text('打开'));
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull, reason: '四个按钮不能溢出');
        await tester.tap(find.byKey(ValueKey(key)));
        await tester.pumpAndSettle();
        expect(result, decision, reason: key);
      }
    });

    test('决定映射为发给服务端的 accept 与 scope', () {
      expect(RequestDecision.accept.accepted, isTrue);
      expect(RequestDecision.declineOnce.scope, 'once');
      expect(RequestDecision.declineRequester.scope, 'requester');
      expect(RequestDecision.declineEveryone.scope, 'everyone');
      expect(RequestDecision.declineEveryone.accepted, isFalse);
    });
  });

  testWidgets('偏好弹窗切换开关即时回调新偏好', (tester) async {
    final changes = <RequestPreferences>[];
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: RequestPreferencesDialog(
            initial: const RequestPreferences(),
            onChanged: (next) {
              changes.add(next);
              return true;
            },
          ),
        ),
      ),
    );
    await tester.tap(find.byKey(const ValueKey('request-pref-seat-swap')));
    await tester.pumpAndSettle();
    expect(changes, [
      const RequestPreferences(
        allowSeatSwapRequests: false,
        allowHoleCardViewRequests: true,
      ),
    ]);
    await tester.tap(find.byKey(const ValueKey('request-pref-hole-card-view')));
    await tester.pumpAndSettle();
    expect(
      changes.last,
      const RequestPreferences(
        allowSeatSwapRequests: false,
        allowHoleCardViewRequests: false,
      ),
    );
  });

  testWidgets('偏好弹窗在命令发不出去时不拨开关并提示', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: RequestPreferencesDialog(
            initial: const RequestPreferences(),
            onChanged: (_) => false,
          ),
        ),
      ),
    );
    await tester.tap(find.byKey(const ValueKey('request-pref-seat-swap')));
    await tester.pumpAndSettle();
    final toggle = tester.widget<SwitchListTile>(
      find.byKey(const ValueKey('request-pref-seat-swap')),
    );
    expect(toggle.value, isTrue, reason: '服务端没收到，开关不能停在新位置');
    expect(find.byKey(const ValueKey('request-pref-error')), findsOneWidget);
    expect(find.text('还没连上牌桌，请稍后再试'), findsOneWidget);
  });

  testWidgets('看牌申请弹窗在横屏手机大字号下正文可读、按钮可点', (tester) async {
    tester.view.physicalSize = const Size(640, 360);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    RequestDecision? result;
    await tester.pumpWidget(
      MaterialApp(
        // 字号放大必须经 MaterialApp.builder 注入：showDialog 走根导航器，
        // home 下面包一层 MediaQuery 对弹窗不起作用。
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(
            context,
          ).copyWith(textScaler: const TextScaler.linear(1.3)),
          child: child!,
        ),
        home: Builder(
          builder: (context) => Scaffold(
            body: Center(
              child: FilledButton(
                onPressed: () async {
                  result = await showDialog<RequestDecision>(
                    context: context,
                    builder: (_) => const TableRequestDialog(
                      title: '查看手牌申请',
                      description: '张三已弃牌，申请提前查看你的手牌。是否同意？',
                      holeCards: true,
                    ),
                  );
                },
                child: const Text('打开'),
              ),
            ),
          ),
        ),
      ),
    );
    await tester.tap(find.text('打开'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    final dialogContext = tester.element(find.byType(TableRequestDialog));
    expect(
      MediaQuery.textScalerOf(dialogContext).scale(10),
      13,
      reason: '放大字号必须真的作用到弹窗上，否则这条测试在测空气',
    );
    final description = find.text('张三已弃牌，申请提前查看你的手牌。是否同意？');
    expect(description, findsOneWidget);
    expect(tester.getSize(description).height, greaterThan(0));
    expect(
      find.ancestor(
        of: description,
        matching: find.byType(SingleChildScrollView),
      ),
      findsWidgets,
      reason: '正文所在区域必须可滚动，放不下时滚而不是被挤掉',
    );
    // 动作栏只有两个按钮，永远在同一行：拒绝在左、同意在右
    final decline = tester.getRect(
      find.byKey(const ValueKey('request-decline-once')),
    );
    final accept = tester.getRect(find.byKey(const ValueKey('request-accept')));
    expect(decline.top, accept.top);
    expect(decline.right, lessThanOrEqualTo(accept.left));
    // 范围选项在正文里，滚到可见后能点到
    final everyone = find.byKey(const ValueKey('request-decline-everyone'));
    await tester.ensureVisible(everyone);
    await tester.tap(everyone);
    await tester.pumpAndSettle();
    expect(result, RequestDecision.declineEveryone);
  });

  Map<String, dynamic> snapshotJson({
    List<Map<String, String>> holeCardViewRequests = const [],
    List<Map<String, String>> seatSwapRequests = const [],
    Map<String, dynamic>? requestPreferences,
  }) => {
    'roomId': 'room_1',
    'roomCode': '123456',
    'tableRevision': 1,
    'phase': 'FLOP',
    'handId': 'hand_1',
    'seats': [
      {
        'userId': 'me',
        'displayName': '我',
        'seat': 1,
        'stack': 1000,
        'ready': true,
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
    'holeCardViewRequests': holeCardViewRequests,
    'seatSwapRequests': seatSwapRequests,
    'requestPreferences': ?requestPreferences,
  };

  group('快照解析 requestPreferences', () {
    test('字段缺失时默认全部允许', () {
      final snapshot = TableSnapshot.fromJson(snapshotJson());
      expect(snapshot.requestPreferences, const RequestPreferences());
    });

    test('按服务端的键名解析', () {
      final snapshot = TableSnapshot.fromJson(
        snapshotJson(
          requestPreferences: {
            'allowSeatSwapRequests': false,
            'allowHoleCardViewRequests': true,
          },
        ),
      );
      expect(snapshot.requestPreferences.allowSeatSwapRequests, isFalse);
      expect(snapshot.requestPreferences.allowHoleCardViewRequests, isTrue);
    });
  });

  group('拒绝后本地撤回排队申请', () {
    Map<String, String> request(String id, String requester) => {
      'requestId': id,
      'requesterUserId': requester,
      'targetUserId': 'me',
    };

    test('本手不再接受任何人：同一目标下其余看牌申请不再弹出', () {
      final coordinator = TableAutomationCoordinator(currentUserId: 'me');
      final snapshot = TableSnapshot.fromJson(
        snapshotJson(
          holeCardViewRequests: [request('v1', 'a'), request('v2', 'b')],
          seatSwapRequests: [request('s1', 'a')],
        ),
      );
      final first = coordinator.takeNextRequest(snapshot)!;
      expect(first.request.requestId, 'v1');
      coordinator.withdrawAfterDecline(
        snapshot: snapshot,
        declined: first.request,
        holeCards: true,
        scope: 'everyone',
      );
      coordinator.requestDialogOpen = false;
      // 旧快照上再取：看牌申请都没了，换座申请不受影响
      final next = coordinator.takeNextRequest(snapshot)!;
      expect(next.request.requestId, 's1');
      expect(next.holeCards, isFalse);
    });

    test('不再接受此人：只撤同一申请者的，其他人的照常弹', () {
      final coordinator = TableAutomationCoordinator(currentUserId: 'me');
      final snapshot = TableSnapshot.fromJson(
        snapshotJson(
          holeCardViewRequests: [
            request('v1', 'a'),
            request('v2', 'a'),
            request('v3', 'b'),
          ],
        ),
      );
      final first = coordinator.takeNextRequest(snapshot)!;
      coordinator.withdrawAfterDecline(
        snapshot: snapshot,
        declined: first.request,
        holeCards: true,
        scope: 'requester',
      );
      coordinator.requestDialogOpen = false;
      expect(coordinator.takeNextRequest(snapshot)!.request.requestId, 'v3');
    });

    test('只拒这一次：不撤任何申请', () {
      final coordinator = TableAutomationCoordinator(currentUserId: 'me');
      final snapshot = TableSnapshot.fromJson(
        snapshotJson(
          holeCardViewRequests: [request('v1', 'a'), request('v2', 'a')],
        ),
      );
      final first = coordinator.takeNextRequest(snapshot)!;
      coordinator.withdrawAfterDecline(
        snapshot: snapshot,
        declined: first.request,
        holeCards: true,
        scope: 'once',
      );
      coordinator.requestDialogOpen = false;
      expect(coordinator.takeNextRequest(snapshot)!.request.requestId, 'v2');
    });

    test('断线重连中不弹申请，重连后同一条照常弹', () {
      final coordinator = TableAutomationCoordinator(currentUserId: 'me');
      final snapshot = TableSnapshot.fromJson(
        snapshotJson(seatSwapRequests: [request('s1', 'a')]),
      );
      expect(
        coordinator.takeNextRequest(snapshot, socketJoined: false),
        isNull,
      );
      expect(
        coordinator.takeNextRequest(snapshot)!.request.requestId,
        's1',
        reason: '没弹过的不能被记成已处理',
      );
    });

    test('答复的那条已不在快照里时，不替下一手的申请作答', () {
      final coordinator = TableAutomationCoordinator(currentUserId: 'me');
      final oldHand = TableSnapshot.fromJson(
        snapshotJson(holeCardViewRequests: [request('v1', 'a')]),
      );
      final stale = coordinator.takeNextRequest(oldHand)!;
      // 弹窗开着时本手结束、下一手 a 又申请了一次
      final nextHand = TableSnapshot.fromJson(
        snapshotJson(holeCardViewRequests: [request('v2', 'a')]),
      );
      coordinator.withdrawAfterDecline(
        snapshot: nextHand,
        declined: stale.request,
        holeCards: true,
        scope: 'requester',
      );
      coordinator.requestDialogOpen = false;
      expect(coordinator.takeNextRequest(nextHand)!.request.requestId, 'v2');
    });

    test('答复发不出去时放回，下次还会弹', () {
      final coordinator = TableAutomationCoordinator(currentUserId: 'me');
      final snapshot = TableSnapshot.fromJson(
        snapshotJson(seatSwapRequests: [request('s1', 'a')]),
      );
      final first = coordinator.takeNextRequest(snapshot)!;
      coordinator.requestDialogOpen = false;
      expect(coordinator.takeNextRequest(snapshot), isNull);
      coordinator.forgetRequest(first.request.requestId);
      expect(coordinator.takeNextRequest(snapshot)!.request.requestId, 's1');
    });
  });

  testWidgets('偏好弹窗跟着新快照里的服务端真实值走', (tester) async {
    final updates = _Notifier();
    var current = const RequestPreferences();
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: RequestPreferencesDialog(
            initial: current,
            onChanged: (_) => true,
            updates: updates,
            current: () => current,
          ),
        ),
      ),
    );
    // 服务端手间重启或命令在断线瞬间丢失：快照说换座申请其实是关着的
    current = const RequestPreferences(
      allowSeatSwapRequests: false,
      allowHoleCardViewRequests: true,
    );
    updates.ping();
    await tester.pump();
    final toggle = tester.widget<SwitchListTile>(
      find.byKey(const ValueKey('request-pref-seat-swap')),
    );
    expect(toggle.value, isFalse);
  });

  test('答复在没连上牌桌时不发送并返回 false', () {
    final client = _client();
    expect(client.respondSeatSwap('s1', false, scope: 'requester'), isFalse);
    expect(client.respondHoleCardsView('v1', true), isFalse);
    expect(client.setRequestPreferences(const RequestPreferences()), isFalse);
    client.dispose();
  });

  group('设置弹窗里的申请偏好入口', () {
    testWidgets('传入回调时显示并可点开', (tester) async {
      final settings = AppSettingsController();
      var opened = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: Center(
                child: FilledButton(
                  onPressed: () => showAppSettingsDialog(
                    context,
                    settings,
                    onOpenRequestPreferences: () => opened++,
                  ),
                  child: const Text('打开'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开'));
      await tester.pumpAndSettle();
      final entry = find.byKey(const ValueKey('settings-request-preferences'));
      await tester.scrollUntilVisible(entry, 80);
      await tester.tap(entry);
      await tester.pumpAndSettle();
      expect(opened, 1);
    });

    testWidgets('观战者等没有回调时不显示', (tester) async {
      final settings = AppSettingsController();
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: Center(
                child: FilledButton(
                  onPressed: () => showAppSettingsDialog(context, settings),
                  child: const Text('打开'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开'));
      await tester.pumpAndSettle();
      expect(
        find.byKey(const ValueKey('settings-request-preferences')),
        findsNothing,
      );
    });
  });
}

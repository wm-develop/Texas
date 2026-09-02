import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/admin/domain/audit_event.dart';
import 'package:poker_client/features/admin/presentation/admin_audit_page.dart';

AuditLog _sampleLog() => AuditLog.fromJson({
  'events': [
    {
      'eventId': 'aud_1',
      'actorUserId': 'usr_admin',
      'eventType': 'admin.wallet_changed',
      'metadata': {'targetUserId': 'usr_player', 'walletChips': 5000},
      'createdAt': '2026-09-02T10:00:00Z',
    },
    {
      'eventId': 'aud_2',
      'actorUserId': 'usr_player',
      'roomId': 'room_1',
      'eventType': 'voice.joined',
      'metadata': {'microphoneEnabled': true},
      'createdAt': '2026-09-02T09:59:00Z',
    },
    {
      'eventId': 'aud_3',
      'actorUserId': 'usr_player',
      'roomId': 'room_1',
      'eventType': 'voice.left',
      'metadata': {'reason': 'disconnected'},
      'createdAt': '2026-09-02T09:58:00Z',
    },
    {
      'eventId': 'aud_4',
      'actorUserId': 'usr_player',
      'eventType': 'account.self_deleted',
      'metadata': {
        'previousUsername': 'old_name',
        'transferredChips': 1500,
        'recipientUserId': 'usr_admin',
      },
      'createdAt': '2026-09-02T09:57:00Z',
    },
  ],
  'users': {
    'usr_admin': {'username': 'boss', 'displayName': '管理员'},
    'usr_player': {'username': 'pl', 'displayName': '玩家甲'},
  },
});

void main() {
  testWidgets('renders Chinese descriptions and filters by category', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: AdminAuditPage(loader: ({String userId = ''}) async => _sampleLog()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('把 玩家甲（pl） 的钱包调整为 5000 筹码'), findsOneWidget);
    expect(find.text('加入语音（开麦）'), findsOneWidget);
    expect(find.text('退出语音（连接断开）'), findsOneWidget);
    expect(
      find.text('注销账号（原用户名 old_name，1500 筹码转入 管理员（boss））'),
      findsOneWidget,
    );
    expect(find.textContaining('操作者 管理员（boss）'), findsOneWidget);

    await tester.tap(find.text('语音进出'));
    await tester.pumpAndSettle();
    expect(find.text('加入语音（开麦）'), findsOneWidget);
    expect(find.textContaining('钱包调整'), findsNothing);

    await tester.tap(find.text('全部'));
    await tester.enterText(find.byType(TextField), 'boss');
    await tester.pumpAndSettle();
    expect(find.textContaining('钱包调整'), findsOneWidget);
    expect(find.text('加入语音（开麦）'), findsNothing);
  });

  testWidgets('shows a load error instead of crashing', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: AdminAuditPage(
          loader: ({String userId = ''}) async => throw Exception('boom'),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('无法连接游戏服务'), findsOneWidget);
    expect(find.text('没有匹配的审计记录'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

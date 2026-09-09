import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:poker_client/app/poker_app.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/features/profile/presentation/profile_page.dart';

AuthSession _session({bool admin = false}) => AuthSession(
  user: AppUser(
    userId: 'user-1',
    username: 'leaving_player',
    displayName: '要走的人',
    role: admin ? 'admin' : 'player',
  ),
  accessToken: 'access-token',
  refreshToken: 'refresh-token',
  accessExpiresAt: DateTime.utc(2026, 9, 28),
  refreshExpiresAt: DateTime.utc(2026, 10, 28),
);

Widget _profile(
  AuthSession session, {
  Future<void> Function(String password)? onDeleteAccount,
}) => MaterialApp(
  home: ProfilePage(
    session: session,
    onUpdateUsername: (_) async => session.user,
    onUpdateDisplayName: (_) async => session.user,
    onChangePassword: (_, _) async => session,
    onDeleteAccount: onDeleteAccount,
  ),
);

void main() {
  // 模拟一台没有存过登录态的设备：启动时的恢复会立刻得出「没有令牌」
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('deleting the account asks for the password and confirms', (
    tester,
  ) async {
    String? submittedPassword;
    await tester.pumpWidget(
      _profile(
        _session(),
        onDeleteAccount: (password) async => submittedPassword = password,
      ),
    );

    await tester.tap(find.text('注销账号'));
    await tester.pumpAndSettle();
    expect(find.textContaining('转入服务器管理员账户'), findsOneWidget);
    expect(find.textContaining('不可撤销'), findsOneWidget);

    await tester.enterText(find.byType(TextFormField), 'player-secret-1');
    await tester.tap(find.text('确认注销'));
    await tester.pumpAndSettle();
    expect(submittedPassword, 'player-secret-1');
  });

  testWidgets('cancelling the dialog does not delete', (tester) async {
    var called = false;
    await tester.pumpWidget(
      _profile(_session(), onDeleteAccount: (_) async => called = true),
    );
    await tester.tap(find.text('注销账号'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('取消'));
    await tester.pumpAndSettle();
    expect(called, isFalse);
  });

  testWidgets('administrators do not see the delete entry', (tester) async {
    await tester.pumpWidget(
      _profile(_session(admin: true), onDeleteAccount: (_) async {}),
    );
    expect(find.text('注销账号'), findsNothing);
    expect(find.text('隐私说明'), findsOneWidget);
  });

  testWidgets('the privacy notice opens from the registration form', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1200, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(const PokerApp());
    // 启动时会先读设备上存的刷新令牌；空存储读完就结束，一帧即可。
    // 不能用 pumpAndSettle：等待界面上的进度圈是无限动画，永远不静止。
    await tester.pump();
    expect(find.text('《隐私说明》'), findsNothing);
    await tester.tap(find.text('注册'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('《隐私说明》'));
    await tester.pumpAndSettle();
    expect(find.textContaining('不保存明文密码'), findsOneWidget);
    expect(find.textContaining('不接入支付'), findsOneWidget);
    await tester.tap(find.text('我知道了'));
    await tester.pumpAndSettle();
    expect(find.text('隐私说明'), findsNothing);
    expect(tester.takeException(), isNull);
  });
}

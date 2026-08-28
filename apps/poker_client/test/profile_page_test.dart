import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/features/profile/presentation/profile_page.dart';

void main() {
  testWidgets('updates login username, table nickname and password', (
    tester,
  ) async {
    const user = AppUser(
      userId: 'user-1',
      username: 'old_login',
      displayName: '牌桌好友',
    );
    final session = AuthSession(
      user: user,
      accessToken: 'access-token',
      refreshToken: 'refresh-token',
      accessExpiresAt: DateTime.utc(2026, 8, 28),
      refreshExpiresAt: DateTime.utc(2026, 9, 28),
    );
    String? changedCurrentPassword;
    String? changedNewPassword;

    await tester.pumpWidget(
      MaterialApp(
        home: ProfilePage(
          session: session,
          onUpdateUsername: (username) async => AppUser(
            userId: user.userId,
            username: username,
            displayName: user.displayName,
          ),
          onUpdateDisplayName: (displayName) async => AppUser(
            userId: user.userId,
            username: 'new_login',
            displayName: displayName,
          ),
          onChangePassword: (currentPassword, newPassword) async {
            changedCurrentPassword = currentPassword;
            changedNewPassword = newPassword;
            return session.copyWith(accessToken: 'fresh-access-token');
          },
        ),
      ),
    );

    await tester.tap(find.text('修改登录用户名'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField), 'new_login');
    await tester.tap(find.text('保存'));
    await tester.pumpAndSettle();
    expect(find.text('new_login'), findsOneWidget);

    await tester.tap(find.text('修改牌桌昵称'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField), '新的昵称');
    await tester.tap(find.text('保存'));
    await tester.pumpAndSettle();
    expect(find.text('新的昵称'), findsOneWidget);

    await tester.tap(find.text('修改密码'));
    await tester.pumpAndSettle();
    final passwordFields = find.byType(TextFormField);
    await tester.enterText(passwordFields.at(0), 'password-123');
    await tester.enterText(passwordFields.at(1), 'new-password-456');
    await tester.enterText(passwordFields.at(2), 'new-password-456');
    await tester.tap(find.text('确认修改'));
    await tester.pumpAndSettle();

    expect(changedCurrentPassword, 'password-123');
    expect(changedNewPassword, 'new-password-456');
    expect(find.text('密码已修改，其他已登录设备需要重新登录'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:poker_client/app/poker_app.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/features/auth/presentation/auth_page.dart';

void main() {
  // 模拟一台没有存过登录态的设备：启动时的恢复会立刻得出「没有令牌」
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('starts at the friends-only sign in screen', (tester) async {
    tester.view.physicalSize = const Size(1280, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(const PokerApp());
    // 启动时会先读设备上存的刷新令牌；空存储读完就结束，一帧即可。
    // 不能用 pumpAndSettle：等待界面上的进度圈是无限动画，永远不静止。
    await tester.pump();

    expect(find.text('好友德州'), findsOneWidget);
    expect(find.text('只和认识的朋友，快速组织一桌牌局'), findsOneWidget);
    expect(find.text('登录'), findsWidgets);
    expect(find.text('加入语音'), findsNothing);
  });

  testWidgets('fits the registration form on a landscape phone', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(800, 360);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(const PokerApp());
    // 启动时会先读设备上存的刷新令牌；空存储读完就结束，一帧即可。
    // 不能用 pumpAndSettle：等待界面上的进度圈是无限动画，永远不静止。
    await tester.pump();
    await tester.tap(find.text('注册'));
    await tester.pump();

    expect(find.text('牌桌昵称'), findsOneWidget);
    expect(find.text('注册格式要求'), findsOneWidget);
    expect(find.textContaining('3～24 位'), findsWidgets);
    expect(find.textContaining('1～20 个字符'), findsOneWidget);
    expect(find.textContaining('8～128 字节'), findsOneWidget);
    final submitButton = find.widgetWithText(FilledButton, '注册并进入');
    expect(submitButton, findsOneWidget);
    expect(tester.getBottomRight(submitButton).dy, lessThanOrEqualTo(360));
    expect(tester.takeException(), isNull);
  });

  testWidgets('registration validation explains each rejected field', (
    tester,
  ) async {
    var submitted = false;
    await tester.pumpWidget(
      MaterialApp(
        home: AuthPage(
          onLogin: (_, _) => throw UnimplementedError(),
          onRegister: (_, _, _, _) async {
            submitted = true;
            throw UnimplementedError();
          },
        ),
      ),
    );

    await tester.tap(find.text('注册'));
    await tester.pump();
    await tester.enterText(find.byType(TextFormField).at(0), '中');
    await tester.enterText(
      find.byType(TextFormField).at(1),
      '这是一个超过二十个字符的牌桌昵称用于验证提示',
    );
    await tester.enterText(find.byType(TextFormField).at(2), '123');
    final submitButton = find.text('注册并进入');
    await tester.ensureVisible(submitButton);
    await tester.tap(submitButton);
    await tester.pump();

    expect(find.text('账号只能包含英文字母、数字和下划线'), findsOneWidget);
    expect(find.text('牌桌昵称不能超过 20 个字符'), findsOneWidget);
    expect(find.textContaining('密码须为 8～128 字节'), findsOneWidget);
    expect(submitted, isFalse);
  });

  testWidgets('ten registration logo taps request the initial administrator', (
    tester,
  ) async {
    var requestedAdmin = false;
    await tester.pumpWidget(
      MaterialApp(
        home: AuthPage(
          onLogin: (_, _) => throw UnimplementedError(),
          onRegister: (_, _, _, requestAdmin) async {
            requestedAdmin = requestAdmin;
            return AuthSession(
              user: const AppUser(
                userId: 'usr_admin',
                username: 'admin',
                displayName: '管理员',
                role: 'admin',
              ),
              accessToken: 'access',
              refreshToken: 'refresh',
              accessExpiresAt: DateTime(2026, 8, 27),
              refreshExpiresAt: DateTime(2026, 9, 27),
            );
          },
        ),
      ),
    );

    await tester.tap(find.text('注册'));
    await tester.pump();
    for (var index = 0; index < 10; index++) {
      await tester.tap(find.byIcon(Icons.style));
    }
    await tester.pump();
    expect(find.textContaining('管理员注册模式已开启'), findsOneWidget);

    await tester.enterText(find.byType(TextFormField).at(0), 'admin_1');
    await tester.enterText(find.byType(TextFormField).at(1), '管理员');
    await tester.enterText(find.byType(TextFormField).at(2), 'password-123');
    final submitButton = find.text('注册并进入');
    await tester.ensureVisible(submitButton);
    await tester.tap(submitButton);
    await tester.pump();

    expect(requestedAdmin, isTrue);
  });
}

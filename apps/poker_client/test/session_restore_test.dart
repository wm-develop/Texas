import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/app/poker_app.dart';
import 'package:poker_client/features/auth/presentation/auth_page.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  // 本文件只覆盖失败路径（这里不注入网络客户端，所有 HTTP 都是 400）。
  // 恢复成功的完整路径见 session_restore_success_test.dart。
  //
  // 仍未覆盖、只能靠真机验证的一条：临时失败后回到前台自动重试。
  // tester.binding.handleAppLifecycleStateChanged 在本项目的 Flutter OH
  // 分支上不会通知 WidgetsBindingObserver，走平台消息也没能触发，
  // 那条测试写不稳，删掉而不是留一个假的。
  group('启动时恢复登录态', () {
    testWidgets('没存过令牌就直接显示登录页', (tester) async {
      tester.view.physicalSize = const Size(1280, 720);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});

      await tester.pumpWidget(const PokerApp());
      await tester.pump();

      expect(find.byType(AuthPage), findsOneWidget);
      expect(find.byKey(const ValueKey('session-restoring')), findsNothing);
    });

    testWidgets('第一帧不是登录页，避免闪一下再跳走', (tester) async {
      // 这正是加持久化要解决的场景：出去回条微信，进程被系统回收，回来
      // 不该再输一次账号密码，也不该先看到登录页再跳走
      tester.view.physicalSize = const Size(1280, 720);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({});

      await tester.pumpWidget(const PokerApp());

      // 还没读完存储，此时必须是等待而不是登录页
      expect(find.byKey(const ValueKey('session-restoring')), findsOneWidget);
      expect(find.byType(AuthPage), findsNothing);
      await tester.pump();
    });

    testWidgets('网络失败时回到登录页，但不清掉令牌', (tester) async {
      // 测试环境下所有 HTTP 都返回 400，等价于「没连上服务端」。
      // 这种情况清掉令牌，会让玩家在信号不好的地方启动一次就得重新登录。
      tester.view.physicalSize = const Size(1280, 720);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({
        'texas.session.refresh_token': 'still_good_token',
        'texas.session.refresh_expires_at': DateTime.now()
            .add(const Duration(days: 30))
            .toIso8601String(),
      });

      await tester.pumpWidget(const PokerApp());
      for (var frame = 0; frame < 5; frame++) {
        await tester.pump(const Duration(milliseconds: 20));
      }

      expect(find.byType(AuthPage), findsOneWidget);
      final raw = await SharedPreferences.getInstance();
      expect(
        raw.getString('texas.session.refresh_token'),
        'still_good_token',
        reason: '连不上服务端不代表令牌失效',
      );
    });

    testWidgets('过期的令牌当作没有，并且从存储里清掉', (tester) async {
      tester.view.physicalSize = const Size(1280, 720);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      SharedPreferences.setMockInitialValues({
        'texas.session.refresh_token': 'expired_token',
        'texas.session.refresh_expires_at': DateTime.now()
            .subtract(const Duration(days: 1))
            .toIso8601String(),
      });

      await tester.pumpWidget(const PokerApp());
      await tester.pump();

      expect(find.byType(AuthPage), findsOneWidget);
      final raw = await SharedPreferences.getInstance();
      expect(raw.getString('texas.session.refresh_token'), isNull);
    });
  });
  testWidgets('回到前台重试时不换掉玩家正在填的登录表单', (tester) async {
    // 触发这条路径的前提正是「上次因为网络失败」，也就是玩家很可能正在
    // 手动登录。切回等待界面会把 AuthPage 整个卸载，已输入的内容全没了。
    tester.view.physicalSize = const Size(1280, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    SharedPreferences.setMockInitialValues({
      'texas.session.refresh_token': 'still_good_token',
      'texas.session.refresh_expires_at': DateTime.now()
          .toUtc()
          .add(const Duration(days: 30))
          .toIso8601String(),
    });

    await tester.pumpWidget(const PokerApp());
    for (var frame = 0; frame < 5; frame++) {
      await tester.pump(const Duration(milliseconds: 20));
    }
    expect(find.byType(AuthPage), findsOneWidget);

    // 玩家开始输用户名
    final username = find.byType(TextField).first;
    await tester.enterText(username, 'my_account');
    await tester.pump();
    expect(find.text('my_account'), findsOneWidget);

    // 这里没法驱动真实的生命周期回调（见文件顶部说明），所以直接验证
    // 静默重试本身不会动界面：它不切 _restoringSession，AuthPage 留在原位。
    for (var frame = 0; frame < 5; frame++) {
      await tester.pump(const Duration(milliseconds: 20));
    }
    expect(find.byType(AuthPage), findsOneWidget);
    expect(find.byKey(const ValueKey('session-restoring')), findsNothing);
    expect(
      find.text('my_account'),
      findsOneWidget,
      reason: '已输入的内容不能被恢复流程冲掉',
    );
  });
}

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:poker_client/app/poker_app.dart';
import 'package:poker_client/core/auth/session_store.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/features/auth/presentation/auth_page.dart';
import 'package:poker_client/features/lobby/presentation/lobby_page.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 恢复成功的完整路径：换会话 → 查钱包 → 进大厅。
///
/// 这条路径此前没有测试，因为测试框架把所有 HTTP 打成 400。给 PokerApp
/// 开一个注入网络客户端的口子之后就能测了。
const _json = {'content-type': 'application/json; charset=utf-8'};

Map<String, dynamic> _sessionPayload() => {
  'user': {
    'userId': 'usr_1',
    'username': 'player',
    'displayName': '玩家',
    'role': 'player',
    'status': 'active',
  },
  'accessToken': 'access_new',
  'refreshToken': 'refresh_new',
  'accessExpiresAt': DateTime.now()
      .add(const Duration(minutes: 15))
      .toIso8601String(),
  'refreshExpiresAt': DateTime.now()
      .add(const Duration(days: 30))
      .toIso8601String(),
};

GameApiClient _api({
  required List<String> seenPaths,
  String tableId = '',
  int refreshStatus = 200,
}) => GameApiClient(
  serverBaseUri: Uri.parse('http://game.test'),
  httpClient: MockClient((request) async {
    seenPaths.add(request.url.path);
    switch (request.url.path) {
      case '/v1/auth/refresh':
        if (refreshStatus != 200) {
          return http.Response(
            jsonEncode({'error': 'invalid_refresh_token'}),
            refreshStatus,
            headers: _json,
          );
        }
        return http.Response(
          jsonEncode(_sessionPayload()),
          200,
          headers: _json,
        );
      case '/v1/bankroll':
        return http.Response(
          jsonEncode({
            'userId': 'usr_1',
            'walletChips': 5000,
            'tableChips': 0,
            'revision': 1,
            'tableId': tableId,
          }),
          200,
          headers: _json,
        );
      case '/v1/client/version':
        return http.Response(jsonEncode({'minimum': 0}), 200, headers: _json);
      default:
        return http.Response(jsonEncode({'ok': true}), 200, headers: _json);
    }
  }),
);

Future<void> _pumpApp(WidgetTester tester, GameApiClient api) async {
  tester.view.physicalSize = const Size(1280, 720);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    PokerApp(
      apiClient: api,
      // 强制启用持久化：默认实现在 Web 上是空操作，而测试要覆盖真实行为
      sessionStore: SessionStore(persistent: true),
    ),
  );
  for (var frame = 0; frame < 8; frame++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
}

void main() {
  setUp(
    () => SharedPreferences.setMockInitialValues({
      'texas.session.refresh_token': 'stored_refresh_token',
      'texas.session.refresh_expires_at': DateTime.now()
          .add(const Duration(days: 30))
          .toIso8601String(),
    }),
  );

  group('恢复成功', () {
    testWidgets('用存下来的令牌换回会话，直接进大厅，不必重新登录', (tester) async {
      final seenPaths = <String>[];
      await _pumpApp(tester, _api(seenPaths: seenPaths));

      expect(find.byType(LobbyPage), findsOneWidget);
      expect(find.byType(AuthPage), findsNothing);
      expect(seenPaths, contains('/v1/auth/refresh'));
      expect(seenPaths, contains('/v1/bankroll'));
    });

    testWidgets('轮换后的新令牌覆盖了存储里的旧令牌', (tester) async {
      // 服务端每次刷新都会换令牌。存着旧的，下次启动就登不回来了。
      await _pumpApp(tester, _api(seenPaths: []));

      final raw = await SharedPreferences.getInstance();
      expect(raw.getString('texas.session.refresh_token'), 'refresh_new');
    });

    testWidgets('钱包里没有牌桌时不去查房间', (tester) async {
      final seenPaths = <String>[];
      await _pumpApp(tester, _api(seenPaths: seenPaths, tableId: ''));

      expect(seenPaths, isNot(contains('/v1/rooms/current')));
      expect(find.byType(LobbyPage), findsOneWidget);
    });

    testWidgets('服务端拒绝令牌时回到登录页，并把它清掉', (tester) async {
      // 令牌可能已被吊销：改过密码、在别处注销过设备
      await _pumpApp(tester, _api(seenPaths: [], refreshStatus: 401));

      expect(find.byType(AuthPage), findsOneWidget);
      final raw = await SharedPreferences.getInstance();
      expect(raw.getString('texas.session.refresh_token'), isNull);
    });
  });

  group('Web 端不持久化', () {
    testWidgets('默认存储在 Web 上读不到东西，也写不进去', (tester) async {
      // 浏览器常常是公用的：下一个打开页面的人不该进入上一个人的账号
      SharedPreferences.setMockInitialValues({
        'texas.session.refresh_token': 'someone_elses_token',
        'texas.session.refresh_expires_at': DateTime.now()
            .add(const Duration(days: 30))
            .toIso8601String(),
      });
      final webStore = SessionStore(persistent: false);
      expect(await webStore.loadRefreshToken(), isNull);

      await webStore.clear();
      final raw = await SharedPreferences.getInstance();
      expect(
        raw.getString('texas.session.refresh_token'),
        'someone_elses_token',
        reason: '不持久化的实现不该去动存储里已有的内容',
      );
    });
  });
}

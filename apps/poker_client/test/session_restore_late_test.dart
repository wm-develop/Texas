import 'dart:async';
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

/// 慢网络下的迟到恢复。
///
/// `Future.timeout` 只是放弃等待，不会取消底层的网络请求：8 秒超时之后，
/// 那个换会话的请求可能十几秒后才成功返回。如果它的结果照样落地，玩家会
/// 在登录页上打字打到一半，界面自己跳进大厅；更糟的是他已经手动登录了另一个
/// 账号，会被覆盖成前一个人。
const _json = {'content-type': 'application/json; charset=utf-8'};

Map<String, dynamic> _sessionPayload(String suffix) => {
  'user': {
    'userId': 'usr_$suffix',
    'username': 'player_$suffix',
    'displayName': '玩家$suffix',
    'role': 'player',
    'status': 'active',
  },
  'accessToken': 'access_$suffix',
  'refreshToken': 'refresh_$suffix',
  'accessExpiresAt': DateTime.now()
      .add(const Duration(minutes: 15))
      .toIso8601String(),
  'refreshExpiresAt': DateTime.now()
      .add(const Duration(days: 30))
      .toIso8601String(),
};

http.Response _bankroll() => http.Response(
  jsonEncode({
    'userId': 'usr_a',
    'walletChips': 5000,
    'tableChips': 0,
    'revision': 1,
    'tableId': '',
  }),
  200,
  headers: _json,
);

void main() {
  setUp(
    () => SharedPreferences.setMockInitialValues({
      'texas.session.refresh_token': 'stored_token',
      'texas.session.refresh_expires_at': DateTime.now()
          .add(const Duration(days: 30))
          .toIso8601String(),
    }),
  );

  testWidgets('超时之后迟到成功的恢复不再改动界面', (tester) async {
    tester.view.physicalSize = const Size(1280, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    // 让换会话的请求一直挂着，由测试决定什么时候返回
    final refreshGate = Completer<http.Response>();
    final api = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        switch (request.url.path) {
          case '/v1/auth/refresh':
            return refreshGate.future;
          case '/v1/bankroll':
            return _bankroll();
          default:
            return http.Response(jsonEncode({'ok': true}), 200, headers: _json);
        }
      }),
      // 让客户端自己的请求超时不要先于恢复的总超时触发
      requestTimeout: const Duration(minutes: 1),
    );

    await tester.pumpWidget(
      PokerApp(apiClient: api, sessionStore: SessionStore(persistent: true)),
    );
    await tester.pump();
    expect(find.byKey(const ValueKey('session-restoring')), findsOneWidget);

    // 走过恢复的 8 秒总超时
    await tester.pump(const Duration(seconds: 9));
    expect(
      find.byType(AuthPage),
      findsOneWidget,
      reason: '超时后应该让玩家自己登录，而不是无限等待',
    );

    // 现在那个请求终于成功了
    refreshGate.complete(
      http.Response(jsonEncode(_sessionPayload('a')), 200, headers: _json),
    );
    for (var frame = 0; frame < 8; frame++) {
      await tester.pump(const Duration(milliseconds: 20));
    }

    expect(
      find.byType(AuthPage),
      findsOneWidget,
      reason: '迟到的恢复不该把正在登录的玩家推进大厅',
    );
    expect(find.byType(LobbyPage), findsNothing);

    // 但轮换出来的新令牌必须已经存下来：服务端刷新时删掉了旧会话，
    // 不存就等于把登录态丢了
    final raw = await SharedPreferences.getInstance();
    expect(
      raw.getString('texas.session.refresh_token'),
      'refresh_a',
      reason: '新令牌要保留，切后台回来还能用上',
    );
  });
}

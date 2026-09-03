import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:poker_client/core/app_version.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/features/update/presentation/update_required_page.dart';

GameApiClient _client(Future<http.Response> Function(http.Request) handler) =>
    GameApiClient(
      serverBaseUri: Uri.parse('http://example.com'),
      httpClient: MockClient(handler),
    );

void main() {
  group('客户端上报自身版本', () {
    test('普通请求带版本请求头', () async {
      String? reported;
      final client = _client((request) async {
        reported = request.headers[clientVersionHeader.toLowerCase()];
        return http.Response(jsonEncode({'ok': true}), 200);
      });

      await client.clientVersionRequirement();
      await client.heartbeat('token').catchError((_) {});

      expect(reported, '$appVersionCode');
      client.close();
    });
  });

  group('版本要求查询', () {
    test('读取最低版本', () async {
      // 相对当前版本取值：写死数字会在版本升过它之后悄悄失效
      final higher = appVersionCode + 1;
      final client = _client(
        (request) async => http.Response(jsonEncode({'minimum': higher}), 200),
      );

      final requirement = await client.clientVersionRequirement();

      expect(requirement, isNotNull);
      expect(requirement!.minimum, higher);
      expect(
        requirement.blocksCurrentBuild,
        isTrue,
        reason: '当前版本低于最低要求',
      );
      client.close();
    });

    test('服务端没启用门禁时不阻断', () async {
      final client = _client(
        (request) async => http.Response(jsonEncode({'minimum': 0}), 200),
      );

      final requirement = await client.clientVersionRequirement();

      expect(requirement!.blocksCurrentBuild, isFalse);
      client.close();
    });

    test('端点不存在或网络不通时返回空，不把自己锁死', () async {
      // 那种情况该更新的是服务端，或者只是暂时连不上
      for (final handler in <Future<http.Response> Function(http.Request)>[
        (_) async => http.Response('not found', 404),
        (_) async => throw const SocketExceptionStub(),
      ]) {
        final client = _client(handler);
        expect(await client.clientVersionRequirement(), isNull);
        client.close();
      }
    });
  });

  group('服务端拒绝过旧客户端', () {
    test('426 抛出专用异常，带上最低版本', () async {
      final client = _client(
        (request) async => http.Response(
          jsonEncode({'error': 'client_too_old', 'minimum': 3000}),
          426,
        ),
      );

      await expectLater(
        client.bankroll('token'),
        throwsA(
          isA<ClientTooOldException>().having(
            (error) => error.minimumVersionCode,
            'minimumVersionCode',
            3000,
          ),
        ),
      );
      client.close();
    });

    test('426 不会被当成普通接口错误', () async {
      final client = _client(
        (request) async => http.Response(jsonEncode({'minimum': 3000}), 426),
      );

      await expectLater(
        client.bankroll('token'),
        throwsA(isNot(isA<GameApiException>())),
      );
      client.close();
    });
  });

  group('阻断页', () {
    testWidgets('显示当前与所需版本，且没有「继续使用」的出口', (tester) async {
      var retried = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: UpdateRequiredPage(
            // 取一个永远不会成为真实版本的值：与当前版本相同的话，
            // 「当前」与「需要」会显示成同一个字符串，断言就失去意义
            minimumVersionCode: 999002001,
            onRetry: () async => retried++,
          ),
        ),
      );

      expect(find.text('需要更新客户端'), findsOneWidget);
      expect(find.text(appVersionName), findsOneWidget);
      expect(
        find.text('999.2.1'),
        findsOneWidget,
        reason: '999002001 应解码成 999.2.1',
      );
      // 阻断就是阻断：不提供跳过
      expect(find.textContaining('继续'), findsNothing);
      expect(find.textContaining('跳过'), findsNothing);

      await tester.tap(find.byKey(const ValueKey('update-retry-button')));
      expect(retried, 1);
    });

    test('Web 提示刷新页面，其他平台提示重新安装', () {
      // Web 刷新就是最新版本，让人去下载安装包是误导
      expect(
        UpdateRequiredPage.actionHintFor(TargetPlatform.android, isWeb: true),
        contains('刷新'),
      );
      expect(
        UpdateRequiredPage.actionHintFor(TargetPlatform.android, isWeb: false),
        contains('安装'),
      );
    });
  });
}

class SocketExceptionStub implements Exception {
  const SocketExceptionStub();
}

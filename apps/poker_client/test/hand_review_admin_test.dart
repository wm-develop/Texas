import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/features/admin/domain/audit_event.dart';
import 'package:poker_client/features/admin/domain/managed_user.dart';
import 'package:poker_client/features/admin/presentation/admin_audit_page.dart';
import 'package:poker_client/features/admin/presentation/admin_review_page.dart';
import 'package:poker_client/features/history/domain/hand_review.dart';

http.Response _json(Object body, {int status = 200}) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

ManagedUser _user(String id, String name, {String role = 'player'}) =>
    ManagedUser.fromJson({
      'userId': id,
      'username': id,
      'displayName': name,
      'role': role,
      'createdAt': '2026-09-01T00:00:00Z',
    });

ReviewOverview _overview({
  Set<String> access = const {},
  bool modelConfigured = true,
}) => ReviewOverview.fromJson({
  'settings': {
    'enabled': true,
    'dailyLimitPerUser': 5,
    'monthlyTokenBudget': 2000000,
  },
  'access': [
    for (final id in access) {'userId': id},
  ],
  'usage': {'requests24h': 3, 'requests30d': 12, 'tokens30d': 123456},
  'modelConfigured': modelConfigured,
  'model': 'deepseek-reasoner',
});

void main() {
  group('复盘接口', () {
    GameApiClient client(
      Future<http.Response> Function(http.Request request) handler,
    ) => GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient(handler),
    );

    test('老服务端没有复盘接口时当作没开通', () async {
      final api = client(
        (_) async => _json({'error': 'not_found'}, status: 404),
      );
      expect(await api.reviewAvailable('token'), isFalse);
    });

    test('没发起过的手取回 null；发起走 POST', () async {
      final api = client((request) async {
        expect(request.url.path, '/v1/hands/hand_1/review');
        if (request.method == 'GET') {
          return _json({'error': 'review_not_found'}, status: 404);
        }
        expect(request.method, 'POST');
        return _json({'handId': 'hand_1', 'status': 'queued'});
      });
      expect(
        await api.handReview(accessToken: 'token', handId: 'hand_1'),
        isNull,
      );
      final queued = await api.requestHandReview(
        accessToken: 'token',
        handId: 'hand_1',
      );
      expect(queued.inProgress, isTrue);
    });

    test('复盘结果与失败原因的解析', () async {
      final api = client(
        (_) async => _json({
          'handId': 'hand_1',
          'status': 'failed',
          'failure': 'model_error',
          'model': 'deepseek-reasoner',
        }),
      );
      final review = await api.handReview(
        accessToken: 'token',
        handId: 'hand_1',
      );
      expect(review!.failed, isTrue);
      expect(reviewErrorLabel(review.failure), '大模型服务暂时不可用，请稍后重试');
    });

    test('管理员设置三个字段一起提交', () async {
      final api = client((request) async {
        expect(request.method, 'POST');
        expect(request.url.path, '/v1/admin/review/settings');
        final body = jsonDecode(request.body) as Map<String, dynamic>;
        expect(body, {
          'enabled': false,
          'dailyLimitPerUser': 0,
          'monthlyTokenBudget': 500000,
        });
        return _json(body);
      });
      final saved = await api.adminSetReviewSettings(
        accessToken: 'token',
        settings: const ReviewSettings(
          enabled: false,
          monthlyTokenBudget: 500000,
        ),
      );
      expect(saved.enabled, isFalse);
    });

    test('名单为空时服务端可能给 null', () {
      final overview = ReviewOverview.fromJson({
        'settings': {'enabled': true},
        'access': null,
        'usage': <String, dynamic>{},
        'modelConfigured': false,
      });
      expect(overview.accessUserIds, isEmpty);
      expect(overview.modelConfigured, isFalse);
    });
  });

  group('审计记录', () {
    const log = AuditLog(
      events: [],
      users: {'usr_1': AuditUser(username: 'zhang', displayName: '老张')},
    );

    test('复盘设置与开通名单的变更翻译成中文', () {
      AuditEvent event(String type, Map<String, dynamic> metadata) =>
          AuditEvent.fromJson({
            'eventId': 'e1',
            'actorUserId': 'admin',
            'eventType': type,
            'createdAt': '2026-09-23T00:00:00Z',
            'metadata': metadata,
          });
      expect(
        describeAuditEvent(
          event('admin.review_access_changed', {
            'targetUserId': 'usr_1',
            'granted': true,
          }),
          log,
        ),
        '为 老张（zhang） 开通 AI 复盘',
      );
      expect(
        describeAuditEvent(
          event('admin.review_access_changed', {
            'targetUserId': 'usr_1',
            'granted': false,
          }),
          log,
        ),
        '收回 老张（zhang） 的 AI 复盘',
      );
      expect(
        describeAuditEvent(
          event('admin.review_settings_changed', {
            'enabled': true,
            'dailyLimitPerUser': 0,
            'monthlyTokenBudget': 1000000,
          }),
          log,
        ),
        '开放 AI 复盘，每人 24 小时 不限，30 天 token 1000000',
      );
    });
  });

  group('复盘管理页', () {
    Future<void> pump(
      WidgetTester tester, {
      required Future<ReviewOverview> Function() loadOverview,
      Future<ReviewSettings> Function(ReviewSettings settings)? saveSettings,
      Future<void> Function(String userId, bool granted)? setAccess,
    }) async {
      tester.view.devicePixelRatio = 1;
      tester.view.physicalSize = const Size(800, 1200);
      addTearDown(tester.view.reset);
      await tester.pumpWidget(
        MaterialApp(
          home: AdminReviewPage(
            loadOverview: loadOverview,
            loadUsers: () async => [
              _user('admin', '管理员', role: 'admin'),
              _user('usr_1', '老张'),
              _user('usr_2', '小李'),
            ],
            saveSettings: saveSettings ?? (settings) async => settings,
            setAccess: setAccess ?? (_, _) async {},
          ),
        ),
      );
      await tester.pumpAndSettle();
    }

    testWidgets('显示模型、用量与名单；管理员不在名单里', (tester) async {
      await pump(
        tester,
        loadOverview: () async => _overview(access: {'usr_1'}),
      );
      expect(find.text('大模型：deepseek-reasoner'), findsOneWidget);
      expect(
        tester.widget<Text>(find.byKey(const ValueKey('review-usage'))).data,
        '最近 24 小时 3 次 · 最近 30 天 12 次 · 12.3 万 token',
      );
      expect(find.byKey(const ValueKey('review-access-admin')), findsNothing);
      expect(
        tester
            .widget<SwitchListTile>(
              find.byKey(const ValueKey('review-access-usr_1')),
            )
            .value,
        isTrue,
      );
      expect(
        tester
            .widget<SwitchListTile>(
              find.byKey(const ValueKey('review-access-usr_2')),
            )
            .value,
        isFalse,
      );
    });

    testWidgets('没配置大模型时提示去配置环境变量', (tester) async {
      await pump(
        tester,
        loadOverview: () async => _overview(modelConfigured: false),
      );
      expect(find.text('服务端没有配置大模型'), findsOneWidget);
    });

    testWidgets('开通后刷新名单，不覆盖还没保存的设置', (tester) async {
      var access = <String>{};
      final calls = <String>[];
      await pump(
        tester,
        loadOverview: () async => _overview(access: access),
        setAccess: (userId, granted) async {
          calls.add('$userId:$granted');
          access = {...access, userId};
        },
      );
      await tester.enterText(
        find.byKey(const ValueKey('review-daily-limit')),
        '9',
      );
      await tester.tap(find.byKey(const ValueKey('review-access-usr_2')));
      await tester.pumpAndSettle();
      expect(calls, ['usr_2:true']);
      expect(
        tester
            .widget<SwitchListTile>(
              find.byKey(const ValueKey('review-access-usr_2')),
            )
            .value,
        isTrue,
      );
      expect(find.text('9'), findsOneWidget, reason: '正在改的额度不能被刷新冲掉');
    });

    testWidgets('保存设置；负数与非数字不提交', (tester) async {
      final saved = <ReviewSettings>[];
      await pump(
        tester,
        loadOverview: () async => _overview(),
        saveSettings: (settings) async {
          saved.add(settings);
          return settings;
        },
      );
      await tester.enterText(
        find.byKey(const ValueKey('review-daily-limit')),
        'abc',
      );
      await tester.tap(find.byKey(const ValueKey('review-save-settings')));
      await tester.pumpAndSettle();
      expect(saved, isEmpty);
      expect(find.text('额度要填不小于 0 的整数，0 表示不限'), findsOneWidget);

      await tester.enterText(
        find.byKey(const ValueKey('review-daily-limit')),
        '0',
      );
      await tester.tap(find.byKey(const ValueKey('review-enabled')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const ValueKey('review-save-settings')));
      await tester.pumpAndSettle();
      expect(saved, hasLength(1));
      expect(saved.single.enabled, isFalse);
      expect(saved.single.dailyLimitPerUser, 0);
      expect(saved.single.monthlyTokenBudget, 2000000);
    });

    testWidgets('开通之后刷新失败：不再按旧名单显示开关', (tester) async {
      var loads = 0;
      await pump(
        tester,
        loadOverview: () async {
          loads++;
          if (loads > 1) {
            throw const GameApiException('internal_error', statusCode: 500);
          }
          return _overview();
        },
      );
      await tester.tap(find.byKey(const ValueKey('review-access-usr_2')));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('review-access-usr_2')), findsNothing);
      expect(
        find.byKey(const ValueKey('review-access-unavailable')),
        findsOneWidget,
      );
    });

    testWidgets('读取失败时不列开关，不把「没取到」显示成「没开通」', (tester) async {
      await pump(
        tester,
        loadOverview: () async =>
            throw const GameApiException('internal_error', statusCode: 500),
      );
      expect(find.textContaining('读取复盘设置失败'), findsOneWidget);
      expect(find.byKey(const ValueKey('review-access-usr_1')), findsNothing);
      expect(
        find.byKey(const ValueKey('review-access-unavailable')),
        findsOneWidget,
      );
    });
  });
}

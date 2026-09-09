import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/auth/session_store.dart';
import 'package:shared_preferences/shared_preferences.dart';

AuthSession _session({
  String refreshToken = 'refresh_1',
  Duration refreshTtl = const Duration(days: 30),
}) => AuthSession(
  user: const AppUser(
    userId: 'usr_1',
    username: 'player',
    displayName: '玩家',
  ),
  accessToken: 'access_1',
  refreshToken: refreshToken,
  accessExpiresAt: DateTime.now().add(const Duration(minutes: 15)),
  refreshExpiresAt: DateTime.now().add(refreshTtl),
);

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  group('登录态持久化', () {
    test('存下刷新令牌，下次启动能读回来', () async {
      // 此前登录态只在内存里：进程被系统回收就要重新输账号密码
      final store = SessionStore(persistent: true);
      expect(await store.loadRefreshToken(), isNull);

      await store.save(_session());
      expect(await store.loadRefreshToken(), 'refresh_1');
    });

    test('轮换后的新令牌覆盖旧的', () async {
      // 服务端每次刷新都会换令牌，存着旧的等于下次启动登不回来
      final store = SessionStore(persistent: true);
      await store.save(_session());
      await store.save(_session(refreshToken: 'refresh_2'));
      expect(await store.loadRefreshToken(), 'refresh_2');
    });

    test('已过期的令牌不返回，并顺手清掉', () async {
      // 拿一个注定失败的令牌去换会话，只会让启动多等一个往返
      final store = SessionStore(persistent: true);
      await store.save(_session(refreshTtl: const Duration(seconds: -1)));
      expect(await store.loadRefreshToken(), isNull);

      final raw = await SharedPreferences.getInstance();
      expect(raw.getString('texas.session.refresh_token'), isNull);
    });

    test('清除后读不到', () async {
      final store = SessionStore(persistent: true);
      await store.save(_session());
      await store.clear();
      expect(await store.loadRefreshToken(), isNull);
    });

    test('只存刷新令牌，不存访问令牌和密码', () async {
      // 访问令牌十几分钟就过期，存了也用不上；密码永远不存
      final store = SessionStore(persistent: true);
      await store.save(_session());
      final raw = await SharedPreferences.getInstance();
      final values = raw.getKeys().map(raw.get).map((value) => '$value');
      expect(values, isNot(contains('access_1')));
      expect(raw.getKeys().where((key) => key.contains('password')), isEmpty);
    });
    test('清除之后再保存仍会写入：调用方必须自己保证不在登出后保存', () async {
      // SessionStore 不记住「已登出」——那是会话层的职责。poker_app 里刷新
      // 成功后的保存与「当前会话是否还是它」的判断放在同一个同步块内，
      // 就是为了让登出后在飞的保存不会把令牌写回去。
      final store = SessionStore(persistent: true);
      await store.save(_session());
      await store.clear();
      await store.save(_session(refreshToken: 'resurrected'));
      expect(
        await store.loadRefreshToken(),
        'resurrected',
        reason: '存储层照实写入，防复活由调用方负责',
      );
    });
    test('过期时间按 UTC 存，换时区读回来不会误判为过期', () async {
      // 本地时间的 ISO 串不带时区后缀。玩家飞到另一个时区，一个还有效的
      // 令牌会被当成过期直接清掉，白白要求重新登录。
      final store = SessionStore(persistent: true);
      final expiresAt = DateTime.now().add(const Duration(days: 30));
      await store.save(_session(refreshTtl: const Duration(days: 30)));

      final raw = await SharedPreferences.getInstance();
      final stored = raw.getString('texas.session.refresh_expires_at')!;
      expect(stored, endsWith('Z'), reason: '必须是 UTC 串');
      expect(
        DateTime.parse(stored).difference(expiresAt.toUtc()).inSeconds.abs(),
        lessThan(2),
        reason: '换算不能改变时刻本身',
      );
    });
  });
}

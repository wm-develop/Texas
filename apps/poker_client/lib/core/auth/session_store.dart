import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:poker_client/core/auth/auth_session.dart';

/// 把刷新令牌留在设备上，让「回来还要重新登录」不再发生。
///
/// 此前登录态只存在内存里：进程一被系统回收（在移动端上出去回条微信就可能
/// 发生），甚至用户主动划掉应用再打开，都要重新输一次账号密码。而牌局本身
/// 从 0.5.0 起连服务端重启都能续上，客户端却因为这一处停在登录页。
///
/// 只存刷新令牌，不存访问令牌：访问令牌十几分钟就过期，存了也用不上；刷新
/// 令牌有效期 30 天，拿它向服务端换一份新会话即可。也不存密码。
///
/// 存储用应用私有的 shared_preferences，内容是明文。这是权衡后的选择：本项目
/// 是熟人私人牌局、不涉真钱，应用私有目录在未 root 的设备上其他应用读不到；
/// 而更严格的安全存储在 HarmonyOS 上没有可用适配（本项目连 shared_preferences
/// 都用的是 OH 适配分支）。若将来引入更敏感的数据，这里要换成安全存储。
///
/// **Web 端一律不持久化。** 那里的 shared_preferences 落在 localStorage，
/// 而浏览器常常是公用的：网吧、办公室、家里的台式机，下一个打开页面的人会
/// 直接进入上一个人的账号。移动端的设备通常属于一个人，风险完全不同。
/// Web 用户关掉标签页要重新登录，这是刻意的。
class SessionStore {
  SessionStore({SharedPreferences? preferences, bool? persistent})
    : _preferences = preferences,
      _persistent = persistent ?? !kIsWeb;

  /// 是否真的落盘。Web 上为 false，所有读写都成为空操作。
  final bool _persistent;

  static const _refreshTokenKey = 'texas.session.refresh_token';
  static const _refreshExpiresAtKey = 'texas.session.refresh_expires_at';

  SharedPreferences? _preferences;

  Future<SharedPreferences?> _instance() async {
    if (!_persistent) return null;
    if (_preferences != null) return _preferences;
    try {
      _preferences = await SharedPreferences.getInstance();
    } on Object {
      // 没有 preferences 插件的平台照常运行，只是记不住登录态
      return null;
    }
    return _preferences;
  }

  /// 读出仍然可用的刷新令牌；没有、已过期或读取失败都返回 null。
  Future<String?> loadRefreshToken() async {
    final preferences = await _instance();
    if (preferences == null) return null;
    final token = preferences.getString(_refreshTokenKey);
    if (token == null || token.isEmpty) return null;
    final expiresAt = preferences.getString(_refreshExpiresAtKey);
    if (expiresAt != null) {
      final parsed = DateTime.tryParse(expiresAt)?.toUtc();
      // 明显过期就别再去换了：那次请求注定失败，只会让启动多等一个往返
      if (parsed != null && !parsed.isAfter(DateTime.now().toUtc())) {
        await clear();
        return null;
      }
    }
    return token;
  }

  /// 保存会话里的刷新令牌。
  ///
  /// 服务端每次刷新都会轮换令牌，所以登录之后的每一次刷新都要再存一遍，
  /// 否则存着的那个会在下次启动时已经作废。
  Future<void> save(AuthSession session) async {
    final preferences = await _instance();
    if (preferences == null) return;
    try {
      await preferences.setString(_refreshTokenKey, session.refreshToken);
      // 存 UTC：本地时间的 ISO 串不带时区后缀，换一台时区不同的设备读回来
      // 会整体偏移几小时，一个还有效的令牌可能被当成过期直接清掉。
      await preferences.setString(
        _refreshExpiresAtKey,
        session.refreshExpiresAt.toUtc().toIso8601String(),
      );
    } on Object {
      // 存不下来只是下次要重新登录，不该影响当前这次登录
    }
  }

  /// 清除登录态。登出、注销、令牌失效时都必须调用。
  Future<void> clear() async {
    final preferences = await _instance();
    if (preferences == null) return;
    try {
      await preferences.remove(_refreshTokenKey);
      await preferences.remove(_refreshExpiresAtKey);
    } on Object {
      // 忽略：下次启动时用这个令牌换会话会被服务端拒绝，届时同样会清除
    }
  }
}

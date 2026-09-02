import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/features/admin/domain/audit_event.dart';

/// 管理审计查询：按时间倒序展示管理操作、账号变更和语音加入/退出记录。
class AdminAuditPage extends StatefulWidget {
  const AdminAuditPage({required this.loader, super.key});

  /// 拉取审计记录；[userId] 非空时只返回与该用户相关的事件。
  final Future<AuditLog> Function({String userId}) loader;

  @override
  State<AdminAuditPage> createState() => _AdminAuditPageState();
}

class _AdminAuditPageState extends State<AdminAuditPage> {
  final _search = TextEditingController();
  AuditLog _log = const AuditLog(events: [], users: {});
  _AuditCategory _category = _AuditCategory.all;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final log = await widget.loader();
      if (mounted) setState(() => _log = log);
    } on Object catch (error) {
      if (mounted) setState(() => _error = _message(error));
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  List<AuditEvent> get _visibleEvents {
    final query = _search.text.trim().toLowerCase();
    return _log.events.where((event) {
      if (!_category.matches(event.eventType)) return false;
      if (query.isEmpty) return true;
      final haystack = [
        _log.describeUser(event.actorUserId),
        describeAuditEvent(event, _log),
        event.roomId,
        event.metadata['roomCode']?.toString() ?? '',
      ].join(' ').toLowerCase();
      return haystack.contains(query);
    }).toList(growable: false);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('审计记录'),
        actions: [
          IconButton(
            onPressed: _loading ? null : _load,
            icon: const Icon(Icons.refresh),
            tooltip: '刷新',
          ),
          const SizedBox(width: 8),
        ],
      ),
      body: DecoratedBox(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            colors: [Color(0xFF16473B), Color(0xFF061814)],
            radius: 1.2,
          ),
        ),
        child: SafeArea(
          child: Column(
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
                child: TextField(
                  controller: _search,
                  onChanged: (_) => setState(() {}),
                  decoration: const InputDecoration(
                    prefixIcon: Icon(Icons.search),
                    hintText: '按用户名、昵称、房间号或内容筛选',
                    isDense: true,
                    border: OutlineInputBorder(),
                  ),
                ),
              ),
              SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Row(
                  children: [
                    for (final category in _AuditCategory.values)
                      Padding(
                        padding: const EdgeInsets.only(right: 8, top: 6),
                        child: ChoiceChip(
                          label: Text(category.label),
                          selected: _category == category,
                          onSelected: (_) =>
                              setState(() => _category = category),
                        ),
                      ),
                  ],
                ),
              ),
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                  child: Text(
                    _error!,
                    style: const TextStyle(color: Colors.redAccent),
                  ),
                ),
              Expanded(
                child: _loading
                    ? const Center(child: CircularProgressIndicator())
                    : _buildList(),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildList() {
    final events = _visibleEvents;
    if (events.isEmpty) {
      return const Center(
        child: Text('没有匹配的审计记录', style: TextStyle(color: Colors.white70)),
      );
    }
    return ListView.separated(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
      itemCount: events.length,
      separatorBuilder: (_, _) => const SizedBox(height: 6),
      itemBuilder: (context, index) {
        final event = events[index];
        return Card(
          child: ListTile(
            dense: true,
            leading: Icon(auditEventIcon(event.eventType)),
            title: Text(describeAuditEvent(event, _log)),
            subtitle: Text(
              '${_formatLocalTime(event.createdAt)} · '
              '操作者 ${_log.describeUser(event.actorUserId)}'
              '${event.roomId.isEmpty ? '' : ' · 房间 ${event.metadata['roomCode'] ?? event.roomId}'}',
            ),
          ),
        );
      },
    );
  }

  String _message(Object error) {
    if (error is GameApiTimeoutException) return '服务器响应超时，请稍后刷新';
    if (error is GameApiException) {
      return switch (error.code) {
        'admin_required' => '当前账号没有管理员权限',
        _ => '加载审计记录失败（${error.code}）',
      };
    }
    return '无法连接游戏服务';
  }
}

enum _AuditCategory {
  all('全部'),
  admin('管理操作'),
  account('账号变更'),
  voice('语音进出');

  const _AuditCategory(this.label);

  final String label;

  bool matches(String eventType) => switch (this) {
    _AuditCategory.all => true,
    _AuditCategory.admin => eventType.startsWith('admin.'),
    _AuditCategory.account =>
      eventType.startsWith('user.') || eventType.startsWith('account.'),
    _AuditCategory.voice => eventType.startsWith('voice.'),
  };
}

IconData auditEventIcon(String eventType) => switch (eventType) {
  'voice.joined' => Icons.mic,
  'voice.left' => Icons.mic_off_outlined,
  'account.self_deleted' => Icons.person_remove_outlined,
  'admin.wallet_changed' => Icons.toll,
  'admin.user_removed_from_room' => Icons.exit_to_app,
  'admin.user_status_changed' => Icons.manage_accounts_outlined,
  'admin.user_created' => Icons.person_add_alt_1,
  'admin.password_reset' || 'user.password_changed' => Icons.password_outlined,
  'admin.registration_changed' => Icons.how_to_reg_outlined,
  'admin.bootstrap' => Icons.verified_user_outlined,
  _ => Icons.history,
};

/// 把服务端审计事件翻译为一句中文。
String describeAuditEvent(AuditEvent event, AuditLog log) {
  final metadata = event.metadata;
  String user(String key) => log.describeUser(metadata[key]?.toString() ?? '');
  switch (event.eventType) {
    case 'admin.bootstrap':
      return '初始化首位管理员';
    case 'admin.user_created':
      return '创建账号 ${metadata['username'] ?? user('targetUserId')}';
    case 'admin.user_status_changed':
      final targets = (metadata['targetUserIds'] as List<dynamic>? ?? const [])
          .map((value) => log.describeUser(value.toString()))
          .join('、');
      return '将 $targets 设为${_statusLabel(metadata['status']?.toString())}';
    case 'admin.password_reset':
      return '重置 ${user('targetUserId')} 的密码';
    case 'admin.wallet_changed':
      return '把 ${user('targetUserId')} 的钱包调整为 ${metadata['walletChips']} 筹码';
    case 'admin.user_removed_from_room':
      return '把 ${user('targetUserId')} 请出房间 ${metadata['roomCode'] ?? ''}';
    case 'admin.registration_changed':
      return metadata['enabled'] == true ? '开放新用户注册' : '关闭新用户注册';
    case 'admin.username_changed':
    case 'user.username_changed':
      return '${user('targetUserId')} 的用户名由 ${metadata['oldUsername']} 改为 ${metadata['newUsername']}';
    case 'user.display_name_changed':
      return '牌桌昵称由 ${metadata['oldDisplayName']} 改为 ${metadata['newDisplayName']}';
    case 'user.password_changed':
      return '修改了自己的密码';
    case 'account.self_deleted':
      return '注销账号（原用户名 ${metadata['previousUsername']}，'
          '${metadata['transferredChips']} 筹码转入 ${user('recipientUserId')}）';
    case 'voice.joined':
      return metadata['microphoneEnabled'] == true ? '加入语音（开麦）' : '加入语音（闭麦）';
    case 'voice.left':
      return switch (metadata['reason']) {
        'disconnected' => '退出语音（连接断开）',
        'left_table' => '退出语音（离开牌桌）',
        _ => '退出语音',
      };
    default:
      return event.eventType;
  }
}

String _statusLabel(String? status) => switch (status) {
  'active' => '正常',
  'suspended' => '停用',
  'deleted' => '已删除',
  _ => status ?? '未知状态',
};

String _formatLocalTime(DateTime value) {
  final local = value.toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${local.year}-${two(local.month)}-${two(local.day)} '
      '${two(local.hour)}:${two(local.minute)}:${two(local.second)}';
}

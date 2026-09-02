/// 管理审计事件（含语音加入/退出元数据）。
class AuditEvent {
  const AuditEvent({
    required this.eventId,
    required this.actorUserId,
    required this.roomId,
    required this.eventType,
    required this.metadata,
    required this.createdAt,
  });

  final String eventId;
  final String actorUserId;
  final String roomId;
  final String eventType;
  final Map<String, dynamic> metadata;
  final DateTime createdAt;

  factory AuditEvent.fromJson(Map<String, dynamic> json) => AuditEvent(
    eventId: json['eventId'] as String,
    actorUserId: json['actorUserId'] as String? ?? '',
    roomId: json['roomId'] as String? ?? '',
    eventType: json['eventType'] as String,
    metadata: (json['metadata'] as Map<String, dynamic>?) ?? const {},
    createdAt: DateTime.parse(json['createdAt'] as String),
  );
}

/// 审计事件中出现过的用户的显示信息。
class AuditUser {
  const AuditUser({required this.username, required this.displayName});

  final String username;
  final String displayName;

  factory AuditUser.fromJson(Map<String, dynamic> json) => AuditUser(
    username: json['username'] as String,
    displayName: json['displayName'] as String,
  );
}

class AuditLog {
  const AuditLog({required this.events, required this.users});

  final List<AuditEvent> events;
  final Map<String, AuditUser> users;

  factory AuditLog.fromJson(Map<String, dynamic> json) => AuditLog(
    events: (json['events'] as List<dynamic>? ?? const [])
        .map((value) => AuditEvent.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    users: {
      for (final entry
          in (json['users'] as Map<String, dynamic>? ?? const {}).entries)
        entry.key: AuditUser.fromJson(entry.value as Map<String, dynamic>),
    },
  );

  /// 把 user_id 显示为「昵称（用户名）」；未知用户退回 user_id。
  String describeUser(String userId) {
    if (userId.isEmpty) return '系统';
    final user = users[userId];
    if (user == null) return userId;
    return '${user.displayName}（${user.username}）';
  }
}

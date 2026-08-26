class ManagedUser {
  const ManagedUser({
    required this.userId,
    required this.username,
    required this.displayName,
    required this.role,
    required this.status,
    required this.walletChips,
    required this.tableChips,
    required this.tableId,
    required this.createdAt,
  });

  final String userId;
  final String username;
  final String displayName;
  final String role;
  final String status;
  final int walletChips;
  final int tableChips;
  final String tableId;
  final DateTime createdAt;

  bool get isAdmin => role == 'admin';
  bool get isActive => status == 'active';
  bool get isInRoom => tableId.isNotEmpty;

  factory ManagedUser.fromJson(Map<String, dynamic> json) => ManagedUser(
    userId: json['userId'] as String,
    username: json['username'] as String,
    displayName: json['displayName'] as String,
    role: json['role'] as String? ?? 'player',
    status: json['status'] as String? ?? 'active',
    walletChips: (json['walletChips'] as num?)?.toInt() ?? 0,
    tableChips: (json['tableChips'] as num?)?.toInt() ?? 0,
    tableId: json['tableId'] as String? ?? '',
    createdAt: DateTime.parse(json['createdAt'] as String),
  );
}

class AppUser {
  const AppUser({
    required this.userId,
    required this.username,
    required this.displayName,
    this.role = 'player',
    this.status = 'active',
  });

  final String userId;
  final String username;
  final String displayName;
  final String role;
  final String status;

  bool get isAdmin => role == 'admin';

  factory AppUser.fromJson(Map<String, dynamic> json) => AppUser(
    userId: json['userId'] as String,
    username: json['username'] as String,
    displayName: json['displayName'] as String,
    role: json['role'] as String? ?? 'player',
    status: json['status'] as String? ?? 'active',
  );
}

class AuthSession {
  const AuthSession({
    required this.user,
    required this.accessToken,
    required this.refreshToken,
    required this.accessExpiresAt,
    required this.refreshExpiresAt,
  });

  final AppUser user;
  final String accessToken;
  final String refreshToken;
  final DateTime accessExpiresAt;
  final DateTime refreshExpiresAt;

  factory AuthSession.fromJson(Map<String, dynamic> json) => AuthSession(
    user: AppUser.fromJson(json['user'] as Map<String, dynamic>),
    accessToken: json['accessToken'] as String,
    refreshToken: json['refreshToken'] as String,
    accessExpiresAt: DateTime.parse(json['accessExpiresAt'] as String),
    refreshExpiresAt: DateTime.parse(json['refreshExpiresAt'] as String),
  );
}

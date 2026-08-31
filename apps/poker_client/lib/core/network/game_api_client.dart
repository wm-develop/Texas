import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/features/admin/domain/managed_user.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_entry.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_snapshot.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';

class GameApiException implements Exception {
  const GameApiException(this.code, {required this.statusCode});

  final String code;
  final int statusCode;

  @override
  String toString() => 'GameApiException($statusCode, $code)';
}

class GameApiTimeoutException implements Exception {
  const GameApiTimeoutException(this.path, this.timeout);

  final String path;
  final Duration timeout;

  @override
  String toString() => 'GameApiTimeoutException($path, ${timeout.inSeconds}s)';
}

class GameApiClient {
  GameApiClient({
    Uri? serverBaseUri,
    http.Client? httpClient,
    this.requestTimeout = const Duration(seconds: 30),
  }) : _serverBaseUri = _withTrailingSlash(
         serverBaseUri ??
             Uri.parse(
               const String.fromEnvironment(
                 'GAME_HTTP_SERVER_URL',
                 defaultValue: 'http://127.0.0.1:8080',
               ),
             ),
       ),
       _httpClient = httpClient ?? http.Client(),
       _ownsClient = httpClient == null;

  final Uri _serverBaseUri;
  final http.Client _httpClient;
  final bool _ownsClient;
  final Duration requestTimeout;

  Future<AuthSession> register({
    required String username,
    required String displayName,
    required String password,
    bool requestAdmin = false,
  }) async => AuthSession.fromJson(
    await _request(
      'v1/auth/register',
      body: {
        'username': username,
        'displayName': displayName,
        'password': password,
        if (requestAdmin) 'requestAdmin': true,
      },
      expectedStatus: 201,
    ),
  );

  Future<List<ManagedUser>> adminUsers(String accessToken) async {
    final payload = await _get('v1/admin/users', token: accessToken);
    return (payload['users'] as List<dynamic>? ?? const [])
        .map((value) => ManagedUser.fromJson(value as Map<String, dynamic>))
        .toList(growable: false);
  }

  Future<ManagedUser> adminCreateUser({
    required String accessToken,
    required String username,
    required String displayName,
    required String password,
  }) async => ManagedUser.fromJson(
    await _request(
      'v1/admin/users',
      token: accessToken,
      body: {
        'username': username,
        'displayName': displayName,
        'password': password,
      },
      expectedStatus: 201,
    ),
  );

  Future<void> adminSetUserStatus({
    required String accessToken,
    required List<String> userIds,
    required String status,
  }) async {
    await _request(
      'v1/admin/users/status',
      token: accessToken,
      body: {'userIds': userIds, 'status': status},
      expectedStatus: 204,
    );
  }

  Future<void> adminResetPassword({
    required String accessToken,
    required String userId,
    required String password,
  }) async {
    await _request(
      'v1/admin/users/$userId/password',
      token: accessToken,
      body: {'password': password},
      expectedStatus: 204,
    );
  }

  Future<bool> adminSetChatMuted({
    required String accessToken,
    required String userId,
    required bool muted,
  }) async =>
      (await _request(
            'v1/admin/users/$userId/chat-mute',
            token: accessToken,
            body: {'muted': muted},
          ))['muted']
          as bool;

  Future<AppUser> adminUpdateUsername({
    required String accessToken,
    required String userId,
    required String username,
  }) async => AppUser.fromJson(
    await _request(
      'v1/admin/users/$userId/username',
      token: accessToken,
      body: {'username': username},
    ),
  );

  Future<BankrollSnapshot> adminSetWallet({
    required String accessToken,
    required String userId,
    required int chips,
    required String requestId,
  }) async => BankrollSnapshot.fromJson(
    await _request(
      'v1/admin/users/$userId/wallet',
      token: accessToken,
      body: {'chips': chips, 'requestId': requestId},
    ),
  );

  Future<bool> adminLeaveRoom({
    required String accessToken,
    required String userId,
  }) async =>
      (await _request(
            'v1/admin/users/$userId/leave-room',
            token: accessToken,
            body: const {},
          ))['closed']
          as bool? ??
      false;

  Future<bool> adminRegistrationEnabled(String accessToken) async =>
      (await _get(
            'v1/admin/settings/registration',
            token: accessToken,
          ))['enabled']
          as bool;

  Future<bool> adminSetRegistrationEnabled({
    required String accessToken,
    required bool enabled,
  }) async =>
      (await _request(
            'v1/admin/settings/registration',
            token: accessToken,
            body: {'enabled': enabled},
          ))['enabled']
          as bool;

  Future<AuthSession> login({
    required String username,
    required String password,
  }) async => AuthSession.fromJson(
    await _request(
      'v1/auth/login',
      body: {'username': username, 'password': password},
    ),
  );

  Future<AuthSession> refresh(String refreshToken) async =>
      AuthSession.fromJson(
        await _request('v1/auth/refresh', body: {'refreshToken': refreshToken}),
      );

  Future<void> logout(String accessToken) async {
    await _request(
      'v1/auth/logout',
      token: accessToken,
      body: const {},
      expectedStatus: 204,
    );
  }

  Future<void> heartbeat(String accessToken) async {
    await _request(
      'v1/users/me/heartbeat',
      token: accessToken,
      body: const {},
      expectedStatus: 204,
    );
  }

  Future<AppUser> updateUsername({
    required String accessToken,
    required String username,
  }) async => AppUser.fromJson(
    await _request(
      'v1/users/me/username',
      token: accessToken,
      body: {'username': username},
    ),
  );

  Future<AppUser> updateDisplayName({
    required String accessToken,
    required String displayName,
  }) async => AppUser.fromJson(
    await _request(
      'v1/users/me/display-name',
      token: accessToken,
      body: {'displayName': displayName},
    ),
  );

  Future<AuthSession> changePassword({
    required String accessToken,
    required String currentPassword,
    required String newPassword,
  }) async => AuthSession.fromJson(
    await _request(
      'v1/users/me/password',
      token: accessToken,
      body: {'currentPassword': currentPassword, 'newPassword': newPassword},
    ),
  );

  Future<FriendRoom> createRoom({
    required String accessToken,
    required String preset,
    required String password,
    required int smallBlind,
    required int bigBlind,
    required int maxBuyIn,
    required int buyIn,
    required String requestId,
  }) async => FriendRoom.fromJson(
    await _request(
      'v1/rooms',
      token: accessToken,
      body: {
        'preset': preset,
        'password': password,
        'smallBlind': smallBlind,
        'bigBlind': bigBlind,
        'maxBuyIn': maxBuyIn,
        'buyIn': buyIn,
        'requestId': requestId,
      },
      expectedStatus: 201,
    ),
  );

  Future<FriendRoom> joinRoom({
    required String accessToken,
    required String code,
    required String password,
    required int buyIn,
    required String requestId,
  }) async => FriendRoom.fromJson(
    await _request(
      'v1/rooms/join',
      token: accessToken,
      body: {
        'code': code,
        'password': password,
        'buyIn': buyIn,
        'requestId': requestId,
      },
    ),
  );

  Future<RoomPreview> roomPreview({
    required String accessToken,
    required String code,
  }) async => RoomPreview.fromJson(
    await _get('v1/rooms/preview?code=$code', token: accessToken),
  );

  Future<FriendRoom?> currentRoom(String accessToken) async {
    try {
      return FriendRoom.fromJson(
        await _get('v1/rooms/current', token: accessToken),
      );
    } on GameApiException catch (error) {
      if (error.code == 'room_not_found') return null;
      rethrow;
    }
  }

  Future<BankrollSnapshot> bankroll(String accessToken) async =>
      BankrollSnapshot.fromJson(await _get('v1/bankroll', token: accessToken));

  Future<BankrollSnapshot> topUp({
    required String accessToken,
    required String requestId,
    required int amount,
  }) async => BankrollSnapshot.fromJson(
    await _request(
      'v1/bankroll/top-ups',
      token: accessToken,
      body: {'requestId': requestId, 'amount': amount},
    ),
  );

  Future<List<BankrollEntry>> bankrollEntries({
    required String accessToken,
    int limit = 30,
  }) async {
    final payload = await _get(
      'v1/bankroll/entries?limit=$limit',
      token: accessToken,
    );
    return (payload['entries'] as List<dynamic>? ?? const [])
        .map((value) => BankrollEntry.fromJson(value as Map<String, dynamic>))
        .toList(growable: false);
  }

  Future<void> leaveRoom(String accessToken) async {
    await _request('v1/rooms/leave', token: accessToken, body: const {});
  }

  Future<List<RecentHand>> recentHands({
    required String accessToken,
    int limit = 20,
  }) async {
    final payload = await _get(
      'v1/hands/recent?limit=$limit',
      token: accessToken,
    );
    return (payload['hands'] as List<dynamic>? ?? const [])
        .map((value) => RecentHand.fromJson(value as Map<String, dynamic>))
        .toList(growable: false);
  }

  Future<Map<String, dynamic>> _request(
    String path, {
    required Map<String, Object?> body,
    String? token,
    int expectedStatus = 200,
  }) async {
    final headers = <String, String>{
      'content-type': 'application/json; charset=utf-8',
      'accept': 'application/json',
    };
    if (token != null) headers['authorization'] = 'Bearer $token';
    late final http.Response response;
    try {
      response = await _httpClient
          .post(
            _serverBaseUri.resolve(path),
            headers: headers,
            body: jsonEncode(body),
          )
          .timeout(requestTimeout);
    } on TimeoutException {
      throw GameApiTimeoutException(path, requestTimeout);
    }
    if (response.statusCode == expectedStatus && expectedStatus == 204) {
      return const {};
    }
    final decoded = _decode(response.bodyBytes);
    if (response.statusCode != expectedStatus) {
      throw GameApiException(
        decoded['error'] as String? ?? 'request_failed',
        statusCode: response.statusCode,
      );
    }
    return decoded;
  }

  Future<Map<String, dynamic>> _get(
    String path, {
    required String token,
  }) async {
    late final http.Response response;
    try {
      response = await _httpClient
          .get(
            _serverBaseUri.resolve(path),
            headers: {
              'accept': 'application/json',
              'authorization': 'Bearer $token',
            },
          )
          .timeout(requestTimeout);
    } on TimeoutException {
      throw GameApiTimeoutException(path, requestTimeout);
    }
    final decoded = _decode(response.bodyBytes);
    if (response.statusCode != 200) {
      throw GameApiException(
        decoded['error'] as String? ?? 'request_failed',
        statusCode: response.statusCode,
      );
    }
    return decoded;
  }

  void close() {
    if (_ownsClient) _httpClient.close();
  }
}

Map<String, dynamic> _decode(List<int> bytes) {
  try {
    final value = jsonDecode(utf8.decode(bytes));
    if (value is Map<String, dynamic>) return value;
  } on FormatException {
    // Converted to one stable client error below.
  }
  throw const GameApiException('invalid_server_response', statusCode: 0);
}

Uri _withTrailingSlash(Uri uri) =>
    uri.path.endsWith('/') ? uri : uri.replace(path: '${uri.path}/');

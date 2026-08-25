import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';

class GameApiException implements Exception {
  const GameApiException(this.code, {required this.statusCode});

  final String code;
  final int statusCode;

  @override
  String toString() => 'GameApiException($statusCode, $code)';
}

class GameApiClient {
  GameApiClient({Uri? serverBaseUri, http.Client? httpClient})
    : _serverBaseUri = _withTrailingSlash(
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

  Future<AuthSession> register({
    required String username,
    required String displayName,
    required String password,
  }) async => AuthSession.fromJson(
    await _request(
      'v1/auth/register',
      body: {
        'username': username,
        'displayName': displayName,
        'password': password,
      },
      expectedStatus: 201,
    ),
  );

  Future<AuthSession> login({
    required String username,
    required String password,
  }) async => AuthSession.fromJson(
    await _request(
      'v1/auth/login',
      body: {'username': username, 'password': password},
    ),
  );

  Future<FriendRoom> createRoom({
    required String accessToken,
    required String preset,
    required int maxPlayers,
    required String password,
  }) async => FriendRoom.fromJson(
    await _request(
      'v1/rooms',
      token: accessToken,
      body: {'preset': preset, 'maxPlayers': maxPlayers, 'password': password},
      expectedStatus: 201,
    ),
  );

  Future<FriendRoom> joinRoom({
    required String accessToken,
    required String code,
    required String password,
  }) async => FriendRoom.fromJson(
    await _request(
      'v1/rooms/join',
      token: accessToken,
      body: {'code': code, 'password': password},
    ),
  );

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
    final response = await _httpClient
        .post(
          _serverBaseUri.resolve(path),
          headers: headers,
          body: jsonEncode(body),
        )
        .timeout(const Duration(seconds: 10));
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
    final response = await _httpClient
        .get(
          _serverBaseUri.resolve(path),
          headers: {
            'accept': 'application/json',
            'authorization': 'Bearer $token',
          },
        )
        .timeout(const Duration(seconds: 10));
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

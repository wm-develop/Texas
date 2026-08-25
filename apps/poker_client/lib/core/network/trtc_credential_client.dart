import 'dart:convert';

import 'package:http/http.dart' as http;

class TrtcCredentials {
  const TrtcCredentials({
    required this.sdkAppId,
    required this.userId,
    required this.roomId,
    required this.userSig,
    required this.expireIn,
  });

  final int sdkAppId;
  final String userId;
  final String roomId;
  final String userSig;
  final int expireIn;

  factory TrtcCredentials.fromJson(Map<String, Object?> json) {
    return TrtcCredentials(
      sdkAppId: _requiredInt(json, 'sdkAppId'),
      userId: _requiredString(json, 'userId'),
      roomId: _requiredString(json, 'roomId'),
      userSig: _requiredString(json, 'userSig'),
      expireIn: _requiredInt(json, 'expireIn'),
    );
  }
}

class TrtcCredentialException implements Exception {
  const TrtcCredentialException(this.message, {this.statusCode});

  final String message;
  final int? statusCode;

  @override
  String toString() => 'TrtcCredentialException($message)';
}

class TrtcCredentialClient {
  TrtcCredentialClient({
    required Uri serverBaseUri,
    http.Client? httpClient,
    this.debugToken,
  }) : _serverBaseUri = _withTrailingSlash(serverBaseUri),
       _httpClient = httpClient ?? http.Client(),
       _ownsClient = httpClient == null;

  final Uri _serverBaseUri;
  final http.Client _httpClient;
  final bool _ownsClient;

  /// Only for a developer connecting to a remote debug server.
  /// Production authentication must come from the signed-in user session.
  final String? debugToken;

  Future<TrtcCredentials> issue({
    required String userId,
    required String roomId,
    String? accessToken,
  }) async {
    final headers = <String, String>{
      'content-type': 'application/json; charset=utf-8',
      'accept': 'application/json',
    };
    final token = accessToken?.trim().isNotEmpty == true
        ? accessToken!.trim()
        : debugToken?.trim();
    if (token != null && token.isNotEmpty) {
      headers['authorization'] = 'Bearer $token';
    }

    final response = await _httpClient
        .post(
          _serverBaseUri.resolve('v1/trtc/credentials'),
          headers: headers,
          body: jsonEncode({'userId': userId, 'roomId': roomId}),
        )
        .timeout(const Duration(seconds: 10));

    if (response.statusCode != 200) {
      throw TrtcCredentialException(
        _readServerError(response.body),
        statusCode: response.statusCode,
      );
    }

    final Object? decoded;
    try {
      decoded = jsonDecode(utf8.decode(response.bodyBytes));
    } on FormatException catch (error) {
      throw TrtcCredentialException('invalid_server_response: $error');
    }
    if (decoded is! Map<String, Object?>) {
      throw const TrtcCredentialException('invalid_server_response');
    }
    return TrtcCredentials.fromJson(decoded);
  }

  void close() {
    if (_ownsClient) _httpClient.close();
  }
}

Uri _withTrailingSlash(Uri uri) {
  if (uri.path.endsWith('/')) return uri;
  return uri.replace(path: '${uri.path}/');
}

String _readServerError(String body) {
  try {
    final decoded = jsonDecode(body);
    if (decoded is Map<String, Object?> && decoded['error'] is String) {
      return decoded['error']! as String;
    }
  } on FormatException {
    // Fall through to the stable error below.
  }
  return 'credential_request_failed';
}

String _requiredString(Map<String, Object?> json, String key) {
  final value = json[key];
  if (value is String && value.isNotEmpty) return value;
  throw TrtcCredentialException('missing_or_invalid_$key');
}

int _requiredInt(Map<String, Object?> json, String key) {
  final value = json[key];
  if (value is int && value > 0) return value;
  throw TrtcCredentialException('missing_or_invalid_$key');
}

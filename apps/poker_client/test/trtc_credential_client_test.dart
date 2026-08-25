import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:poker_client/core/network/trtc_credential_client.dart';

void main() {
  test('requests and parses short-lived TRTC credentials', () async {
    final mock = MockClient((request) async {
      expect(request.method, 'POST');
      expect(
        request.url.toString(),
        'http://localhost:8080/v1/trtc/credentials',
      );
      expect(jsonDecode(request.body), {
        'userId': 'player_1',
        'roomId': 'table_7',
      });
      return http.Response(
        jsonEncode({
          'sdkAppId': 1400000000,
          'userId': 'player_1',
          'roomId': 'table_7',
          'userSig': 'short-lived-sig',
          'expireIn': 3600,
        }),
        200,
        headers: {'content-type': 'application/json'},
      );
    });
    final client = TrtcCredentialClient(
      serverBaseUri: Uri.parse('http://localhost:8080'),
      httpClient: mock,
    );

    final credentials = await client.issue(
      userId: 'player_1',
      roomId: 'table_7',
    );

    expect(credentials.sdkAppId, 1400000000);
    expect(credentials.userSig, 'short-lived-sig');
    expect(credentials.expireIn, 3600);
  });

  test(
    'surfaces a stable server error without leaking response details',
    () async {
      final client = TrtcCredentialClient(
        serverBaseUri: Uri.parse('https://example.invalid/game/'),
        httpClient: MockClient(
          (_) async =>
              http.Response(jsonEncode({'error': 'unauthorized'}), 401),
        ),
      );

      await expectLater(
        client.issue(userId: 'player_1', roomId: 'table_7'),
        throwsA(
          isA<TrtcCredentialException>()
              .having((error) => error.statusCode, 'statusCode', 401)
              .having((error) => error.message, 'message', 'unauthorized'),
        ),
      );
    },
  );
}

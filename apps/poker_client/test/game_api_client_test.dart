import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:poker_client/core/network/game_api_client.dart';

void main() {
  test(
    'parses registration session without exposing transport details',
    () async {
      final client = GameApiClient(
        serverBaseUri: Uri.parse('http://game.test'),
        httpClient: MockClient((request) async {
          expect(request.url.path, '/v1/auth/register');
          expect(jsonDecode(request.body)['username'], 'friend_1');
          return http.Response.bytes(
            utf8.encode(
              jsonEncode({
                'user': {
                  'userId': 'usr_1',
                  'username': 'friend_1',
                  'displayName': '好友一',
                  'createdAt': '2026-08-24T00:00:00Z',
                },
                'accessToken': 'access-token',
                'refreshToken': 'refresh-token',
                'accessExpiresAt': '2026-08-25T00:00:00Z',
                'refreshExpiresAt': '2026-09-24T00:00:00Z',
              }),
            ),
            201,
            headers: {'content-type': 'application/json; charset=utf-8'},
          );
        }),
      );

      final session = await client.register(
        username: 'friend_1',
        displayName: '好友一',
        password: 'password-123',
      );

      expect(session.user.userId, 'usr_1');
      expect(session.accessToken, 'access-token');
    },
  );

  test('adds bearer token and parses a 2-10 player friend room', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        expect(request.headers['authorization'], 'Bearer access-token');
        return http.Response.bytes(
          utf8.encode(
            jsonEncode({
              'roomId': 'table_1',
              'code': '123456',
              'ownerUserId': 'usr_1',
              'preset': 'standard',
              'rules': {
                'startingChips': 2000,
                'smallBlind': 10,
                'bigBlind': 20,
                'actionSeconds': 20,
              },
              'maxPlayers': 10,
              'members': [
                {
                  'userId': 'usr_1',
                  'displayName': '好友一',
                  'seat': 1,
                  'ready': false,
                  'joinedAt': '2026-08-24T00:00:00Z',
                },
              ],
              'revision': 1,
              'createdAt': '2026-08-24T00:00:00Z',
            }),
          ),
          201,
          headers: {'content-type': 'application/json; charset=utf-8'},
        );
      }),
    );

    final room = await client.createRoom(
      accessToken: 'access-token',
      preset: 'standard',
      maxPlayers: 10,
      password: '',
    );

    expect(room.maxPlayers, 10);
    expect(room.members.single.displayName, '好友一');
  });

  test('loads personalized recent hand history', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        expect(request.method, 'GET');
        expect(request.url.path, '/v1/hands/recent');
        expect(request.headers['authorization'], 'Bearer access-token');
        return http.Response.bytes(
          utf8.encode(
            jsonEncode({
              'hands': [
                {
                  'handId': 'hand_1',
                  'roomCode': '123456',
                  'endedAt': '2026-08-25T01:00:00Z',
                  'board': ['As', 'Kd', 'Qc'],
                  'showdown': false,
                  'players': [
                    {
                      'userId': 'usr_1',
                      'displayName': '好友一',
                      'seat': 1,
                      'startingStack': 2000,
                      'endingStack': 2040,
                      'delta': 40,
                      'holeCards': ['Ah', 'Ad'],
                    },
                  ],
                },
              ],
            }),
          ),
          200,
        );
      }),
    );

    final hands = await client.recentHands(accessToken: 'access-token');

    expect(hands.single.players.single.delta, 40);
    expect(hands.single.players.single.holeCards, ['Ah', 'Ad']);
  });
}

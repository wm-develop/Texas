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
        final body = jsonDecode(request.body) as Map<String, dynamic>;
        expect(body['smallBlind'], 10);
        expect(body['bigBlind'], 20);
        expect(body['maxBuyIn'], 5000);
        expect(body['buyIn'], 2000);
        expect(body['requestId'], 'create-room-1');
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
                'maxBuyIn': 5000,
                'actionSeconds': 20,
              },
              'maxPlayers': 10,
              'members': [
                {
                  'userId': 'usr_1',
                  'displayName': '好友一',
                  'seat': 1,
                  'ready': false,
                  'stack': 2000,
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
      smallBlind: 10,
      bigBlind: 20,
      maxBuyIn: 5000,
      buyIn: 2000,
      requestId: 'create-room-1',
    );

    expect(room.maxPlayers, 10);
    expect(room.members.single.displayName, '好友一');
  });

  test('parses and sends an idempotent virtual top-up', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        expect(request.method, 'POST');
        expect(request.url.path, '/v1/bankroll/top-ups');
        expect(request.headers['authorization'], 'Bearer access-token');
        expect(jsonDecode(request.body), {
          'requestId': 'topup-1',
          'amount': 8888,
        });
        return http.Response(
          jsonEncode({
            'userId': 'usr_1',
            'walletChips': 8888,
            'tableChips': 0,
            'revision': 1,
          }),
          200,
        );
      }),
    );

    final bankroll = await client.topUp(
      accessToken: 'access-token',
      requestId: 'topup-1',
      amount: 8888,
    );

    expect(bankroll.walletChips, 8888);
    expect(bankroll.revision, 1);
  });

  test('loads newest entertainment chip ledger entries', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        expect(request.method, 'GET');
        expect(request.url.path, '/v1/bankroll/entries');
        expect(request.url.queryParameters['limit'], '30');
        return http.Response(
          jsonEncode({
            'entries': [
              {
                'entryId': 'bank_1',
                'reason': 'rebuy',
                'walletDelta': -500,
                'tableDelta': 500,
                'walletBalanceAfter': 4500,
                'tableBalanceAfter': 1500,
                'createdAt': '2026-08-25T10:00:00Z',
              },
            ],
          }),
          200,
        );
      }),
    );

    final entries = await client.bankrollEntries(accessToken: 'access-token');

    expect(entries.single.reason, 'rebuy');
    expect(entries.single.walletDelta, -500);
    expect(entries.single.tableBalanceAfter, 1500);
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

  test('loads the current room so a signed-in player can resume it', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        expect(request.method, 'GET');
        expect(request.url.path, '/v1/rooms/current');
        expect(request.headers['authorization'], 'Bearer access-token');
        return http.Response(
          jsonEncode({
            'roomId': 'table_1',
            'code': '654321',
            'ownerUserId': 'usr_1',
            'preset': 'standard',
            'rules': {
              'startingChips': 2000,
              'smallBlind': 10,
              'bigBlind': 20,
              'maxBuyIn': 5000,
              'actionSeconds': 20,
            },
            'maxPlayers': 6,
            'members': <Object?>[],
            'revision': 3,
          }),
          200,
        );
      }),
    );

    final room = await client.currentRoom('access-token');

    expect(room?.roomId, 'table_1');
    expect(room?.code, '654321');
  });

  test('returns null when the signed-in player has no current room', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient(
        (_) async =>
            http.Response(jsonEncode({'error': 'room_not_found'}), 404),
      ),
    );

    expect(await client.currentRoom('access-token'), isNull);
  });

  test('reports slow network timeouts separately from server errors', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      requestTimeout: const Duration(milliseconds: 5),
      httpClient: MockClient((_) async {
        await Future<void>.delayed(const Duration(milliseconds: 30));
        return http.Response('{}', 200);
      }),
    );

    await expectLater(
      client.login(username: 'friend_1', password: 'password-123'),
      throwsA(isA<GameApiTimeoutException>()),
    );
  });
}

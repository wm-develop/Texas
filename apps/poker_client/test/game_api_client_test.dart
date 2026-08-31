import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:poker_client/core/network/game_api_client.dart';

void main() {
  test('requests and parses the initial administrator registration', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        expect(request.url.path, '/v1/auth/register');
        final body = jsonDecode(request.body) as Map<String, dynamic>;
        expect(body.containsKey('maxPlayers'), isFalse);
        expect(body['username'], 'friend_1');
        expect(body['requestAdmin'], isTrue);
        return http.Response.bytes(
          utf8.encode(
            jsonEncode({
              'user': {
                'userId': 'usr_1',
                'username': 'friend_1',
                'displayName': '好友一',
                'role': 'admin',
                'status': 'active',
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
      requestAdmin: true,
    );

    expect(session.user.userId, 'usr_1');
    expect(session.accessToken, 'access-token');
    expect(session.user.isAdmin, isTrue);
  });

  test(
    'ordinary registration omits the optional administrator field',
    () async {
      final client = GameApiClient(
        serverBaseUri: Uri.parse('http://game.test'),
        httpClient: MockClient((request) async {
          final body = jsonDecode(request.body) as Map<String, dynamic>;
          expect(body.containsKey('requestAdmin'), isFalse);
          return http.Response(
            jsonEncode({
              'user': {
                'userId': 'usr_2',
                'username': 'friend_2',
                'displayName': '好友二',
                'createdAt': '2026-08-24T00:00:00Z',
              },
              'accessToken': 'access-token',
              'refreshToken': 'refresh-token',
              'accessExpiresAt': '2026-08-25T00:00:00Z',
              'refreshExpiresAt': '2026-09-24T00:00:00Z',
            }),
            201,
            headers: {'content-type': 'application/json; charset=utf-8'},
          );
        }),
      );

      final session = await client.register(
        username: 'friend_2',
        displayName: '好友二',
        password: 'password-123',
      );

      expect(session.user.isAdmin, isFalse);
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

  test('parses administrator presence and current room details', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        expect(request.method, 'GET');
        expect(request.url.path, '/v1/admin/users');
        expect(request.headers['authorization'], 'Bearer admin-token');
        return http.Response.bytes(
          utf8.encode(
            jsonEncode({
              'users': [
                {
                  'userId': 'usr_1',
                  'username': 'friend_1',
                  'displayName': '好友一',
                  'role': 'player',
                  'status': 'active',
                  'walletChips': 3000,
                  'tableChips': 2000,
                  'tableId': 'table_1',
                  'roomCode': '654321',
                  'online': true,
                  'chatMuted': true,
                  'createdAt': '2026-08-24T00:00:00Z',
                },
              ],
            }),
          ),
          200,
        );
      }),
    );

    final users = await client.adminUsers('admin-token');

    expect(users.single.online, isTrue);
    expect(users.single.roomCode, '654321');
    expect(users.single.isInRoom, isTrue);
    expect(users.single.chatMuted, isTrue);
  });

  test('sends administrator chat mute changes', () async {
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        expect(request.method, 'POST');
        expect(request.url.path, '/v1/admin/users/usr_1/chat-mute');
        expect(request.headers['authorization'], 'Bearer admin-token');
        expect(jsonDecode(request.body), {'muted': true});
        return http.Response(jsonEncode({'muted': true}), 200);
      }),
    );

    final muted = await client.adminSetChatMuted(
      accessToken: 'admin-token',
      userId: 'usr_1',
      muted: true,
    );

    expect(muted, isTrue);
  });

  test('sends personal profile changes and receives a fresh session', () async {
    var requestNumber = 0;
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        requestNumber++;
        expect(request.headers['authorization'], 'Bearer access-token');
        final body = jsonDecode(request.body) as Map<String, dynamic>;
        if (requestNumber == 1) {
          expect(request.url.path, '/v1/users/me/username');
          expect(body, {'username': 'new_login'});
          return http.Response.bytes(
            utf8.encode(
              jsonEncode({
                'userId': 'usr_1',
                'username': 'new_login',
                'displayName': '好友一',
                'role': 'player',
                'status': 'active',
                'createdAt': '2026-08-24T00:00:00Z',
              }),
            ),
            200,
          );
        }
        if (requestNumber == 2) {
          expect(request.url.path, '/v1/users/me/display-name');
          expect(body, {'displayName': '新的昵称'});
          return http.Response.bytes(
            utf8.encode(
              jsonEncode({
                'userId': 'usr_1',
                'username': 'new_login',
                'displayName': '新的昵称',
                'role': 'player',
                'status': 'active',
                'createdAt': '2026-08-24T00:00:00Z',
              }),
            ),
            200,
          );
        }
        expect(request.url.path, '/v1/users/me/password');
        expect(body, {
          'currentPassword': 'password-123',
          'newPassword': 'new-password-456',
        });
        return http.Response.bytes(
          utf8.encode(
            jsonEncode({
              'user': {
                'userId': 'usr_1',
                'username': 'new_login',
                'displayName': '好友一',
                'role': 'player',
                'status': 'active',
                'createdAt': '2026-08-24T00:00:00Z',
              },
              'accessToken': 'fresh-access-token',
              'refreshToken': 'fresh-refresh-token',
              'accessExpiresAt': '2026-08-25T00:00:00Z',
              'refreshExpiresAt': '2026-09-24T00:00:00Z',
            }),
          ),
          200,
        );
      }),
    );

    final user = await client.updateUsername(
      accessToken: 'access-token',
      username: 'new_login',
    );
    final renamed = await client.updateDisplayName(
      accessToken: 'access-token',
      displayName: '新的昵称',
    );
    final session = await client.changePassword(
      accessToken: 'access-token',
      currentPassword: 'password-123',
      newPassword: 'new-password-456',
    );

    expect(user.username, 'new_login');
    expect(renamed.displayName, '新的昵称');
    expect(session.accessToken, 'fresh-access-token');
  });

  test('rotates and revokes authentication sessions', () async {
    var requestNumber = 0;
    final client = GameApiClient(
      serverBaseUri: Uri.parse('http://game.test'),
      httpClient: MockClient((request) async {
        requestNumber++;
        if (requestNumber == 1) {
          expect(request.url.path, '/v1/auth/refresh');
          expect(request.headers.containsKey('authorization'), isFalse);
          expect(jsonDecode(request.body), {'refreshToken': 'old-refresh'});
          return http.Response.bytes(
            utf8.encode(
              jsonEncode({
                'user': {
                  'userId': 'usr_1',
                  'username': 'friend_1',
                  'displayName': '好友一',
                },
                'accessToken': 'new-access',
                'refreshToken': 'new-refresh',
                'accessExpiresAt': '2030-01-01T00:15:00Z',
                'refreshExpiresAt': '2030-02-01T00:00:00Z',
              }),
            ),
            200,
          );
        }
        expect(request.url.path, '/v1/auth/logout');
        expect(request.headers['authorization'], 'Bearer new-access');
        return http.Response('', 204);
      }),
    );

    final refreshed = await client.refresh('old-refresh');
    await client.logout(refreshed.accessToken);

    expect(refreshed.refreshToken, 'new-refresh');
    expect(requestNumber, 2);
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

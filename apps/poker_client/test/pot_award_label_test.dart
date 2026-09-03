import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';

TableSeatSnapshot _seat(String userId, String displayName) =>
    TableSeatSnapshot.fromJson({
      'userId': userId,
      'displayName': displayName,
      'seat': 1,
      'stack': 1000,
      'ready': true,
      'connected': true,
      'participating': true,
      'folded': false,
      'allIn': false,
      'streetBet': 0,
      'totalBet': 0,
      'position': '',
      'lastAction': '',
      'lastCommitted': 0,
      'lastActionTo': 0,
      'timeExtensions': 0,
    });

PotAward _award({
  int potIndex = 0,
  int runoutIndex = 0,
  int amount = 400,
  String userId = 'winner',
  String displayName = '',
}) => PotAward.fromJson({
  'potIndex': potIndex,
  'runoutIndex': runoutIndex,
  'amount': amount,
  'payouts': [
    {
      'playerId': userId,
      if (displayName.isNotEmpty) 'displayName': displayName,
      'amount': amount,
    },
  ],
});

void main() {
  group('结算文案的赢家名字', () {
    test('优先用服务端随结算下发的昵称', () {
      // 赢家常常赢完这手就离开房间，届时座位已经没了
      expect(
        potAwardLabel(_award(displayName: '好友'), const []),
        contains('好友 +400'),
      );
    });

    test('旧服务端不下发昵称时回退到座位', () {
      expect(
        potAwardLabel(_award(), [_seat('winner', '房主')]),
        contains('房主 +400'),
      );
    });

    test('两处都查不到时给出可读文案，不暴露用户 ID', () {
      final label = potAwardLabel(_award(userId: 'usr_HJK8-d4juXRT4a8o'), const []);
      expect(label, contains('已离开的玩家 +400'));
      expect(label, isNot(contains('usr_HJK8')));
    });
  });

  group('底池称呼', () {
    test('只有一个池时叫「底池」，不叫「主池」', () {
      // 「主池」这个称呼要有对照物才成立：没人 all in 就只有一个池
      expect(potAwardLabel(_award(displayName: '好友'), const []), startsWith('底池 400'));
    });

    test('有边池时才区分主池与边池', () {
      expect(
        potAwardLabel(_award(displayName: '好友'), const [], totalPots: 2),
        startsWith('主池 400'),
      );
      expect(
        potAwardLabel(
          _award(potIndex: 1, amount: 800, displayName: '好友'),
          const [],
          totalPots: 2,
        ),
        startsWith('边池 1 800'),
      );
    });

    test('发两次时带上第几次', () {
      expect(
        potAwardLabel(
          _award(runoutIndex: 2, amount: 200, displayName: '好友'),
          const [],
        ),
        startsWith('第2次 · 底池 200'),
      );
    });
  });
}

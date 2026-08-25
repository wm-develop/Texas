import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';

void main() {
  test(
    'parses positions deadlines bet suggestions and showdown settlement',
    () {
      final snapshot = TableSnapshot.fromJson({
        'roomId': 'table_1',
        'roomCode': '123456',
        'tableRevision': 12,
        'phase': 'WAITING_NEXT_HAND',
        'handId': 'hand_1',
        'dealerSeat': 1,
        'board': ['As', 'Kh', 'Qd', 'Jc', 'Ts'],
        'holeCards': ['As', 'Kh'],
        'seats': [
          {
            'userId': 'user_1',
            'displayName': '玩家一',
            'seat': 1,
            'stack': 2200,
            'ready': false,
            'connected': true,
            'participating': true,
            'folded': false,
            'allIn': false,
            'streetBet': 100,
            'totalBet': 200,
            'position': 'BTN',
            'lastAction': 'raise',
            'lastCommitted': 80,
            'lastActionTo': 100,
            'timeExtensions': 2,
          },
        ],
        'currentAction': {
          'userId': 'user_1',
          'seat': 1,
          'deadline': 1000000,
          'options': {
            'toCall': 20,
            'canFold': true,
            'canCheck': false,
            'canCall': true,
            'canBet': false,
            'canRaise': true,
            'canAllIn': true,
            'minRaiseTo': 120,
            'maxRaiseTo': 2000,
          },
          'suggestions': [
            {'label': 'quarter_pot', 'action': 'raise', 'raiseTo': 120},
            {'label': 'all_in', 'action': 'all_in', 'raiseTo': 2000},
          ],
        },
        'totalPot': 400,
        'settlement': {
          'handId': 'hand_1',
          'potAwards': [
            {
              'potIndex': 0,
              'amount': 400,
              'winnerPlayerIds': ['user_1'],
              'payouts': [
                {'playerId': 'user_1', 'amount': 400},
              ],
            },
          ],
          'showdown': true,
          'revealedHands': [
            {
              'playerId': 'user_1',
              'holeCards': ['As', 'Kh'],
              'category': 'straight',
            },
          ],
        },
      });

      expect(snapshot.seats.single.position, 'BTN');
      expect(snapshot.seats.single.lastActionTo, 100);
      expect(snapshot.seats.single.timeExtensions, 2);
      expect(snapshot.currentAction!.options.minRaiseTo, 120);
      expect(snapshot.currentAction!.suggestions.last.label, 'all_in');
      expect(snapshot.settlement!.revealedHands.single.category, 'straight');
      expect(snapshot.settlement!.potAwards.single.payouts.single.amount, 400);
    },
  );
}

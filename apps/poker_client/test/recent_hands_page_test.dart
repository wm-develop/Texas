import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';
import 'package:poker_client/features/history/presentation/recent_hands_page.dart';

void main() {
  testWidgets('shows personalized recent hand result and details', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: RecentHandsPage(
          userId: 'me',
          loadHands: () async => [
            RecentHand(
              handId: 'hand_1',
              roomCode: '123456',
              endedAt: DateTime.utc(2026, 8, 25, 1),
              board: const ['As', 'Kd', 'Qc'],
              showdown: false,
              players: const [
                RecentHandPlayer(
                  userId: 'me',
                  displayName: '我',
                  seat: 1,
                  startingStack: 2000,
                  endingStack: 2040,
                  delta: 40,
                  holeCards: ['Ah', 'Ad'],
                ),
                RecentHandPlayer(
                  userId: 'friend',
                  displayName: '好友',
                  seat: 2,
                  startingStack: 2000,
                  endingStack: 1960,
                  delta: -40,
                  holeCards: [],
                ),
              ],
            ),
          ],
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('+40 筹码'), findsOneWidget);
    expect(find.textContaining('房间 123456'), findsOneWidget);
    await tester.tap(find.text('+40 筹码'));
    await tester.pumpAndSettle();
    expect(find.text('公共牌'), findsOneWidget);
    expect(find.text('好友 · 座位 2'), findsOneWidget);
  });
}

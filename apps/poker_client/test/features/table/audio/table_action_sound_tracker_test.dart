import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/audio/table_action_sound_tracker.dart';

void main() {
  group('TableActionSoundTracker', () {
    test('keeps the initial snapshot and reconnect replay silent', () {
      final tracker = TableActionSoundTracker();
      const initial = TableActionSoundFrame(
        handId: 'hand-1',
        actionId: 'action-12',
        action: 'raise',
      );

      expect(tracker.observeFrame(initial), isNull);
      expect(tracker.observeFrame(initial), isNull);
    });

    test('maps every supported server-confirmed action', () {
      expect(_advanceTo('call'), TableSoundEffect.chips);
      expect(_advanceTo('bet'), TableSoundEffect.chips);
      expect(_advanceTo('raise'), TableSoundEffect.chips);
      expect(_advanceTo('all_in'), TableSoundEffect.allIn);
      expect(_advanceTo('check'), TableSoundEffect.check);
      expect(_advanceTo('fold'), TableSoundEffect.fold);
    });

    test('does not replay the same action on unrelated snapshot updates', () {
      final tracker = TableActionSoundTracker();
      tracker.observeFrame(_frame('action-20', 'check'));

      expect(tracker.observeFrame(_frame('action-20', 'check')), isNull);
      expect(tracker.observeFrame(_frame('action-20', 'check')), isNull);
    });

    test(
      'plays the last action even when a new street cleared seat labels',
      () {
        final tracker = TableActionSoundTracker();
        tracker.observeFrame(_frame('', ''));

        expect(
          tracker.observeFrame(_frame('river-opening-check', 'check')),
          TableSoundEffect.check,
        );
      },
    );

    test('allows reused client action ids in different hands', () {
      final tracker = TableActionSoundTracker();
      tracker.observeFrame(_frame('fold', 'fold', handId: 'hand-1'));

      expect(
        tracker.observeFrame(_frame('fold', 'fold', handId: 'hand-2')),
        TableSoundEffect.fold,
      );
    });

    test('does not reset deduplication during a transient null snapshot', () {
      final tracker = TableActionSoundTracker();
      tracker.observeFrame(_frame('action-1', 'bet'));

      expect(tracker.observe(null), isNull);
      expect(tracker.observeFrame(_frame('action-1', 'bet')), isNull);
    });
  });
}

TableSoundEffect? _advanceTo(String action) {
  final tracker = TableActionSoundTracker();
  tracker.observeFrame(_frame('', ''));
  return tracker.observeFrame(_frame('action-1', action));
}

TableActionSoundFrame _frame(
  String actionId,
  String action, {
  String handId = 'hand-1',
}) => TableActionSoundFrame(handId: handId, actionId: actionId, action: action);

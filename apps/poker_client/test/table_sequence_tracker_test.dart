import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/table_sequence_tracker.dart';

void main() {
  test('accepts contiguous events and rejects duplicates', () {
    final tracker = TableSequenceTracker();

    expect(
      tracker.accept('table.chat.message', 8),
      TableSequenceDisposition.accepted,
    );
    expect(
      tracker.accept('table.chat.message', 9),
      TableSequenceDisposition.accepted,
    );
    expect(
      tracker.accept('table.chat.message', 9),
      TableSequenceDisposition.duplicate,
    );
    expect(tracker.lastSequence, 9);
  });

  test('reports a gap without advancing and lets a snapshot heal it', () {
    final tracker = TableSequenceTracker();
    tracker.accept('table.snapshot', 12);

    expect(
      tracker.accept('table.chat.message', 14),
      TableSequenceDisposition.gap,
    );
    expect(tracker.lastSequence, 12);
    expect(
      tracker.accept('table.snapshot', 14),
      TableSequenceDisposition.accepted,
    );
    expect(tracker.recoveredThrough(14), isTrue);
  });
}

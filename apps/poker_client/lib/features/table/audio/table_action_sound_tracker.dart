import 'package:poker_client/features/table/domain/table_snapshot.dart';

enum TableSoundEffect { chips, allIn, check, fold, praise, taunt }

class TableActionSoundFrame {
  const TableActionSoundFrame({
    required this.handId,
    required this.actionId,
    required this.action,
  });

  factory TableActionSoundFrame.fromSnapshot(TableSnapshot snapshot) =>
      TableActionSoundFrame(
        handId: snapshot.lastAction?.handId ?? '',
        actionId: snapshot.lastAction?.actionId ?? '',
        action: snapshot.lastAction?.action ?? '',
      );

  final String handId;
  final String actionId;
  final String action;

  String get identity => actionId.isEmpty ? '' : '$handId\u0000$actionId';
}

/// Converts server-confirmed actions into one-shot sound cues.
///
/// The first snapshot is deliberately silent. Later snapshots use the stable
/// hand/action identity supplied by the server, so reconnects and duplicate
/// snapshots never replay an old action and the final action of a street is not
/// lost when the server resets per-seat labels for the next street.
class TableActionSoundTracker {
  bool _initialized = false;
  String _lastIdentity = '';

  TableSoundEffect? observe(TableSnapshot? snapshot) {
    if (snapshot == null) return null;
    return observeFrame(TableActionSoundFrame.fromSnapshot(snapshot));
  }

  TableSoundEffect? observeFrame(TableActionSoundFrame current) {
    final identity = current.identity;
    if (!_initialized) {
      _initialized = true;
      _lastIdentity = identity;
      return null;
    }
    if (identity.isEmpty) {
      _lastIdentity = '';
      return null;
    }
    if (identity == _lastIdentity) return null;
    _lastIdentity = identity;

    return switch (current.action) {
      'fold' => TableSoundEffect.fold,
      'check' => TableSoundEffect.check,
      'call' || 'bet' || 'raise' => TableSoundEffect.chips,
      'all_in' => TableSoundEffect.allIn,
      _ => null,
    };
  }
}

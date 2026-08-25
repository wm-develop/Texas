enum TableSequenceDisposition { accepted, duplicate, gap }

class TableSequenceTracker {
  int _lastSequence = 0;

  int get lastSequence => _lastSequence;

  TableSequenceDisposition accept(String? messageType, int sequence) {
    if (sequence <= 0) return TableSequenceDisposition.accepted;
    if (messageType == 'table.snapshot') {
      if (sequence < _lastSequence) return TableSequenceDisposition.duplicate;
      _lastSequence = sequence;
      return TableSequenceDisposition.accepted;
    }
    if (sequence <= _lastSequence) return TableSequenceDisposition.duplicate;
    if (_lastSequence > 0 && sequence != _lastSequence + 1) {
      return TableSequenceDisposition.gap;
    }
    _lastSequence = sequence;
    return TableSequenceDisposition.accepted;
  }

  bool recoveredThrough(int sequence) => _lastSequence >= sequence;
}

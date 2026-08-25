class TableActionOptions {
  const TableActionOptions({
    required this.toCall,
    required this.canFold,
    required this.canCheck,
    required this.canCall,
    required this.canBet,
    required this.canRaise,
    required this.canAllIn,
    required this.minRaiseTo,
    required this.maxRaiseTo,
  });

  final int toCall;
  final bool canFold;
  final bool canCheck;
  final bool canCall;
  final bool canBet;
  final bool canRaise;
  final bool canAllIn;
  final int minRaiseTo;
  final int maxRaiseTo;

  factory TableActionOptions.fromJson(Map<String, dynamic> json) =>
      TableActionOptions(
        toCall: json['toCall'] as int? ?? 0,
        canFold: json['canFold'] as bool? ?? false,
        canCheck: json['canCheck'] as bool? ?? false,
        canCall: json['canCall'] as bool? ?? false,
        canBet: json['canBet'] as bool? ?? false,
        canRaise: json['canRaise'] as bool? ?? false,
        canAllIn: json['canAllIn'] as bool? ?? false,
        minRaiseTo: json['minRaiseTo'] as int? ?? 0,
        maxRaiseTo: json['maxRaiseTo'] as int? ?? 0,
      );
}

class BetSuggestion {
  const BetSuggestion({
    required this.label,
    required this.action,
    required this.raiseTo,
  });

  final String label;
  final String action;
  final int raiseTo;

  factory BetSuggestion.fromJson(Map<String, dynamic> json) => BetSuggestion(
    label: json['label'] as String,
    action: json['action'] as String,
    raiseTo: json['raiseTo'] as int? ?? 0,
  );
}

class CurrentTableAction {
  const CurrentTableAction({
    required this.userId,
    required this.seat,
    required this.deadline,
    required this.options,
    required this.suggestions,
  });

  final String userId;
  final int seat;
  final DateTime? deadline;
  final TableActionOptions options;
  final List<BetSuggestion> suggestions;

  factory CurrentTableAction.fromJson(Map<String, dynamic> json) =>
      CurrentTableAction(
        userId: json['userId'] as String,
        seat: json['seat'] as int,
        deadline: (json['deadline'] as int? ?? 0) == 0
            ? null
            : DateTime.fromMillisecondsSinceEpoch(json['deadline'] as int),
        options: TableActionOptions.fromJson(
          json['options'] as Map<String, dynamic>,
        ),
        suggestions: (json['suggestions'] as List<dynamic>? ?? const [])
            .map(
              (value) => BetSuggestion.fromJson(value as Map<String, dynamic>),
            )
            .toList(growable: false),
      );
}

class TableSeatSnapshot {
  const TableSeatSnapshot({
    required this.userId,
    required this.displayName,
    required this.seat,
    required this.stack,
    required this.ready,
    required this.connected,
    required this.participating,
    required this.folded,
    required this.allIn,
    required this.streetBet,
    required this.totalBet,
    required this.position,
    required this.lastAction,
    required this.lastCommitted,
    required this.lastActionTo,
    required this.timeExtensions,
  });

  final String userId;
  final String displayName;
  final int seat;
  final int stack;
  final bool ready;
  final bool connected;
  final bool participating;
  final bool folded;
  final bool allIn;
  final int streetBet;
  final int totalBet;
  final String position;
  final String lastAction;
  final int lastCommitted;
  final int lastActionTo;
  final int timeExtensions;

  factory TableSeatSnapshot.fromJson(Map<String, dynamic> json) =>
      TableSeatSnapshot(
        userId: json['userId'] as String,
        displayName: json['displayName'] as String,
        seat: json['seat'] as int,
        stack: json['stack'] as int,
        ready: json['ready'] as bool,
        connected: json['connected'] as bool,
        participating: json['participating'] as bool,
        folded: json['folded'] as bool,
        allIn: json['allIn'] as bool,
        streetBet: json['streetBet'] as int,
        totalBet: json['totalBet'] as int,
        position: json['position'] as String? ?? '',
        lastAction: json['lastAction'] as String? ?? '',
        lastCommitted: json['lastCommitted'] as int? ?? 0,
        lastActionTo: json['lastActionTo'] as int? ?? 0,
        timeExtensions: json['timeExtensions'] as int? ?? 0,
      );
}

class RevealedHand {
  const RevealedHand({
    required this.userId,
    required this.holeCards,
    required this.category,
  });

  final String userId;
  final List<String> holeCards;
  final String category;

  factory RevealedHand.fromJson(Map<String, dynamic> json) => RevealedHand(
    userId: json['playerId'] as String,
    holeCards: (json['holeCards'] as List<dynamic>).cast<String>(),
    category: json['category'] as String,
  );
}

class PotPayout {
  const PotPayout({required this.userId, required this.amount});

  final String userId;
  final int amount;

  factory PotPayout.fromJson(Map<String, dynamic> json) => PotPayout(
    userId: json['playerId'] as String,
    amount: json['amount'] as int,
  );
}

class PotAward {
  const PotAward({
    required this.potIndex,
    required this.amount,
    required this.payouts,
  });

  final int potIndex;
  final int amount;
  final List<PotPayout> payouts;

  factory PotAward.fromJson(Map<String, dynamic> json) => PotAward(
    potIndex: json['potIndex'] as int,
    amount: json['amount'] as int,
    payouts: (json['payouts'] as List<dynamic>)
        .map((value) => PotPayout.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
  );
}

class TableSettlement {
  const TableSettlement({
    required this.handId,
    required this.showdown,
    required this.revealedHands,
    required this.potAwards,
  });

  final String handId;
  final bool showdown;
  final List<RevealedHand> revealedHands;
  final List<PotAward> potAwards;

  factory TableSettlement.fromJson(Map<String, dynamic> json) =>
      TableSettlement(
        handId: json['handId'] as String,
        showdown: json['showdown'] as bool? ?? false,
        revealedHands: (json['revealedHands'] as List<dynamic>? ?? const [])
            .map(
              (value) => RevealedHand.fromJson(value as Map<String, dynamic>),
            )
            .toList(growable: false),
        potAwards: (json['potAwards'] as List<dynamic>? ?? const [])
            .map((value) => PotAward.fromJson(value as Map<String, dynamic>))
            .toList(growable: false),
      );
}

class TableSnapshot {
  const TableSnapshot({
    required this.roomId,
    required this.roomCode,
    required this.tableRevision,
    required this.phase,
    required this.handId,
    required this.dealerSeat,
    required this.board,
    required this.holeCards,
    required this.seats,
    required this.currentAction,
    required this.totalPot,
    required this.settlement,
  });

  final String roomId;
  final String roomCode;
  final int tableRevision;
  final String phase;
  final String handId;
  final int dealerSeat;
  final List<String> board;
  final List<String> holeCards;
  final List<TableSeatSnapshot> seats;
  final CurrentTableAction? currentAction;
  final int totalPot;
  final TableSettlement? settlement;
  bool get hasSettlement => settlement != null;

  factory TableSnapshot.fromJson(Map<String, dynamic> json) => TableSnapshot(
    roomId: json['roomId'] as String,
    roomCode: json['roomCode'] as String,
    tableRevision: json['tableRevision'] as int,
    phase: json['phase'] as String,
    handId: json['handId'] as String? ?? '',
    dealerSeat: json['dealerSeat'] as int? ?? 0,
    board: (json['board'] as List<dynamic>? ?? const []).cast<String>(),
    holeCards: (json['holeCards'] as List<dynamic>? ?? const []).cast<String>(),
    seats: (json['seats'] as List<dynamic>? ?? const [])
        .map(
          (value) => TableSeatSnapshot.fromJson(value as Map<String, dynamic>),
        )
        .toList(growable: false),
    currentAction: json['currentAction'] == null
        ? null
        : CurrentTableAction.fromJson(
            json['currentAction'] as Map<String, dynamic>,
          ),
    totalPot: json['totalPot'] as int? ?? 0,
    settlement: json['settlement'] == null
        ? null
        : TableSettlement.fromJson(json['settlement'] as Map<String, dynamic>),
  );
}

class TableChatMessage {
  const TableChatMessage({
    required this.messageId,
    required this.userId,
    required this.displayName,
    required this.kind,
    required this.content,
    required this.sentAt,
  });

  final String messageId;
  final String userId;
  final String displayName;
  final String kind;
  final String content;
  final DateTime sentAt;

  factory TableChatMessage.fromJson(Map<String, dynamic> json) =>
      TableChatMessage(
        messageId: json['messageId'] as String,
        userId: json['userId'] as String,
        displayName: json['displayName'] as String,
        kind: json['kind'] as String,
        content: json['content'] as String,
        sentAt: DateTime.fromMillisecondsSinceEpoch(json['sentAt'] as int),
      );
}

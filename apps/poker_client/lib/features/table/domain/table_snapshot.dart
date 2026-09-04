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

class ConfirmedTableAction {
  const ConfirmedTableAction({
    required this.actionId,
    required this.handId,
    required this.userId,
    required this.action,
    required this.tableRevision,
  });

  final String actionId;
  final String handId;
  final String userId;
  final String action;
  final int tableRevision;

  factory ConfirmedTableAction.fromJson(Map<String, dynamic> json) =>
      ConfirmedTableAction(
        actionId: json['actionId'] as String,
        handId: json['handId'] as String,
        userId: json['userId'] as String,
        action: json['action'] as String,
        tableRevision: json['tableRevision'] as int,
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
    this.holeCards = const [],
    this.pendingSpectate = false,
  });

  /// 只有本手有效付费的观战者才会收到别人的手牌；普通玩家这里永远为空。
  final List<String> holeCards;

  /// 已申请本手结束后进入观战。
  final bool pendingSpectate;

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
        holeCards: (json['holeCards'] as List<dynamic>? ?? const [])
            .cast<String>(),
        pendingSpectate: json['pendingSpectate'] as bool? ?? false,
      );
}

/// 观战位上的一名成员。
class SpectatorSnapshot {
  const SpectatorSnapshot({
    required this.userId,
    required this.displayName,
    required this.connected,
    required this.stack,
    required this.canSeeHoleCards,
    required this.pendingSeat,
  });

  final String userId;
  final String displayName;
  final bool connected;
  final int stack;

  /// 本手已付费（或免费模式），能看到所有人的手牌。
  final bool canSeeHoleCards;

  /// 已申请本手结束后上桌。
  final bool pendingSeat;

  factory SpectatorSnapshot.fromJson(Map<String, dynamic> json) =>
      SpectatorSnapshot(
        userId: json['userId'] as String,
        displayName: json['displayName'] as String? ?? '',
        connected: json['connected'] as bool? ?? false,
        stack: json['stack'] as int? ?? 0,
        canSeeHoleCards: json['canSeeHoleCards'] as bool? ?? false,
        pendingSeat: json['pendingSeat'] as bool? ?? false,
      );
}

/// 房主对观战位的设置。
class SpectatorSettings {
  const SpectatorSettings({
    this.feeBigBlinds = 10,
    this.voiceAllowed = true,
    this.chatAllowed = true,
    this.emoteAllowed = true,
  });

  /// 看牌费，按大盲倍数；0 表示免费。
  final int feeBigBlinds;
  final bool voiceAllowed;
  final bool chatAllowed;
  final bool emoteAllowed;

  static const int maxFeeBigBlinds = 100;

  factory SpectatorSettings.fromJson(Map<String, dynamic>? json) =>
      SpectatorSettings(
        feeBigBlinds: json?['feeBigBlinds'] as int? ?? 10,
        voiceAllowed: json?['voiceAllowed'] as bool? ?? true,
        chatAllowed: json?['chatAllowed'] as bool? ?? true,
        emoteAllowed: json?['emoteAllowed'] as bool? ?? true,
      );

  Map<String, Object?> toJson() => {
    'feeBigBlinds': feeBigBlinds,
    'voiceAllowed': voiceAllowed,
    'chatAllowed': chatAllowed,
    'emoteAllowed': emoteAllowed,
  };

  SpectatorSettings copyWith({
    int? feeBigBlinds,
    bool? voiceAllowed,
    bool? chatAllowed,
    bool? emoteAllowed,
  }) => SpectatorSettings(
    feeBigBlinds: feeBigBlinds ?? this.feeBigBlinds,
    voiceAllowed: voiceAllowed ?? this.voiceAllowed,
    chatAllowed: chatAllowed ?? this.chatAllowed,
    emoteAllowed: emoteAllowed ?? this.emoteAllowed,
  );
}

/// 一笔看牌费的收或付。
class SpectatorFeeShare {
  const SpectatorFeeShare({
    required this.userId,
    required this.displayName,
    required this.amount,
  });

  final String userId;
  final String displayName;
  final int amount;

  factory SpectatorFeeShare.fromJson(Map<String, dynamic> json) =>
      SpectatorFeeShare(
        userId: json['userId'] as String,
        displayName: json['displayName'] as String? ?? '',
        amount: json['amount'] as int? ?? 0,
      );
}

/// 一手牌的看牌费明细。
class SpectatorFees {
  const SpectatorFees({
    required this.handId,
    required this.feePerSpectator,
    required this.payers,
    required this.recipients,
  });

  final String handId;
  final int feePerSpectator;
  final List<SpectatorFeeShare> payers;
  final List<SpectatorFeeShare> recipients;

  factory SpectatorFees.fromJson(Map<String, dynamic> json) => SpectatorFees(
    handId: json['handId'] as String? ?? '',
    feePerSpectator: json['feePerSpectator'] as int? ?? 0,
    payers: (json['payers'] as List<dynamic>? ?? const [])
        .map((value) => SpectatorFeeShare.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    recipients: (json['recipients'] as List<dynamic>? ?? const [])
        .map((value) => SpectatorFeeShare.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
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
  const PotPayout({
    required this.userId,
    required this.displayName,
    required this.amount,
  });

  final String userId;

  /// 服务端在结算时固化的昵称。赢家常常赢完这手就离开房间，那时座位已经
  /// 没了，靠座位反查只能显示用户 ID。旧服务端不下发时为空。
  final String displayName;
  final int amount;

  factory PotPayout.fromJson(Map<String, dynamic> json) => PotPayout(
    userId: json['playerId'] as String,
    displayName: json['displayName'] as String? ?? '',
    amount: json['amount'] as int,
  );
}

class PotAward {
  const PotAward({
    required this.potIndex,
    required this.runoutIndex,
    required this.amount,
    required this.payouts,
  });

  final int potIndex;
  final int runoutIndex;
  final int amount;
  final List<PotPayout> payouts;

  factory PotAward.fromJson(Map<String, dynamic> json) => PotAward(
    potIndex: json['potIndex'] as int,
    runoutIndex: json['runoutIndex'] as int? ?? 0,
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
    required this.runoutBoards,
  });

  final String handId;
  final bool showdown;
  final List<RevealedHand> revealedHands;
  final List<PotAward> potAwards;
  final List<List<String>> runoutBoards;

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
        runoutBoards: (json['runoutBoards'] as List<dynamic>? ?? const [])
            .map((value) => (value as List<dynamic>).cast<String>())
            .toList(growable: false),
      );
}

class RunoutChoiceSnapshot {
  const RunoutChoiceSnapshot({
    required this.eligiblePlayerIds,
    required this.choices,
    required this.deadline,
  });

  final List<String> eligiblePlayerIds;
  final Map<String, int> choices;
  final DateTime? deadline;

  factory RunoutChoiceSnapshot.fromJson(Map<String, dynamic> json) =>
      RunoutChoiceSnapshot(
        eligiblePlayerIds:
            (json['eligiblePlayerIds'] as List<dynamic>? ?? const [])
                .cast<String>(),
        choices: (json['choices'] as Map<String, dynamic>? ?? const {}).map(
          (key, value) => MapEntry(key, value as int),
        ),
        deadline: (json['deadline'] as int? ?? 0) == 0
            ? null
            : DateTime.fromMillisecondsSinceEpoch(json['deadline'] as int),
      );
}

class PendingTableRequest {
  const PendingTableRequest({
    required this.requestId,
    required this.requesterUserId,
    required this.targetUserId,
  });

  final String requestId;
  final String requesterUserId;
  final String targetUserId;

  factory PendingTableRequest.fromJson(Map<String, dynamic> json) =>
      PendingTableRequest(
        requestId: json['requestId'] as String,
        requesterUserId: json['requesterUserId'] as String,
        targetUserId: json['targetUserId'] as String,
      );
}

class TableSnapshot {
  const TableSnapshot({
    required this.roomId,
    required this.roomCode,
    required this.ownerUserId,
    required this.tableRevision,
    required this.phase,
    required this.handId,
    required this.dealerSeat,
    this.smallBlindSeat = 0,
    required this.board,
    required this.holeCards,
    required this.seats,
    required this.currentAction,
    required this.lastAction,
    required this.totalPot,
    required this.maxBuyIn,
    required this.settlement,
    required this.voluntaryReveals,
    required this.privateReveals,
    required this.holeCardViewRequests,
    required this.seatSwapRequests,
    required this.runoutChoice,
    required this.canShowHoleCards,
    required this.autoReadyDeadline,
    required this.autoReadyCancelled,
    this.draining = false,
    this.joinLocked = false,
    this.spectators = const [],
    this.spectatorSettings = const SpectatorSettings(),
    this.spectatorFee = 0,
    this.spectatorFees,
    this.spectating = false,
  });

  final String roomId;
  final String roomCode;
  final String ownerUserId;
  final int tableRevision;
  final String phase;
  final String handId;
  final int dealerSeat;

  /// 小盲座位号。发牌顺序从这里开始。
  final int smallBlindSeat;
  final List<String> board;
  final List<String> holeCards;
  final List<TableSeatSnapshot> seats;
  final CurrentTableAction? currentAction;
  final ConfirmedTableAction? lastAction;
  final int totalPot;
  final int maxBuyIn;
  final TableSettlement? settlement;
  final List<RevealedHand> voluntaryReveals;
  final List<RevealedHand> privateReveals;
  final List<PendingTableRequest> holeCardViewRequests;
  final List<PendingTableRequest> seatSwapRequests;
  final RunoutChoiceSnapshot? runoutChoice;
  final bool canShowHoleCards;
  final DateTime? autoReadyDeadline;
  final bool autoReadyCancelled;

  /// 服务端正在优雅停机：本手结束后不再开新局，重启完成后自动恢复。
  final bool draining;

  /// 房主已关闭房间入口。
  final bool joinLocked;

  /// 观战位上的成员；[seats] 仍只含上桌玩家。
  final List<SpectatorSnapshot> spectators;

  /// 房主对观战位的设置。
  final SpectatorSettings spectatorSettings;

  /// 每手看牌费的筹码数（= feeBigBlinds × 大盲）。0 表示免费。
  final int spectatorFee;

  /// 某位成员的桌上筹码：上桌玩家取座位，观战者取观战位；不在房间返回 null。
  /// 补码等只关心「他有多少筹码」的逻辑用这个，别只认座位——观战者没有座位。
  int? stackForUser(String userId) {
    for (final seat in seats) {
      if (seat.userId == userId) return seat.stack;
    }
    for (final spectator in spectators) {
      if (spectator.userId == userId) return spectator.stack;
    }
    return null;
  }

  /// 本手看牌费明细，只在该手（含结算展示期）下发。
  final SpectatorFees? spectatorFees;

  /// 本人在观战位。
  final bool spectating;

  /// 上桌人数（不含观战者）。
  int get seatedCount => seats.length;
  bool get hasSettlement => settlement != null;

  factory TableSnapshot.fromJson(Map<String, dynamic> json) => TableSnapshot(
    roomId: json['roomId'] as String,
    roomCode: json['roomCode'] as String,
    ownerUserId: json['ownerUserId'] as String? ?? '',
    tableRevision: json['tableRevision'] as int,
    phase: json['phase'] as String,
    handId: json['handId'] as String? ?? '',
    dealerSeat: json['dealerSeat'] as int? ?? 0,
    smallBlindSeat: json['smallBlindSeat'] as int? ?? 0,
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
    lastAction: json['lastAction'] == null
        ? null
        : ConfirmedTableAction.fromJson(
            json['lastAction'] as Map<String, dynamic>,
          ),
    totalPot: json['totalPot'] as int? ?? 0,
    maxBuyIn: json['maxBuyIn'] as int? ?? 0,
    settlement: json['settlement'] == null
        ? null
        : TableSettlement.fromJson(json['settlement'] as Map<String, dynamic>),
    voluntaryReveals: (json['voluntaryReveals'] as List<dynamic>? ?? const [])
        .map((value) => RevealedHand.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    privateReveals: (json['privateReveals'] as List<dynamic>? ?? const [])
        .map((value) => RevealedHand.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    holeCardViewRequests:
        (json['holeCardViewRequests'] as List<dynamic>? ?? const [])
            .map(
              (value) =>
                  PendingTableRequest.fromJson(value as Map<String, dynamic>),
            )
            .toList(growable: false),
    seatSwapRequests: (json['seatSwapRequests'] as List<dynamic>? ?? const [])
        .map(
          (value) =>
              PendingTableRequest.fromJson(value as Map<String, dynamic>),
        )
        .toList(growable: false),
    runoutChoice: json['runoutChoice'] == null
        ? null
        : RunoutChoiceSnapshot.fromJson(
            json['runoutChoice'] as Map<String, dynamic>,
          ),
    canShowHoleCards: json['canShowHoleCards'] as bool? ?? false,
    autoReadyDeadline: (json['autoReadyDeadline'] as int? ?? 0) == 0
        ? null
        : DateTime.fromMillisecondsSinceEpoch(json['autoReadyDeadline'] as int),
    autoReadyCancelled: json['autoReadyCancelled'] as bool? ?? false,
    draining: json['draining'] as bool? ?? false,
    joinLocked: json['joinLocked'] as bool? ?? false,
    spectators: (json['spectators'] as List<dynamic>? ?? const [])
        .map(
          (value) => SpectatorSnapshot.fromJson(value as Map<String, dynamic>),
        )
        .toList(growable: false),
    spectatorSettings: SpectatorSettings.fromJson(
      json['spectatorSettings'] as Map<String, dynamic>?,
    ),
    spectatorFees: json['spectatorFees'] == null
        ? null
        : SpectatorFees.fromJson(json['spectatorFees'] as Map<String, dynamic>),
    spectating: json['spectating'] as bool? ?? false,
    spectatorFee: json['spectatorFee'] as int? ?? 0,
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

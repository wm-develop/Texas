import 'package:poker_client/features/table/domain/table_snapshot.dart';

/// 一手牌的回放时间轴，由服务端按请求者裁剪后生成：本人的底牌每一帧都有，
/// 别人的底牌只在摊牌亮出之后才会出现。客户端只负责逐帧显示，不做任何推算。
class HandReplay {
  const HandReplay({
    required this.handId,
    required this.roomCode,
    required this.endedAt,
    required this.smallBlind,
    required this.bigBlind,
    required this.dealerSeat,
    required this.smallBlindSeat,
    required this.bigBlindSeat,
    required this.showdown,
    required this.rake,
    required this.players,
    required this.steps,
    required this.revealedHands,
  });

  final String handId;
  final String roomCode;
  final DateTime endedAt;
  final int smallBlind;
  final int bigBlind;
  final int dealerSeat;
  final int smallBlindSeat;
  final int bigBlindSeat;
  final bool showdown;
  final int rake;
  final List<ReplayPlayer> players;
  final List<ReplayStep> steps;
  final List<RevealedHand> revealedHands;

  factory HandReplay.fromJson(Map<String, dynamic> json) => HandReplay(
    handId: json['handId'] as String,
    roomCode: json['roomCode'] as String? ?? '',
    endedAt: DateTime.parse(json['endedAt'] as String),
    smallBlind: json['smallBlind'] as int? ?? 0,
    bigBlind: json['bigBlind'] as int? ?? 0,
    dealerSeat: json['dealerSeat'] as int? ?? 0,
    smallBlindSeat: json['smallBlindSeat'] as int? ?? 0,
    bigBlindSeat: json['bigBlindSeat'] as int? ?? 0,
    showdown: json['showdown'] as bool? ?? false,
    rake: json['rake'] as int? ?? 0,
    players: (json['players'] as List<dynamic>? ?? const [])
        .map((value) => ReplayPlayer.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    steps: (json['steps'] as List<dynamic>? ?? const [])
        .map((value) => ReplayStep.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    revealedHands: (json['revealedHands'] as List<dynamic>? ?? const [])
        .map((value) => RevealedHand.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
  );

  ReplayPlayer? player(String userId) =>
      players.where((value) => value.userId == userId).firstOrNull;
}

class ReplayPlayer {
  const ReplayPlayer({
    required this.userId,
    required this.displayName,
    required this.seat,
    required this.position,
    required this.startingStack,
    required this.endingStack,
    required this.delta,
    required this.isViewer,
  });

  final String userId;
  final String displayName;
  final int seat;

  /// 位置名：BTN、SB、BB、UTG、HJ、CO 等。
  final String position;
  final int startingStack;
  final int endingStack;
  final int delta;
  final bool isViewer;

  factory ReplayPlayer.fromJson(Map<String, dynamic> json) => ReplayPlayer(
    userId: json['userId'] as String,
    displayName: json['displayName'] as String? ?? '',
    seat: json['seat'] as int,
    position: json['position'] as String? ?? '',
    startingStack: json['startingStack'] as int? ?? 0,
    endingStack: json['endingStack'] as int? ?? 0,
    delta: json['delta'] as int? ?? 0,
    isViewer: json['isViewer'] as bool? ?? false,
  );
}

/// 时间轴上的一帧，带着这一刻完整的桌面状态。
class ReplayStep {
  const ReplayStep({
    required this.index,
    required this.kind,
    required this.street,
    required this.actorId,
    required this.action,
    required this.amount,
    required this.timedOut,
    required this.board,
    required this.runoutBoards,
    required this.pot,
    required this.seats,
    required this.awards,
  });

  final int index;

  /// blinds、action、street、runout、refund、showdown、settle 之一。
  final String kind;

  /// preflop、flop、turn、river 或 showdown。
  final String street;
  final String actorId;
  final String action;

  /// 这一步投入（行动）或退回（refund）的筹码。
  final int amount;
  final bool timedOut;
  final List<String> board;
  final List<List<String>> runoutBoards;
  final int pot;
  final List<ReplaySeat> seats;

  /// 只在结算那一帧出现。
  final List<PotAward> awards;

  factory ReplayStep.fromJson(Map<String, dynamic> json) => ReplayStep(
    index: json['index'] as int? ?? 0,
    kind: json['kind'] as String? ?? '',
    street: json['street'] as String? ?? '',
    actorId: json['actorId'] as String? ?? '',
    action: json['action'] as String? ?? '',
    amount: json['amount'] as int? ?? 0,
    timedOut: json['timedOut'] as bool? ?? false,
    board: (json['board'] as List<dynamic>? ?? const []).cast<String>(),
    runoutBoards: (json['runoutBoards'] as List<dynamic>? ?? const [])
        .map((value) => (value as List<dynamic>).cast<String>())
        .toList(growable: false),
    pot: json['pot'] as int? ?? 0,
    seats: (json['seats'] as List<dynamic>? ?? const [])
        .map((value) => ReplaySeat.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    awards: (json['awards'] as List<dynamic>? ?? const [])
        .map((value) => PotAward.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
  );

  ReplaySeat? seat(String userId) =>
      seats.where((value) => value.userId == userId).firstOrNull;
}

class ReplaySeat {
  const ReplaySeat({
    required this.userId,
    required this.stack,
    required this.streetBet,
    required this.totalBet,
    required this.folded,
    required this.allIn,
    required this.lastAction,
    required this.holeCards,
  });

  final String userId;
  final int stack;
  final int streetBet;
  final int totalBet;
  final bool folded;
  final bool allIn;
  final String lastAction;

  /// 这一帧对本人可见的底牌；看不到时为空。
  final List<String> holeCards;

  factory ReplaySeat.fromJson(Map<String, dynamic> json) => ReplaySeat(
    userId: json['userId'] as String,
    stack: json['stack'] as int? ?? 0,
    streetBet: json['streetBet'] as int? ?? 0,
    totalBet: json['totalBet'] as int? ?? 0,
    folded: json['folded'] as bool? ?? false,
    allIn: json['allIn'] as bool? ?? false,
    lastAction: json['lastAction'] as String? ?? '',
    holeCards: (json['holeCards'] as List<dynamic>? ?? const []).cast<String>(),
  );
}

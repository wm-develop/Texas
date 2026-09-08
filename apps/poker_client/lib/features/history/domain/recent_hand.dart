class RecentHand {
  const RecentHand({
    required this.handId,
    required this.roomCode,
    required this.endedAt,
    required this.board,
    required this.players,
    required this.showdown,
    this.runoutBoards = const [],
    this.actions = const [],
  });

  factory RecentHand.fromJson(Map<String, dynamic> json) => RecentHand(
    handId: json['handId'] as String,
    roomCode: json['roomCode'] as String? ?? '',
    endedAt: DateTime.parse(json['endedAt'] as String),
    board: (json['board'] as List<dynamic>? ?? const []).cast<String>(),
    players: (json['players'] as List<dynamic>? ?? const [])
        .map(
          (value) => RecentHandPlayer.fromJson(value as Map<String, dynamic>),
        )
        .toList(growable: false),
    showdown: json['showdown'] as bool? ?? false,
    runoutBoards: (json['runoutBoards'] as List<dynamic>? ?? const [])
        .map((value) => (value as List<dynamic>).cast<String>())
        .toList(growable: false),
    actions: (json['actions'] as List<dynamic>? ?? const [])
        .map((value) => RecentHandAction.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
  );

  final String handId;
  final String roomCode;
  final DateTime endedAt;
  final List<String> board;
  final List<RecentHandPlayer> players;
  final bool showdown;
  final List<List<String>> runoutBoards;

  /// 本手的动作序列，按发生顺序。复盘时最想知道的就是「谁在什么时候下了多少」。
  final List<RecentHandAction> actions;
}

/// 一手牌里的一个动作。服务端一直在记录，此前客户端没有解析。
class RecentHandAction {
  const RecentHandAction({
    required this.userId,
    required this.street,
    required this.type,
    required this.committed,
    required this.raiseTo,
  });

  factory RecentHandAction.fromJson(Map<String, dynamic> json) =>
      RecentHandAction(
        userId: json['userId'] as String? ?? '',
        street: json['street'] as String? ?? '',
        type: json['type'] as String? ?? '',
        committed: json['committed'] as int? ?? 0,
        raiseTo: json['raiseTo'] as int? ?? 0,
      );

  final String userId;
  final String street;
  final String type;
  final int committed;
  final int raiseTo;
}

class RecentHandPlayer {
  const RecentHandPlayer({
    required this.userId,
    required this.displayName,
    required this.seat,
    required this.startingStack,
    required this.endingStack,
    required this.delta,
    required this.holeCards,
  });

  factory RecentHandPlayer.fromJson(Map<String, dynamic> json) =>
      RecentHandPlayer(
        userId: json['userId'] as String,
        displayName: json['displayName'] as String,
        seat: json['seat'] as int,
        startingStack: json['startingStack'] as int,
        endingStack: json['endingStack'] as int,
        delta: json['delta'] as int,
        holeCards: (json['holeCards'] as List<dynamic>? ?? const [])
            .cast<String>(),
      );

  final String userId;
  final String displayName;
  final int seat;
  final int startingStack;
  final int endingStack;
  final int delta;
  final List<String> holeCards;
}

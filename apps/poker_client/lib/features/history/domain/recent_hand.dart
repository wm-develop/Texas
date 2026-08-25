class RecentHand {
  const RecentHand({
    required this.handId,
    required this.roomCode,
    required this.endedAt,
    required this.board,
    required this.players,
    required this.showdown,
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
  );

  final String handId;
  final String roomCode;
  final DateTime endedAt;
  final List<String> board;
  final List<RecentHandPlayer> players;
  final bool showdown;
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

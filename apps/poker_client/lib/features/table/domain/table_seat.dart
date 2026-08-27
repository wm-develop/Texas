class TableSeat {
  const TableSeat({
    required this.number,
    required this.displayName,
    required this.chips,
    this.isCurrentUser = false,
    this.isDealer = false,
    this.isOwner = false,
    this.isSpeaking = false,
    this.isEmpty = false,
    this.isReady = false,
    this.isConnected = true,
    this.isFolded = false,
    this.isAllIn = false,
    this.position = '',
    this.streetBet = 0,
    this.totalBet = 0,
    this.lastAction = '',
    this.lastActionTo = 0,
    this.isCurrentActor = false,
    this.revealedCards = const [],
    this.handCategory = '',
    this.timeExtensions = 0,
    this.isParticipating = false,
  });

  final int number;
  final String displayName;
  final int chips;
  final bool isCurrentUser;
  final bool isDealer;
  final bool isOwner;
  final bool isSpeaking;
  final bool isEmpty;
  final bool isReady;
  final bool isConnected;
  final bool isFolded;
  final bool isAllIn;
  final String position;
  final int streetBet;
  final int totalBet;
  final String lastAction;
  final int lastActionTo;
  final bool isCurrentActor;
  final List<String> revealedCards;
  final String handCategory;
  final int timeExtensions;
  final bool isParticipating;
}

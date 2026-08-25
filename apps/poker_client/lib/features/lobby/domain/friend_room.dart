class RoomRules {
  const RoomRules({
    required this.startingChips,
    required this.smallBlind,
    required this.bigBlind,
    required this.actionSeconds,
    required this.maxBuyIn,
  });

  final int startingChips;
  final int smallBlind;
  final int bigBlind;
  final int actionSeconds;
  final int maxBuyIn;

  factory RoomRules.fromJson(Map<String, dynamic> json) => RoomRules(
    startingChips: json['startingChips'] as int,
    smallBlind: json['smallBlind'] as int,
    bigBlind: json['bigBlind'] as int,
    actionSeconds: json['actionSeconds'] as int,
    maxBuyIn: json['maxBuyIn'] as int? ?? json['startingChips'] as int,
  );
}

class RoomMember {
  const RoomMember({
    required this.userId,
    required this.displayName,
    required this.seat,
    required this.ready,
    required this.stack,
  });

  final String userId;
  final String displayName;
  final int seat;
  final bool ready;
  final int stack;

  factory RoomMember.fromJson(Map<String, dynamic> json) => RoomMember(
    userId: json['userId'] as String,
    displayName: json['displayName'] as String,
    seat: json['seat'] as int,
    ready: json['ready'] as bool,
    stack: json['stack'] as int? ?? 0,
  );
}

class RoomPreview {
  const RoomPreview({
    required this.code,
    required this.rules,
    required this.maxPlayers,
    required this.currentPlayers,
    required this.passwordRequired,
  });

  final String code;
  final RoomRules rules;
  final int maxPlayers;
  final int currentPlayers;
  final bool passwordRequired;

  factory RoomPreview.fromJson(Map<String, dynamic> json) => RoomPreview(
    code: json['code'] as String,
    rules: RoomRules.fromJson(json['rules'] as Map<String, dynamic>),
    maxPlayers: json['maxPlayers'] as int,
    currentPlayers: json['currentPlayers'] as int,
    passwordRequired: json['passwordRequired'] as bool,
  );
}

class CreateRoomInput {
  const CreateRoomInput({
    required this.preset,
    required this.maxPlayers,
    required this.password,
    required this.smallBlind,
    required this.bigBlind,
    required this.maxBuyIn,
    required this.buyIn,
  });

  final String preset;
  final int maxPlayers;
  final String password;
  final int smallBlind;
  final int bigBlind;
  final int maxBuyIn;
  final int buyIn;
}

class FriendRoom {
  const FriendRoom({
    required this.roomId,
    required this.code,
    required this.ownerUserId,
    required this.preset,
    required this.rules,
    required this.maxPlayers,
    required this.members,
    required this.revision,
  });

  final String roomId;
  final String code;
  final String ownerUserId;
  final String preset;
  final RoomRules rules;
  final int maxPlayers;
  final List<RoomMember> members;
  final int revision;

  factory FriendRoom.fromJson(Map<String, dynamic> json) => FriendRoom(
    roomId: json['roomId'] as String,
    code: json['code'] as String,
    ownerUserId: json['ownerUserId'] as String,
    preset: json['preset'] as String,
    rules: RoomRules.fromJson(json['rules'] as Map<String, dynamic>),
    maxPlayers: json['maxPlayers'] as int,
    members: (json['members'] as List<dynamic>)
        .map((value) => RoomMember.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    revision: json['revision'] as int,
  );
}

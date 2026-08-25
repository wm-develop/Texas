class RoomRules {
  const RoomRules({
    required this.startingChips,
    required this.smallBlind,
    required this.bigBlind,
    required this.actionSeconds,
  });

  final int startingChips;
  final int smallBlind;
  final int bigBlind;
  final int actionSeconds;

  factory RoomRules.fromJson(Map<String, dynamic> json) => RoomRules(
    startingChips: json['startingChips'] as int,
    smallBlind: json['smallBlind'] as int,
    bigBlind: json['bigBlind'] as int,
    actionSeconds: json['actionSeconds'] as int,
  );
}

class RoomMember {
  const RoomMember({
    required this.userId,
    required this.displayName,
    required this.seat,
    required this.ready,
  });

  final String userId;
  final String displayName;
  final int seat;
  final bool ready;

  factory RoomMember.fromJson(Map<String, dynamic> json) => RoomMember(
    userId: json['userId'] as String,
    displayName: json['displayName'] as String,
    seat: json['seat'] as int,
    ready: json['ready'] as bool,
  );
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

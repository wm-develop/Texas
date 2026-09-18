import 'package:poker_client/features/table/domain/rake_settings.dart';

export 'package:poker_client/features/table/domain/rake_settings.dart';

/// 管理员后台房间列表里的一行：一个开着的房间和它的抽水规则、累计抽水。
class AdminRoom {
  const AdminRoom({
    required this.roomId,
    required this.roomCode,
    required this.ownerName,
    required this.smallBlind,
    required this.bigBlind,
    required this.seatedCount,
    required this.spectatorCount,
    required this.rake,
    required this.rakeTotal,
    required this.rakeHands,
  });

  final String roomId;
  final String roomCode;
  final String ownerName;
  final int smallBlind;
  final int bigBlind;
  final int seatedCount;
  final int spectatorCount;
  final RakeSettings rake;
  final int rakeTotal;
  final int rakeHands;

  factory AdminRoom.fromJson(Map<String, dynamic> json) => AdminRoom(
    roomId: json['roomId'] as String,
    roomCode: json['roomCode'] as String? ?? '',
    ownerName: json['ownerName'] as String? ?? '',
    smallBlind: json['smallBlind'] as int? ?? 0,
    bigBlind: json['bigBlind'] as int? ?? 0,
    seatedCount: json['seatedCount'] as int? ?? 0,
    spectatorCount: json['spectatorCount'] as int? ?? 0,
    rake: RakeSettings.fromJson(
      json['rake'] as Map<String, dynamic>? ?? const {},
    ),
    rakeTotal: json['rakeTotal'] as int? ?? 0,
    rakeHands: json['rakeHands'] as int? ?? 0,
  );
}

/// 一个房间的累计抽水；房间关闭后仍然保留。
class RoomRakeTotal {
  const RoomRakeTotal({
    required this.roomId,
    required this.roomCode,
    required this.closed,
    required this.hands,
    required this.total,
  });

  final String roomId;
  final String roomCode;
  final bool closed;
  final int hands;
  final int total;

  factory RoomRakeTotal.fromJson(Map<String, dynamic> json) => RoomRakeTotal(
    roomId: json['roomId'] as String? ?? '',
    roomCode: json['roomCode'] as String? ?? '',
    closed: json['closed'] as bool? ?? false,
    hands: json['hands'] as int? ?? 0,
    total: json['total'] as int? ?? 0,
  );
}

class RakeSummary {
  const RakeSummary({
    required this.rooms,
    required this.total,
    required this.hands,
  });

  final List<RoomRakeTotal> rooms;
  final int total;
  final int hands;

  factory RakeSummary.fromJson(Map<String, dynamic> json) => RakeSummary(
    rooms: (json['rooms'] as List<dynamic>? ?? const [])
        .map((value) => RoomRakeTotal.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    total: json['total'] as int? ?? 0,
    hands: json['hands'] as int? ?? 0,
  );
}

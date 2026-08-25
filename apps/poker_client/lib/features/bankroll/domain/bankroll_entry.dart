class BankrollEntry {
  const BankrollEntry({
    required this.entryId,
    required this.reason,
    required this.walletDelta,
    required this.tableDelta,
    required this.walletBalanceAfter,
    required this.tableBalanceAfter,
    required this.createdAt,
  });

  final String entryId;
  final String reason;
  final int walletDelta;
  final int tableDelta;
  final int walletBalanceAfter;
  final int tableBalanceAfter;
  final DateTime createdAt;

  factory BankrollEntry.fromJson(Map<String, dynamic> json) => BankrollEntry(
    entryId: json['entryId'] as String,
    reason: json['reason'] as String,
    walletDelta: json['walletDelta'] as int? ?? 0,
    tableDelta: json['tableDelta'] as int? ?? 0,
    walletBalanceAfter: json['walletBalanceAfter'] as int? ?? 0,
    tableBalanceAfter: json['tableBalanceAfter'] as int? ?? 0,
    createdAt: DateTime.parse(json['createdAt'] as String),
  );
}

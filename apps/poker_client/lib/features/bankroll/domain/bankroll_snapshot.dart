class BankrollSnapshot {
  const BankrollSnapshot({
    required this.userId,
    required this.walletChips,
    required this.tableChips,
    required this.revision,
  });

  final String userId;
  final int walletChips;
  final int tableChips;
  final int revision;

  factory BankrollSnapshot.fromJson(Map<String, dynamic> json) =>
      BankrollSnapshot(
        userId: json['userId'] as String,
        walletChips: json['walletChips'] as int,
        tableChips: json['tableChips'] as int? ?? 0,
        revision: json['revision'] as int,
      );
}

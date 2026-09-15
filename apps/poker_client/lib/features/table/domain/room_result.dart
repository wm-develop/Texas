/// 本人在某个房间内的净胜负。
///
/// 桌上筹码计入净胜负：玩家通常在牌局中途查看，此时这一局的盈亏还没有
/// 通过离桌返还落回钱包。
class RoomResult {
  const RoomResult({
    required this.boughtIn,
    required this.returnedToWallet,
    required this.tableChips,
    required this.net,
    this.foldedCommitment = 0,
  });

  /// 本手已弃牌时，这一手已投入底池的筹码。
  ///
  /// 服务端的「桌上筹码」取自成员表，只在结算时更新，牌局中途仍含着已经推进
  /// 底池的那部分。没弃牌时那笔钱归属未定，照常算在桌上；一旦弃牌它就已经
  /// 输掉了，再算成自己的筹码会把净胜负虚高一截。
  final int foldedCommitment;

  /// 弃牌后按已投入 [committed] 修正：桌上筹码与净胜负同时扣减。
  RoomResult withFoldedCommitment(int committed) {
    if (committed <= 0) return this;
    return RoomResult(
      boughtIn: boughtIn,
      returnedToWallet: returnedToWallet,
      tableChips: tableChips - committed,
      net: net - committed,
      foldedCommitment: committed,
    );
  }

  /// 累计从钱包投入牌桌的筹码（带入与补码）。
  final int boughtIn;

  /// 累计从牌桌返还钱包的筹码（离桌返还）。
  final int returnedToWallet;

  /// 此刻仍在牌桌上的筹码。
  final int tableChips;

  /// 净胜负：为正表示赢。
  final int net;

  factory RoomResult.fromJson(Map<String, dynamic> json) => RoomResult(
    boughtIn: json['boughtIn'] as int? ?? 0,
    returnedToWallet: json['returnedToWallet'] as int? ?? 0,
    tableChips: json['tableChips'] as int? ?? 0,
    net: json['net'] as int? ?? 0,
  );
}

/// 筹码与现实金额的换算比例，仅用于熟人之间自行结算的参考。
///
/// 默认 10 元 = 2000 筹码。换算只在客户端本地进行：服务端不存储、不传输
/// 任何金额，产品也不接入支付。
class ChipExchangeRate {
  const ChipExchangeRate({required this.money, required this.chips});

  static const ChipExchangeRate defaultRate = ChipExchangeRate(
    money: 10,
    chips: 2000,
  );

  final double money;
  final int chips;

  bool get isValid => money > 0 && chips > 0;

  /// 把筹码换算成金额；比例非法时返回 null，由界面提示而不是显示错误数字。
  double? convert(int netChips) {
    if (!isValid) return null;
    return netChips * money / chips;
  }
}

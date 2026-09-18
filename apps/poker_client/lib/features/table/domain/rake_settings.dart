/// 一个房间的抽水规则。只有管理员能改，下一手起生效。
///
/// 放在牌桌这边而不是管理后台：牌桌信息栏、加入房间前的预览都要向玩家显示它。
class RakeSettings {
  const RakeSettings({
    this.enabled = false,
    this.basisPoints = 0,
    this.cap = 0,
    this.postflopEnabled = false,
    this.postflopAmount = 0,
  });

  /// 比例上限 10%，与服务端一致。
  static const int maximumBasisPoints = 1000;

  /// 界面上调整比例的步长：0.25%。
  static const int basisPointStep = 25;

  final bool enabled;

  /// 万分比：250 即 2.5%。用整数避免浮点比例在两端算出不同的结果。
  final int basisPoints;

  /// 按比例抽水的封顶筹码；0 表示不封顶。
  final int cap;
  final bool postflopEnabled;

  /// 发出翻牌的手额外加抽的筹码，不受 [cap] 限制，但不能超过一个大盲。
  final int postflopAmount;

  factory RakeSettings.fromJson(Map<String, dynamic> json) => RakeSettings(
    enabled: json['enabled'] as bool? ?? false,
    basisPoints: json['basisPoints'] as int? ?? 0,
    cap: json['cap'] as int? ?? 0,
    postflopEnabled: json['postflopEnabled'] as bool? ?? false,
    postflopAmount: json['postflopAmount'] as int? ?? 0,
  );

  /// 服务端要求五个字段全部传齐：漏传不会被当成「关掉」。
  Map<String, Object?> toJson() => {
    'enabled': enabled,
    'basisPoints': basisPoints,
    'cap': cap,
    'postflopEnabled': postflopEnabled,
    'postflopAmount': postflopAmount,
  };

  /// 开着且真的会抽到筹码。
  bool get takesChips =>
      enabled && (basisPoints > 0 || (postflopEnabled && postflopAmount > 0));

  /// 一句话概括，用在房间列表、牌桌信息栏与加入前的预览里。
  String get summary {
    if (!enabled) return '不抽水';
    final parts = <String>[
      if (basisPoints > 0)
        '${formatBasisPoints(basisPoints)}${cap > 0 ? '，最多 $cap' : '，不封顶'}',
      if (postflopEnabled && postflopAmount > 0) '翻后加抽 $postflopAmount',
    ];
    return parts.isEmpty ? '已开启，但实际不抽' : parts.join('；');
  }
}

/// 万分比写成百分数：250 → 2.5%，500 → 5%。
String formatBasisPoints(int basisPoints) {
  final whole = basisPoints ~/ 100;
  final fraction = basisPoints % 100;
  if (fraction == 0) return '$whole%';
  final text = '$whole.${fraction.toString().padLeft(2, '0')}';
  return '${text.replaceFirst(RegExp(r'0+$'), '')}%';
}

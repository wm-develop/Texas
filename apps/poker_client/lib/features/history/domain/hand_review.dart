/// AI 复盘的结果。服务端把本人这一手的决策连同算好的局面数据交给大模型，
/// 结果按手缓存；只对管理员开通的账号开放。
class HandReview {
  const HandReview({
    required this.handId,
    required this.status,
    this.result,
    this.failure = '',
    this.model = '',
  });

  final String handId;

  /// queued、running、done、failed 之一。
  final String status;
  final ReviewResult? result;

  /// 失败原因码，例如 model_error、invalid_output。
  final String failure;
  final String model;

  bool get inProgress => status == 'queued' || status == 'running';
  bool get done => status == 'done' && result != null;
  bool get failed => status == 'failed';

  factory HandReview.fromJson(Map<String, dynamic> json) => HandReview(
    handId: json['handId'] as String? ?? '',
    status: json['status'] as String? ?? '',
    result: json['result'] == null
        ? null
        : ReviewResult.fromJson(json['result'] as Map<String, dynamic>),
    failure: json['failure'] as String? ?? '',
    model: json['model'] as String? ?? '',
  );

  /// 某一步有没有点评；时间轴上的步号与点评一一对应。
  ReviewDecision? decisionAt(int step) =>
      result?.decisions.where((decision) => decision.step == step).firstOrNull;
}

class ReviewResult {
  const ReviewResult({
    this.situation,
    required this.summary,
    required this.decisions,
    required this.keyLessons,
    required this.opponentNotes,
    required this.hindsight,
  });

  /// 这一手的基本局面，由服务端按交给模型的同一份数据填写。
  final ReviewSituation? situation;
  final String summary;
  final List<ReviewDecision> decisions;
  final List<String> keyLessons;
  final List<String> opponentNotes;
  final String hindsight;

  factory ReviewResult.fromJson(Map<String, dynamic> json) => ReviewResult(
    situation: json['situation'] == null
        ? null
        : ReviewSituation.fromJson(json['situation'] as Map<String, dynamic>),
    summary: json['summary'] as String? ?? '',
    decisions: (json['decisions'] as List<dynamic>? ?? const [])
        .map((value) => ReviewDecision.fromJson(value as Map<String, dynamic>))
        .toList(growable: false),
    keyLessons: (json['keyLessons'] as List<dynamic>? ?? const [])
        .cast<String>(),
    opponentNotes: (json['opponentNotes'] as List<dynamic>? ?? const [])
        .cast<String>(),
    hindsight: json['hindsight'] as String? ?? '',
  );
}

class ReviewSituation {
  const ReviewSituation({
    required this.heroPosition,
    required this.holeCards,
    required this.players,
    required this.smallBlind,
    required this.bigBlind,
    required this.seats,
  });

  final String heroPosition;
  final List<String> holeCards;
  final int players;
  final int smallBlind;
  final int bigBlind;
  final List<ReviewSeat> seats;

  factory ReviewSituation.fromJson(Map<String, dynamic> json) =>
      ReviewSituation(
        heroPosition: json['heroPosition'] as String? ?? '',
        holeCards: (json['holeCards'] as List<dynamic>? ?? const [])
            .cast<String>(),
        players: json['players'] as int? ?? 0,
        smallBlind: json['smallBlind'] as int? ?? 0,
        bigBlind: json['bigBlind'] as int? ?? 0,
        seats: (json['seats'] as List<dynamic>? ?? const [])
            .map((value) => ReviewSeat.fromJson(value as Map<String, dynamic>))
            .toList(growable: false),
      );
}

class ReviewSeat {
  const ReviewSeat({
    required this.position,
    required this.stack,
    required this.stackInBigBlinds,
    required this.isHero,
  });

  final String position;
  final int stack;
  final double stackInBigBlinds;
  final bool isHero;

  factory ReviewSeat.fromJson(Map<String, dynamic> json) => ReviewSeat(
    position: json['position'] as String? ?? '',
    stack: json['startingStack'] as int? ?? 0,
    stackInBigBlinds: (json['stackInBigBlinds'] as num?)?.toDouble() ?? 0,
    isHero: json['isHero'] as bool? ?? false,
  );
}

class ReviewDecision {
  const ReviewDecision({
    required this.step,
    required this.verdict,
    required this.reasoning,
    this.bestAction = '',
    this.equityVsRange,
    this.evTaken,
    this.evBest,
    this.facts,
  });

  final int step;

  /// 好、合理、有争议、失误 之一。
  final String verdict;
  final String reasoning;

  /// 这个决策点的最佳行动。
  final String bestAction;

  /// 模型的估算：对手范围胜率（%）、本次与最佳行动的期望收益（大盲）。
  final double? equityVsRange;
  final double? evTaken;
  final double? evBest;

  /// 服务端算好的精确数字。
  final ReviewDecisionFacts? facts;

  factory ReviewDecision.fromJson(Map<String, dynamic> json) => ReviewDecision(
    step: json['step'] as int? ?? 0,
    verdict: json['verdict'] as String? ?? '',
    reasoning: json['reasoning'] as String? ?? '',
    bestAction: json['bestAction'] as String? ?? '',
    equityVsRange: (json['equityVsRangePercent'] as num?)?.toDouble(),
    evTaken: (json['evTakenBB'] as num?)?.toDouble(),
    evBest: (json['evBestBB'] as num?)?.toDouble(),
    facts: json['facts'] == null
        ? null
        : ReviewDecisionFacts.fromJson(json['facts'] as Map<String, dynamic>),
  );
}

class ReviewDecisionFacts {
  const ReviewDecisionFacts({
    required this.potBefore,
    required this.toCall,
    this.winnablePot = 0,
    this.potOdds,
    this.stackToPotRatio = 0,
    this.effectiveStackInBigBlinds = 0,
    this.madeHand = '',
    this.bestFive = const [],
    this.holeCardsUsed = const [],
    this.boardPlays = false,
    this.equityVsRandom = 0,
  });

  final int potBefore;
  final int toCall;

  /// 本人最多能赢到的底池；只在比 potBefore 小时有值。
  final int winnablePot;

  /// 需跟注时的底池赔率，也就是跟注所需的最低胜率（%）。
  final double? potOdds;
  final double stackToPotRatio;
  final double effectiveStackInBigBlinds;
  final String madeHand;
  final List<String> bestFive;
  final List<String> holeCardsUsed;
  final bool boardPlays;
  final double equityVsRandom;

  factory ReviewDecisionFacts.fromJson(Map<String, dynamic> json) =>
      ReviewDecisionFacts(
        potBefore: json['potBefore'] as int? ?? 0,
        toCall: json['toCall'] as int? ?? 0,
        winnablePot: json['winnablePot'] as int? ?? 0,
        potOdds: (json['potOddsPercent'] as num?)?.toDouble(),
        stackToPotRatio: (json['stackToPotRatio'] as num?)?.toDouble() ?? 0,
        effectiveStackInBigBlinds:
            (json['effectiveStackInBigBlinds'] as num?)?.toDouble() ?? 0,
        madeHand: json['madeHand'] as String? ?? '',
        bestFive: (json['bestFive'] as List<dynamic>? ?? const [])
            .cast<String>(),
        holeCardsUsed: (json['holeCardsUsed'] as List<dynamic>? ?? const [])
            .cast<String>(),
        boardPlays: json['boardPlays'] as bool? ?? false,
        equityVsRandom:
            (json['equityVsRandomHandsPercent'] as num?)?.toDouble() ?? 0,
      );
}

/// 牌面代码写成可读的样子，例如 Ad → A♦。花色后面加文本变体选择符，免得在
/// Android / HarmonyOS 上被系统换成彩色 emoji。
String reviewCardLabel(String code) {
  if (code.length != 2) return code;
  final rank = code[0] == 'T' ? '10' : code[0];
  final suit = switch (code[1]) {
    's' => '♠',
    'h' => '♥',
    'd' => '♦',
    'c' => '♣',
    _ => code[1],
  };
  return '$rank$suit\uFE0E';
}

/// 期望收益写成带正负号的大盲数。
String reviewEvLabel(double value) {
  final text = value.abs() >= 10
      ? value.abs().toStringAsFixed(0)
      : value.abs().toStringAsFixed(1);
  if (text == '0' || text == '0.0') return '0 BB';
  return '${value < 0 ? '−' : '+'}$text BB';
}

/// 回放页用到的复盘接口。为空表示当前账号没有开通或服务端没配置大模型。
class HandReviewApi {
  const HandReviewApi({required this.request, required this.load});

  /// 发起复盘；已有结果或正在分析时服务端直接返回那一条。
  final Future<HandReview> Function(String handId) request;

  /// 查看复盘；没发起过时返回 null。
  final Future<HandReview?> Function(String handId) load;
}

/// 管理员看到的全貌。
class ReviewOverview {
  const ReviewOverview({
    required this.settings,
    required this.accessUserIds,
    required this.requests24h,
    required this.requests30d,
    required this.tokens30d,
    required this.modelConfigured,
    required this.model,
  });

  final ReviewSettings settings;
  final Set<String> accessUserIds;
  final int requests24h;
  final int requests30d;
  final int tokens30d;
  final bool modelConfigured;
  final String model;

  factory ReviewOverview.fromJson(Map<String, dynamic> json) {
    final usage = json['usage'] as Map<String, dynamic>? ?? const {};
    return ReviewOverview(
      settings: ReviewSettings.fromJson(
        json['settings'] as Map<String, dynamic>? ?? const {},
      ),
      accessUserIds: {
        for (final value in json['access'] as List<dynamic>? ?? const [])
          (value as Map<String, dynamic>)['userId'] as String,
      },
      requests24h: usage['requests24h'] as int? ?? 0,
      requests30d: usage['requests30d'] as int? ?? 0,
      tokens30d: usage['tokens30d'] as int? ?? 0,
      modelConfigured: json['modelConfigured'] as bool? ?? false,
      model: json['model'] as String? ?? '',
    );
  }
}

/// 全局设置；两个额度为 0 表示不限。
class ReviewSettings {
  const ReviewSettings({
    this.enabled = true,
    this.dailyLimitPerUser = 0,
    this.monthlyTokenBudget = 0,
  });

  final bool enabled;
  final int dailyLimitPerUser;
  final int monthlyTokenBudget;

  factory ReviewSettings.fromJson(Map<String, dynamic> json) => ReviewSettings(
    enabled: json['enabled'] as bool? ?? true,
    dailyLimitPerUser: json['dailyLimitPerUser'] as int? ?? 0,
    monthlyTokenBudget: json['monthlyTokenBudget'] as int? ?? 0,
  );

  Map<String, Object?> toJson() => {
    'enabled': enabled,
    'dailyLimitPerUser': dailyLimitPerUser,
    'monthlyTokenBudget': monthlyTokenBudget,
  };
}

/// 复盘相关错误码与失败原因的中文说明。
String reviewErrorLabel(String code) => switch (code) {
  'review_unavailable' => 'AI 复盘暂未开放',
  'review_not_allowed' => '你还没有开通 AI 复盘，请联系管理员',
  'review_daily_limit' => '24 小时内的复盘次数已用完，请稍后再来',
  'review_budget_exhausted' => '最近 30 天的复盘额度已用完，请联系管理员',
  'hand_not_found' => '找不到这手牌',
  'review_no_decisions' => '这一手你没有做过决定，没有可复盘的内容',
  'internal_error' => '服务端出错，请稍后重试',
  'model_error' => '大模型服务暂时不可用，请稍后重试',
  'invalid_output' => '这次的分析结果不完整，请重试',
  'output_truncated' => '分析内容超出了输出长度上限，请联系管理员调大或取消上限',
  'replay_unavailable' => '这手牌的记录不完整，无法复盘',
  'history_unavailable' => '读取牌局记录失败，请稍后重试',
  'invalid_review_settings' => '设置不合法：额度要在 0 到上限之间',
  _ => '复盘失败，请稍后重试',
};

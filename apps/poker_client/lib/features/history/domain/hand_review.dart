/// AI 复盘的结果。服务端把本人这一手的决策连同算好的局面数据交给大模型，
/// 结果按手缓存；只对管理员开通的账号开放。
class HandReview {
  const HandReview({
    required this.handId,
    required this.status,
    this.result,
    this.failure = '',
    this.model = '',
    this.attempts = 0,
    this.outdated = false,
    this.previous,
  });

  final String handId;

  /// queued、running、done、failed 之一。
  final String status;
  final ReviewResult? result;

  /// 失败原因码，例如 model_error、invalid_output。
  final String failure;
  final String model;

  /// 大模型服务繁忙时已经自动重试了几次；大于 0 时界面说明正在重试。
  final int attempts;

  /// 这是旧版提示词的结果：新版还没分析过。可以用新版重新分析。
  final bool outdated;

  /// 新版还没有结果（排队、分析中、失败）时，旧版最近一次的结果。
  final ReviewResult? previous;

  bool get inProgress => status == 'queued' || status == 'running';
  bool get done => status == 'done' && result != null;
  bool get failed => status == 'failed';

  /// 结果与旧版结果里模型写的文字都过一遍 [convert]。
  HandReview mapText(String Function(String) convert) => HandReview(
    handId: handId,
    status: status,
    result: result?.mapText(convert),
    failure: failure,
    model: model,
    attempts: attempts,
    outdated: outdated,
    previous: previous?.mapText(convert),
  );

  factory HandReview.fromJson(Map<String, dynamic> json) => HandReview(
    handId: json['handId'] as String? ?? '',
    status: json['status'] as String? ?? '',
    result: json['result'] == null
        ? null
        : ReviewResult.fromJson(json['result'] as Map<String, dynamic>),
    failure: json['failure'] as String? ?? '',
    model: json['model'] as String? ?? '',
    attempts: json['attempts'] as int? ?? 0,
    outdated: json['outdated'] as bool? ?? false,
    previous: json['previous'] == null
        ? null
        : ReviewResult.fromJson(json['previous'] as Map<String, dynamic>),
  );

  /// 某一步有没有点评；时间轴上的步号与点评一一对应。
  ReviewDecision? decisionAt(int step) =>
      result?.decisions.where((decision) => decision.step == step).firstOrNull;
}

class ReviewResult {
  const ReviewResult({
    required this.summary,
    required this.decisions,
    required this.keyLessons,
    required this.opponentNotes,
    required this.hindsight,
  });

  final String summary;
  final List<ReviewDecision> decisions;
  final List<String> keyLessons;
  final List<String> opponentNotes;
  final String hindsight;

  factory ReviewResult.fromJson(Map<String, dynamic> json) => ReviewResult(
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

  /// 把模型写的每一段文字都过一遍 [convert]，数字与结论不动。
  ReviewResult mapText(String Function(String) convert) => ReviewResult(
    summary: convert(summary),
    decisions: [for (final decision in decisions) decision.mapText(convert)],
    keyLessons: keyLessons.map(convert).toList(growable: false),
    opponentNotes: opponentNotes.map(convert).toList(growable: false),
    hindsight: convert(hindsight),
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

  ReviewDecision mapText(String Function(String) convert) => ReviewDecision(
    step: step,
    verdict: verdict,
    reasoning: convert(reasoning),
    bestAction: convert(bestAction),
    equityVsRange: equityVsRange,
    evTaken: evTaken,
    evBest: evBest,
    facts: facts,
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
  final rank = code[0];
  final suit = switch (code[1]) {
    's' => '♠',
    'h' => '♥',
    'd' => '♦',
    'c' => '♣',
    _ => code[1],
  };
  return '$rank$suit\uFE0E';
}

const _suitOfLetter = {'s': '♠', 'h': '♥', 'd': '♦', 'c': '♣'};
const _suitOfName = {
  '黑桃': '♠',
  '红桃': '♥',
  '红心': '♥',
  '方块': '♦',
  '方片': '♦',
  '梅花': '♣',
  '草花': '♣',
};
const _suitOfWord = {
  'spades': '♠',
  'hearts': '♥',
  'diamonds': '♦',
  'clubs': '♣',
};
const _letterOfSuit = {'♠': 's', '♥': 'h', '♦': 'd', '♣': 'c'};

/// 花色符号：黑白两套都认，后面可能跟着 emoji / 文本变体选择符。
final _suitGlyph = RegExp('[♠♥♦♣♤♡♢♧][\\uFE0E\\uFE0F]?');
const _whiteSuits = {'♤': '♠', '♡': '♥', '♢': '♦', '♧': '♣'};

/// 字母写的牌，可以连写（AsKd），点数与花色之间可以有一个空格，花色字母大小写
/// 都认；点数的字母必须大写，免得把英文单词 as 当成黑桃 A。整串前后都不能紧挨
/// 字母或数字：AKs、T9s 里的 Ks、9s 是起手牌范围写法，不是一张牌。
final _letterRun = RegExp(
  r'(?<![A-Za-z0-9])(?:(?:10|[2-9TJQKA]) ?[cdhsCDHS])+(?![A-Za-z0-9])',
);
final _letterCard = RegExp(r'(10|[2-9TJQKA]) ?([cdhsCDHS])');

/// 中文花色名在前：黑桃 A、红桃10。后面跟着量词的是在数张数（「梅花 2 张」是
/// 两张梅花），不是一张牌。
final _namedCard = RegExp(
  r'(黑桃|红桃|红心|方块|方片|梅花|草花) ?(10|[2-9TJQKA])(?![A-Za-z0-9]| ?[张个条枚])',
);

/// 英文全称：9 of clubs。
final _wordCard = RegExp(
  r'(?<![A-Za-z0-9])(10|[2-9TJQKA]) of (spades|hearts|diamonds|clubs)(?![A-Za-z])',
  caseSensitive: false,
);

/// 一手里本人能看到的所有牌（公共牌、发两次的牌面、底牌与摊牌亮出的牌），
/// 也就是发给模型的那些牌。代码统一成 Ah、Td 这样。
Set<String> reviewHandCards(Iterable<String> cards) => {
  for (final card in cards)
    if (card.length == 2) '${card[0].toUpperCase()}${card[1].toLowerCase()}',
};

/// 把模型写的文字里提到的牌统一写成与「牌力」一致的样子：点数加花色符号，
/// 例如 9c、梅花9、9 of clubs 都写成 9♣︎。点数照模型写的保留（T 还是 T）。
///
/// 模型输出无法完全控制，所以分三种情况处理：
/// - 已经是花色符号的（提示词要求模型这样写，多数输出就是这样）：只统一加上文本
///   变体选择符，免得在 Android / HarmonyOS 上被换成彩色 emoji。花色符号不会是
///   别的意思，不用看牌局。
/// - 用字母、中文花色名或英文全称写的：只换 [handCards] 里真有的牌。一手最多
///   十几张牌，没出现过的写法（比如这手没有黑桃 A 时的 As）一律不动，所以不会把
///   别的字误当成牌。
/// - 都认不出来的写法原样保留：宁可没换成符号，也不能换错。
String reviewTextWithSuits(String text, Set<String> handCards) {
  String? symbolFor(String rank, String suit) {
    final code = '${rank == '10' ? 'T' : rank}${_letterOfSuit[suit]}';
    return handCards.contains(code) ? '$rank$suit' : null;
  }

  var result = text.replaceAllMapped(
    _letterRun,
    (run) => run[0]!.replaceAllMapped(
      _letterCard,
      (card) =>
          symbolFor(card[1]!, _suitOfLetter[card[2]!.toLowerCase()]!) ??
          card[0]!,
    ),
  );
  result = result.replaceAllMapped(
    _namedCard,
    (card) => symbolFor(card[2]!, _suitOfName[card[1]!]!) ?? card[0]!,
  );
  result = result.replaceAllMapped(
    _wordCard,
    (card) =>
        symbolFor(
          card[1]!.toUpperCase(),
          _suitOfWord[card[2]!.toLowerCase()]!,
        ) ??
        card[0]!,
  );
  return result.replaceAllMapped(_suitGlyph, (glyph) {
    final symbol = glyph[0]![0];
    return '${_whiteSuits[symbol] ?? symbol}\uFE0E';
  });
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
  const HandReviewApi({required this.request, required this.load, this.prompt});

  /// 发起复盘；已有结果或正在分析时服务端直接返回那一条。
  final Future<HandReview> Function(String handId) request;

  /// 查看复盘；没发起过时返回 null。
  final Future<HandReview?> Function(String handId) load;

  /// 这一手按当前版本发给模型的提示词；为空时不提供「复制提示词和结果」。
  final Future<ReviewPrompt> Function(String handId)? prompt;
}

/// 某一手按当前版本的提示词发给模型的全部文字：系统提示词与数据（用户消息）。
class ReviewPrompt {
  const ReviewPrompt({
    required this.promptVersion,
    required this.system,
    required this.user,
  });

  factory ReviewPrompt.fromJson(Map<String, dynamic> json) => ReviewPrompt(
    promptVersion: json['promptVersion'] as String? ?? '',
    system: json['system'] as String? ?? '',
    user: json['user'] as String? ?? '',
  );

  final String promptVersion;
  final String system;
  final String user;
}

/// 管理员看到的全貌。
class ReviewOverview {
  const ReviewOverview({
    required this.settings,
    required this.access,
    required this.requests24h,
    required this.requests30d,
    required this.tokens30d,
    required this.modelConfigured,
    required this.model,
    this.workers = 1,
    this.modelHealthFailure = '',
    this.modelHealthAt,
    this.modelCoolingDown = false,
  });

  final ReviewSettings settings;

  /// 开通名单，按账号查单独额度。
  final Map<String, ReviewUserLimits> access;
  final int requests24h;
  final int requests30d;
  final int tokens30d;
  final bool modelConfigured;
  final String model;

  /// 服务端同时分析的条数。
  final int workers;

  /// 最近一次余额不足或密钥失效；为空表示正常。
  final String modelHealthFailure;
  final DateTime? modelHealthAt;

  /// 还在冷却期、拒绝新的复盘；为假时冷却已过，等下一次调用确认是否恢复。
  final bool modelCoolingDown;

  Set<String> get accessUserIds => access.keys.toSet();

  factory ReviewOverview.fromJson(Map<String, dynamic> json) {
    final usage = json['usage'] as Map<String, dynamic>? ?? const {};
    final health = json['modelHealth'] as Map<String, dynamic>?;
    return ReviewOverview(
      settings: ReviewSettings.fromJson(
        json['settings'] as Map<String, dynamic>? ?? const {},
      ),
      access: {
        for (final value in json['access'] as List<dynamic>? ?? const [])
          (value as Map<String, dynamic>)['userId'] as String:
              ReviewUserLimits.fromJson(value),
      },
      requests24h: usage['requests24h'] as int? ?? 0,
      requests30d: usage['requests30d'] as int? ?? 0,
      tokens30d: usage['tokens30d'] as int? ?? 0,
      modelConfigured: json['modelConfigured'] as bool? ?? false,
      model: json['model'] as String? ?? '',
      workers: json['workers'] as int? ?? 1,
      modelHealthFailure: health?['failure'] as String? ?? '',
      modelHealthAt: health?['at'] == null
          ? null
          : DateTime.tryParse(health!['at'] as String),
      modelCoolingDown: health?['coolingDown'] as bool? ?? false,
    );
  }
}

/// 某个人的单独额度；为空的一项跟随全局，0 表示不限。
class ReviewUserLimits {
  const ReviewUserLimits({this.dailyLimit, this.maxInFlight});

  final int? dailyLimit;
  final int? maxInFlight;

  factory ReviewUserLimits.fromJson(Map<String, dynamic> json) =>
      ReviewUserLimits(
        dailyLimit: json['dailyLimit'] as int?,
        maxInFlight: json['maxInFlight'] as int?,
      );
}

/// 全局设置；额度为 0 表示不限。
class ReviewSettings {
  const ReviewSettings({
    this.enabled = true,
    this.dailyLimitPerUser = 0,
    this.maxInFlightPerUser = 0,
    this.monthlyTokenBudget = 0,
  });

  final bool enabled;
  final int dailyLimitPerUser;

  /// 每人同时最多几条在排队或分析中。
  final int maxInFlightPerUser;
  final int monthlyTokenBudget;

  factory ReviewSettings.fromJson(Map<String, dynamic> json) => ReviewSettings(
    enabled: json['enabled'] as bool? ?? true,
    dailyLimitPerUser: json['dailyLimitPerUser'] as int? ?? 0,
    maxInFlightPerUser: json['maxInFlightPerUser'] as int? ?? 0,
    monthlyTokenBudget: json['monthlyTokenBudget'] as int? ?? 0,
  );

  Map<String, Object?> toJson() => {
    'enabled': enabled,
    'dailyLimitPerUser': dailyLimitPerUser,
    'maxInFlightPerUser': maxInFlightPerUser,
    'monthlyTokenBudget': monthlyTokenBudget,
  };
}

/// 复盘相关错误码与失败原因的中文说明。
String reviewErrorLabel(String code) => switch (code) {
  'review_unavailable' => 'AI 复盘暂未开放',
  'review_not_allowed' => '你还没有开通 AI 复盘，请联系管理员',
  'review_daily_limit' => '24 小时内的复盘次数已用完，请稍后再来',
  'review_in_flight_limit' => '你同时在分析的牌局已达上限，请等之前的分析完成后再发起',
  'review_budget_exhausted' => '最近 30 天的复盘额度已用完，请联系管理员',
  'hand_not_found' => '找不到这手牌',
  'review_no_decisions' => '这一手你没有做过决定，没有可复盘的内容',
  'internal_error' => '服务端出错，请稍后重试',
  'model_error' => '大模型服务暂时不可用，请稍后重试',
  'model_busy' => '大模型服务繁忙，已自动重试多次仍未成功，请稍后再试',
  'model_timeout' => '大模型分析超时，请稍后再试；一直超时请联系管理员调大超时时间',
  'model_insufficient_balance' => '大模型账户余额不足，暂时无法分析，请联系管理员',
  'model_unauthorized' => '大模型服务的密钥无效，暂时无法分析，请联系管理员',
  'invalid_output' => '这次的分析结果不完整，请重试',
  'output_truncated' => '分析内容超出了输出长度上限，请联系管理员调大或取消上限',
  'replay_unavailable' => '这手牌的记录不完整，无法复盘',
  'history_unavailable' => '读取牌局记录失败，请稍后重试',
  'invalid_review_settings' => '设置不合法：额度要在 0 到上限之间',
  _ => '复盘失败，请稍后重试',
};

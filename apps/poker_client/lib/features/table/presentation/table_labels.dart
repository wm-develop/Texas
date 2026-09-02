import 'dart:math' as math;

import 'package:poker_client/features/table/domain/table_snapshot.dart';

/// 牌桌界面的纯展示映射：把服务端下发的英文枚举、错误码和金额转换为
/// 中文界面文案。全部为纯函数，与 Widget 无关，可独立测试。
///
/// 客户端不得直接显示 `one_pair`、`stale_revision` 之类的内部枚举，
/// 所有这类转换都集中在本文件。

String cardRank(String card) {
  if (card.length != 2) return '?';
  return switch (card[0]) {
    'T' => '10',
    'J' => 'J',
    'Q' => 'Q',
    'K' => 'K',
    'A' => 'A',
    final value => value,
  };
}

int remainingSeconds(Duration duration) {
  if (duration <= Duration.zero) return 0;
  return (duration.inMilliseconds / 1000).ceil();
}

String actionLabel(String action, int actionTo) => switch (action) {
  'fold' => '弃牌',
  'check' => '过牌',
  'call' => '跟注至 $actionTo',
  'bet' => '下注至 $actionTo',
  'raise' => '加注至 $actionTo',
  'all_in' => '全下至 $actionTo',
  _ => action,
};

String suggestionLabel(String label) => switch (label) {
  'min_raise' => '最小加注',
  'max_raise' => '最大加注',
  'quarter_pot' => '1/4 池',
  'third_pot' => '1/3 池',
  'half_pot' => '1/2 池',
  'two_thirds_pot' => '2/3 池',
  'pot' => '满池',
  'overbet_120' => '1.2× 超池',
  'all_in' => '全下',
  _ => label,
};

String suggestionButtonLabel(BetSuggestion suggestion, int streetBet) {
  final committed = math.max(0, suggestion.raiseTo - streetBet);
  if (suggestion.action == 'all_in') {
    return '全下\n投入 $committed';
  }
  return '${suggestionLabel(suggestion.label)}\n'
      '投入 $committed · 至 ${suggestion.raiseTo}';
}

String potAwardLabel(PotAward award, List<TableSeatSnapshot> seats) {
  final names = award.payouts
      .map((payout) {
        final name = seats
            .where((seat) => seat.userId == payout.userId)
            .map((seat) => seat.displayName)
            .firstOrNull;
        return '${name ?? payout.userId} +${payout.amount}';
      })
      .join('、');
  final potName = award.potIndex == 0 ? '主池' : '边池 ${award.potIndex}';
  final runout = award.runoutIndex > 0 ? '第${award.runoutIndex}次 · ' : '';
  return '$runout$potName ${award.amount}：$names';
}

String gameErrorLabel(String code) => switch (code) {
  'connection_failed' => '牌桌网络暂时不可用，正在自动重新连接',
  'server_draining' => '服务器即将更新，本手结束后暂停开新局，请稍候',
  'sequence_gap' => '检测到网络消息缺口，正在恢复牌桌状态',
  'stale_revision' || 'stale_hand' => '牌桌状态已经更新，正在为你恢复最新画面',
  'not_your_turn' => '现在还没有轮到你',
  'illegal_action' => '当前不能执行这个操作',
  'invalid_amount' => '下注额度不在允许范围内',
  'rate_limited' => '消息发送太快，请稍后再试',
  'chat_muted' => '你已被管理员禁言，暂时不能发送牌桌文字消息',
  'content_rejected' => '消息内容不符合要求',
  'authentication_required' => '登录状态已失效，请重新登录',
  'no_time_extensions' => '本手的两张加时卡已经用完',
  'time_extension_expired' => '本次行动已经超时，无法再主动加时',
  'insufficient_wallet_chips' => '账户筹码不足，请返回大厅充值',
  'maximum_buy_in_exceeded' => '本次补码会超过房间最大带入',
  'hand_in_progress' => '只能在两手牌之间补码',
  'rebuy_required' => '筹码已用完，请先补码再准备',
  'hole_card_view_not_available' => '只有本手已弃牌的玩家才能申请查看仍在本手中的玩家手牌',
  'hole_card_view_request_not_found' => '这条看牌申请已经失效',
  'invalid_seat_swap' => '不能与该目标交换座位',
  'seat_swap_request_not_found' => '这条换位申请已经失效',
  'runout_choice_not_available' => '当前不在发牌次数选择阶段',
  'invalid_player_interaction' => '请选择同桌的其他玩家进行互动',
  'player_not_at_table' => '该玩家已经离开牌桌',
  'player_interaction_too_frequent' => '互动发送太快，请稍后再试',
  _ when code.startsWith('invalid_server_message') => '收到的牌桌数据无法解析',
  _ => '牌桌操作失败（$code）',
};

String cardSuit(String card) {
  if (card.length != 2) return '';
  return switch (card[1]) {
    'c' => '♣',
    'd' => '♦',
    'h' => '♥',
    's' => '♠',
    _ => '',
  };
}

String phaseLabel(String? phase) => switch (phase) {
  null => '正在同步牌桌…',
  'WAITING' => '等待玩家准备',
  'WAITING_NEXT_HAND' => '本手已结算，等待下一手',
  'PREFLOP' => '翻牌前',
  'FLOP' => '翻牌圈',
  'TURN' => '转牌圈',
  'RIVER' => '河牌圈',
  'RUNOUT_CHOICE' => '全下，等待选择发牌次数',
  'SHOWDOWN' => '摊牌',
  _ => phase,
};

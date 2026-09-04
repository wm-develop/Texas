import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';

/// 房主的房间管理：开关房间入口、把成员移出房间。
///
/// 服务端只在手间允许踢人（牌局进行中把人踢走会牵扯底池归属与行动顺序），
/// 这里同步按快照阶段禁用按钮，让不可用状态在点之前就看得见。
class RoomManagementDialog extends StatefulWidget {
  const RoomManagementDialog({
    required this.snapshot,
    required this.currentUserId,
    required this.joinLocked,
    required this.onSetJoinLocked,
    required this.onRemoveMember,
    required this.spectatorSettings,
    required this.onUpdateSpectatorSettings,
    super.key,
  });

  final TableSnapshot? snapshot;
  final String currentUserId;
  final bool joinLocked;
  final Future<bool> Function(bool locked) onSetJoinLocked;
  final Future<void> Function(String userId) onRemoveMember;

  /// 观战位当前设置；改动通过 [onUpdateSpectatorSettings] 发给服务端，
  /// 服务端立即生效并随快照广播。
  final SpectatorSettings spectatorSettings;
  final void Function(SpectatorSettings settings) onUpdateSpectatorSettings;

  @override
  State<RoomManagementDialog> createState() => _RoomManagementDialogState();
}

class _RoomManagementDialogState extends State<RoomManagementDialog> {
  late bool _locked = widget.joinLocked;
  late SpectatorSettings _spectator = widget.spectatorSettings;
  late final TextEditingController _fee = TextEditingController(
    text: '${widget.spectatorSettings.feeBigBlinds}',
  );
  String? _feeError;
  bool _busy = false;

  @override
  void dispose() {
    _fee.dispose();
    super.dispose();
  }

  void _updateSpectator(SpectatorSettings next) {
    setState(() => _spectator = next);
    widget.onUpdateSpectatorSettings(next);
  }

  /// 看牌费只在提交时生效，避免每敲一个数字就发一次请求。
  void _saveFee() {
    final value = int.tryParse(_fee.text.trim());
    if (value == null || value < 0 || value > SpectatorSettings.maxFeeBigBlinds) {
      setState(() => _feeError = '需在 0～${SpectatorSettings.maxFeeBigBlinds} 之间');
      return;
    }
    setState(() => _feeError = null);
    if (value != _spectator.feeBigBlinds) {
      _updateSpectator(_spectator.copyWith(feeBigBlinds: value));
    }
  }
  String? _error;
  final Set<String> _removed = {};

  /// 牌局进行中不能踢人，与服务端的判断保持一致。
  bool get _betweenHands {
    final phase = widget.snapshot?.phase;
    return phase == null || phase == 'WAITING' || phase == 'WAITING_NEXT_HAND';
  }

  Future<void> _run(Future<void> Function() operation) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await operation();
    } on Object catch (error) {
      if (mounted) setState(() => _error = _message(error));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  String _message(Object error) {
    if (error is GameApiTimeoutException) return '服务器响应超时，请稍后重试';
    if (error is GameApiException) {
      return switch (error.code) {
        'owner_required' => '只有房主可以进行房间管理',
        'hand_in_progress' => '本手牌正在进行，请等结算后再操作',
        'cannot_remove_self' => '不能把自己移出房间',
        'cannot_remove_administrator' => '管理员账号不能被房主移出房间',
        'member_not_found' => '该玩家已经不在房间中',
        'room_not_found' => '你已经不在任何房间中',
        _ => '操作失败（${error.code}）',
      };
    }
    return '无法连接游戏服务';
  }

  @override
  Widget build(BuildContext context) {
    final seats = widget.snapshot?.seats ?? const <TableSeatSnapshot>[];
    final others = seats
        .where((seat) => seat.userId != widget.currentUserId)
        .where((seat) => !_removed.contains(seat.userId))
        .toList(growable: false);
    return AlertDialog(
      title: const Text('房间管理'),
      content: SizedBox(
        width: 380,
        // 人多时列表会很长，手机横屏更放不下，一律可滚动
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                title: const Text('允许新玩家加入'),
                subtitle: Text(
                  _locked ? '房间入口已关闭，房内玩家不受影响' : '其他人可以用房间码加入',
                ),
                value: !_locked,
                onChanged: _busy
                    ? null
                    : (allow) => _run(() async {
                        final locked = await widget.onSetJoinLocked(!allow);
                        if (mounted) setState(() => _locked = locked);
                      }),
              ),
              const Divider(height: 20),
              const Text('观战位', style: TextStyle(fontWeight: FontWeight.w700)),
              const SizedBox(height: 6),
              Row(
                children: [
                  const Expanded(
                    child: Text(
                      '看牌费（大盲倍数，0 为免费）',
                      style: TextStyle(fontSize: 13),
                    ),
                  ),
                  SizedBox(
                    width: 84,
                    child: TextField(
                      key: const ValueKey('spectator-fee-field'),
                      controller: _fee,
                      keyboardType: TextInputType.number,
                      textAlign: TextAlign.center,
                      enabled: !_busy,
                      onSubmitted: (_) => _saveFee(),
                      onTapOutside: (_) => _saveFee(),
                      decoration: InputDecoration(
                        isDense: true,
                        border: const OutlineInputBorder(),
                        suffixText: 'BB',
                        errorText: _feeError,
                      ),
                    ),
                  ),
                ],
              ),
              // 观战者能看到所有人的手牌，念底牌给同伴听是真实风险；这三个开关
              // 让房主决定放开到什么程度。服务端同样校验，这里只是入口。
              SwitchListTile(
                key: const ValueKey('spectator-voice-switch'),
                contentPadding: EdgeInsets.zero,
                dense: true,
                title: const Text('允许观战者开麦'),
                value: _spectator.voiceAllowed,
                onChanged: _busy
                    ? null
                    : (value) =>
                          _updateSpectator(_spectator.copyWith(voiceAllowed: value)),
              ),
              SwitchListTile(
                key: const ValueKey('spectator-chat-switch'),
                contentPadding: EdgeInsets.zero,
                dense: true,
                title: const Text('允许观战者发文字'),
                value: _spectator.chatAllowed,
                onChanged: _busy
                    ? null
                    : (value) =>
                          _updateSpectator(_spectator.copyWith(chatAllowed: value)),
              ),
              SwitchListTile(
                key: const ValueKey('spectator-emote-switch'),
                contentPadding: EdgeInsets.zero,
                dense: true,
                title: const Text('允许观战者发赞赏/嘲讽'),
                value: _spectator.emoteAllowed,
                onChanged: _busy
                    ? null
                    : (value) =>
                          _updateSpectator(_spectator.copyWith(emoteAllowed: value)),
              ),
              const Divider(height: 20),
              Row(
                children: [
                  const Text(
                    '移出玩家',
                    style: TextStyle(fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(width: 8),
                  if (!_betweenHands)
                    const Expanded(
                      child: Text(
                        '本手牌进行中，结算后才能移出',
                        style: TextStyle(color: Colors.white54, fontSize: 12),
                      ),
                    ),
                ],
              ),
              const SizedBox(height: 6),
              if (others.isEmpty)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 10),
                  child: Text(
                    '房间里还没有其他玩家',
                    style: TextStyle(color: Colors.white54),
                  ),
                )
              else
                for (final seat in others)
                  ListTile(
                    contentPadding: EdgeInsets.zero,
                    dense: true,
                    title: Text(seat.displayName),
                    subtitle: Text(
                      '座位 ${seat.seat} · 筹码 ${seat.stack}'
                      '${seat.connected ? '' : ' · 已断线'}',
                      style: const TextStyle(fontSize: 12),
                    ),
                    trailing: TextButton(
                      onPressed: _busy || !_betweenHands
                          ? null
                          : () => _confirmRemove(seat),
                      style: TextButton.styleFrom(
                        foregroundColor: Colors.redAccent,
                      ),
                      child: const Text('移出'),
                    ),
                  ),
              if (_error != null) ...[
                const SizedBox(height: 8),
                Text(
                  _error!,
                  style: const TextStyle(color: Colors.redAccent),
                ),
              ],
            ],
          ),
        ),
      ),
      actions: [
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('完成'),
        ),
      ],
    );
  }

  Future<void> _confirmRemove(TableSeatSnapshot seat) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('移出玩家'),
        content: Text(
          '确定把「${seat.displayName}」移出房间吗？\n'
          '他牌桌上的 ${seat.stack} 筹码会退回他自己的钱包。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.redAccent),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('移出'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    await _run(() async {
      await widget.onRemoveMember(seat.userId);
      if (mounted) setState(() => _removed.add(seat.userId));
    });
  }
}

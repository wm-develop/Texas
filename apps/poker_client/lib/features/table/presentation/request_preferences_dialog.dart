import 'package:flutter/material.dart';
import 'package:poker_client/core/widgets/dialog_scroll_area.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';

/// 「换座与看牌申请」偏好弹窗。两个开关即时发给服务端，只在本房间内有效，
/// 离开房间后恢复为全部允许。
class RequestPreferencesDialog extends StatefulWidget {
  const RequestPreferencesDialog({
    super.key,
    required this.initial,
    required this.onChanged,
    this.updates,
    this.current,
  });

  final RequestPreferences initial;

  /// 有新快照时通知；配合 [current] 让开关跟着服务端的真实值走。
  /// 命令在连接断掉的瞬间丢失、或服务端手间重启把偏好重置时，弹窗不会停在
  /// 一个服务端并不认的位置。
  final Listenable? updates;
  final RequestPreferences? Function()? current;

  /// 返回 false 表示命令没有发出去（断线重连中），开关不能拨到新位置。
  final bool Function(RequestPreferences) onChanged;

  @override
  State<RequestPreferencesDialog> createState() =>
      _RequestPreferencesDialogState();
}

class _RequestPreferencesDialogState extends State<RequestPreferencesDialog> {
  late RequestPreferences _preferences = widget.initial;
  String? _error;

  @override
  void initState() {
    super.initState();
    widget.updates?.addListener(_syncFromServer);
  }

  @override
  void dispose() {
    widget.updates?.removeListener(_syncFromServer);
    super.dispose();
  }

  void _syncFromServer() {
    final latest = widget.current?.call();
    if (latest == null || latest == _preferences || !mounted) return;
    setState(() => _preferences = latest);
  }

  void _update(RequestPreferences next) {
    // 断线重连期间命令会被静默丢弃；先拨开关再发送会让开关停在新位置而
    // 服务端还是旧值，玩家以为已经生效。与房间管理弹窗同一处理。
    if (!widget.onChanged(next)) {
      setState(() => _error = '还没连上牌桌，请稍后再试');
      return;
    }
    setState(() {
      _preferences = next;
      _error = null;
    });
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('换座与看牌申请'),
      content: SizedBox(
        width: 420,
        child: DialogScrollArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              SwitchListTile(
                key: const ValueKey('request-pref-seat-swap'),
                contentPadding: EdgeInsets.zero,
                title: const Text('允许其他玩家向我申请换座'),
                subtitle: const Text('关闭后对方的申请会被直接拒绝，你不会收到提示'),
                value: _preferences.allowSeatSwapRequests,
                onChanged: (value) => _update(
                  _preferences.copyWith(allowSeatSwapRequests: value),
                ),
              ),
              SwitchListTile(
                key: const ValueKey('request-pref-hole-card-view'),
                contentPadding: EdgeInsets.zero,
                title: const Text('允许其他玩家申请私下看我的牌'),
                subtitle: const Text('关闭后对方的申请会被直接拒绝，你不会收到提示'),
                value: _preferences.allowHoleCardViewRequests,
                onChanged: (value) => _update(
                  _preferences.copyWith(allowHoleCardViewRequests: value),
                ),
              ),
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(
                    _error!,
                    key: const ValueKey('request-pref-error'),
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                      fontSize: 12,
                    ),
                  ),
                ),
              const Padding(
                padding: EdgeInsets.only(top: 8),
                child: Text(
                  '以上设置只在本房间内有效，离开房间后恢复为允许。',
                  style: TextStyle(fontSize: 12),
                ),
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('关闭'),
        ),
      ],
    );
  }
}

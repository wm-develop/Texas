import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';

/// 牌桌文字聊天面板。面板可能位于独立弹窗路由中，
/// 因此自行监听 socket，而不依赖牌桌页面的 setState。

class TableChatPanel extends StatefulWidget {
  const TableChatPanel({
    required this.client,
    required this.currentUserId,
    required this.blockedUserIds,
    required this.onBlockChanged,
    required this.onClose,
    super.key,
  });

  final GameSocketClient client;
  final String currentUserId;
  final Set<String> blockedUserIds;
  final void Function(String userId, bool blocked) onBlockChanged;
  final VoidCallback onClose;

  @override
  State<TableChatPanel> createState() => TableChatPanelState();
}

class TableChatPanelState extends State<TableChatPanel> {
  final _controller = TextEditingController();

  @override
  void initState() {
    super.initState();
    // The panel can live inside a dialog route, where the page's own
    // setState never reaches it. Listen to the socket directly so incoming
    // and just-sent messages render immediately.
    widget.client.addListener(_onClientChanged);
  }

  @override
  void didUpdateWidget(covariant TableChatPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(oldWidget.client, widget.client)) {
      oldWidget.client.removeListener(_onClientChanged);
      widget.client.addListener(_onClientChanged);
    }
  }

  @override
  void dispose() {
    widget.client.removeListener(_onClientChanged);
    _controller.dispose();
    super.dispose();
  }

  void _onClientChanged() {
    if (mounted) setState(() {});
  }

  @override
  Widget build(BuildContext context) {
    final messages = widget.client.chatMessages
        .where((message) => !widget.blockedUserIds.contains(message.userId))
        .toList(growable: false);
    return Card(
      color: const Color(0xE6112621),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                const Expanded(
                  child: Text(
                    '牌桌聊天',
                    style: TextStyle(fontWeight: FontWeight.w700),
                  ),
                ),
                if (widget.blockedUserIds.isNotEmpty)
                  IconButton(
                    onPressed: _showBlockedUsers,
                    icon: const Icon(Icons.person_off_outlined),
                    tooltip: '已屏蔽玩家',
                  ),
                IconButton(
                  onPressed: widget.onClose,
                  icon: const Icon(Icons.close),
                  tooltip: '关闭聊天',
                ),
              ],
            ),
            const Divider(height: 12),
            Expanded(
              child: messages.isEmpty
                  ? const Center(
                      child: Text(
                        '还没有消息',
                        style: TextStyle(color: Colors.white38),
                      ),
                    )
                  // reverse 列表让视口固定在最新消息：index 0 渲染在底部，
                  // 新消息到达时无需手动滚动；向上滚动查看历史时位置不被打断。
                  : ListView.builder(
                      reverse: true,
                      itemCount: messages.length,
                      itemBuilder: (context, index) {
                        final message = messages[messages.length - 1 - index];
                        return TableChatLine(
                          name: message.displayName,
                          message: message.content,
                          canBlock: message.userId != widget.currentUserId,
                          onBlock: () =>
                              widget.onBlockChanged(message.userId, true),
                        );
                      },
                    ),
            ),
            TextField(
              controller: _controller,
              maxLength: 200,
              onSubmitted: (_) => _send(),
              decoration: InputDecoration(
                counterText: '',
                hintText: '输入牌桌消息',
                border: const OutlineInputBorder(),
                isDense: true,
                suffixIcon: IconButton(
                  onPressed: _send,
                  icon: const Icon(Icons.send),
                ),
              ),
            ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 6,
              children: [
                ActionChip(
                  label: const Text('好牌'),
                  onPressed: () =>
                      widget.client.sendChat('好牌', kind: 'quick_text'),
                ),
                ActionChip(
                  label: const Text('快一点'),
                  onPressed: () =>
                      widget.client.sendChat('快一点', kind: 'quick_text'),
                ),
                ActionChip(
                  label: const Text('👍'),
                  onPressed: () => widget.client.sendChat('👍', kind: 'emoji'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  void _send() {
    widget.client.sendChat(_controller.text);
    _controller.clear();
  }

  void _showBlockedUsers() {
    // 观战者也能发言、也可能被屏蔽；昵称要从座位和观战位两边找
    final snapshot = widget.client.snapshot;
    final names = {
      for (final seat in snapshot?.seats ?? const <TableSeatSnapshot>[])
        seat.userId: seat.displayName,
      for (final spectator
          in snapshot?.spectators ?? const <SpectatorSnapshot>[])
        spectator.userId: spectator.displayName,
    };
    showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('已屏蔽玩家'),
        content: SizedBox(
          width: 320,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (final userId in widget.blockedUserIds)
                ListTile(
                  title: Text(names[userId] ?? userId),
                  subtitle: const Text('文字和语音均已屏蔽'),
                  trailing: TextButton(
                    onPressed: () {
                      widget.onBlockChanged(userId, false);
                      Navigator.of(context).pop();
                    },
                    child: const Text('取消屏蔽'),
                  ),
                ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('关闭'),
          ),
        ],
      ),
    );
  }
}

class TableChatLine extends StatelessWidget {
  const TableChatLine({
    required this.name,
    required this.message,
    required this.canBlock,
    required this.onBlock,
    super.key,
  });

  final String name;
  final String message;
  final bool canBlock;
  final VoidCallback onBlock;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: Padding(
            padding: const EdgeInsets.only(bottom: 9),
            child: Text.rich(
              TextSpan(
                children: [
                  TextSpan(
                    text: '$name：',
                    style: const TextStyle(color: Color(0xFFF4D477)),
                  ),
                  TextSpan(text: message),
                ],
              ),
            ),
          ),
        ),
        if (canBlock)
          PopupMenuButton<String>(
            padding: EdgeInsets.zero,
            iconSize: 16,
            tooltip: '消息选项',
            onSelected: (_) => onBlock(),
            itemBuilder: (context) => const [
              PopupMenuItem(value: 'block', child: Text('屏蔽此玩家')),
            ],
          ),
      ],
    );
  }
}

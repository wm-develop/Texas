import 'package:flutter/material.dart';

/// 隐私说明正文。与 docs/PRIVACY_NOTICE.md 保持一致；修改其一时同步另一处。
const privacyNoticeText = '''
「好友德州」是供熟人组织私人牌局的娱乐应用。以下说明我们保存什么、不保存什么，以及你能做什么。

一、我们保存的信息
• 登录用户名、牌桌昵称和密码的不可逆哈希值（不保存明文密码）。
• 娱乐筹码余额和每一笔筹码变动的流水，用于牌局结算与对账。
• 牌局记录：每手牌的参与者、行动、公共牌、结算结果，以及你在该手中的底牌。
• 牌桌文字聊天消息，最多保留最近若干条用于断线恢复。
• 管理操作的审计记录，例如管理员调整筹码或禁言。
• 为保障服务正常运行，服务器会在日志中记录请求来源 IP 和错误信息，用于限流与排障。

二、我们不保存的信息
• 不保存手机号、邮箱、真实姓名或任何证件信息——注册只需要用户名和密码。
• 不保存语音内容。语音通过腾讯云 TRTC 实时传输，游戏服务不接触、不录制音频。
• 不接入支付。筹码没有现实价值，不能兑换、提现或交易。

三、谁能看到
• 同桌玩家能看到你的昵称、筹码、行动、公开的牌和聊天内容；你的底牌只在摊牌或你主动公开时可见。
• 服务器管理员可以查看账号列表、筹码、在线状态和审计记录，用于维护牌局秩序。
• 本服务由朋友自行搭建运行，数据保存在搭建者自己的服务器上，不会提供给任何第三方。

四、你的选择
• 你可以随时在「个人信息」中修改用户名、昵称和密码。
• 你可以自行注销账号：注销后原用户名可被重新注册，账号被标记为已注销且不能再登录；钱包中剩余的娱乐筹码会转入服务器管理员账户并记录来源；牌局历史与账本作为其他玩家结算记录的一部分予以保留，但不再关联你的用户名。
• 若对数据有其他疑问，请直接联系搭建这台服务器的朋友。
''';

/// 以对话框展示完整隐私说明。
Future<void> showPrivacyNoticeDialog(BuildContext context) {
  return showDialog<void>(
    context: context,
    builder: (context) => AlertDialog(
      title: const Text('隐私说明'),
      content: SizedBox(
        width: 480,
        child: SingleChildScrollView(
          child: Text(
            privacyNoticeText.trim(),
            style: const TextStyle(height: 1.5),
          ),
        ),
      ),
      actions: [
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('我知道了'),
        ),
      ],
    ),
  );
}

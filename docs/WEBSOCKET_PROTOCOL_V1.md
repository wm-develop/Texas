# WebSocket 协议 v1

状态：当前 v1 协议基线（按 `v0.1.1` 源码校正）。牌局状态以服务端按接收者裁剪的快照为唯一事实来源；新增消息时必须同步更新服务端常量、客户端分发、测试和本文。

> 2026-09-01 校正说明：本文此前描述的是早期**事件驱动**设计（`table.hand.started`、`table.board.dealt`、`table.action.required` 等细粒度事件）。实现最终收敛为**快照驱动**：牌局状态变化统一通过 `table.snapshot` 下发，细粒度手牌事件从未实现。本文已按实际源码重写第 3、5、6、7 节。历史设计中未实现的消息类型集中记录在 5.5 节。

## 1. 连接与编码

- 传输：生产环境使用 `wss`，本地开发可使用 `ws`。
- 编码：UTF-8 JSON 文本帧。
- 服务端对每条连接设置 16 KiB 读取上限（`SetReadLimit`）。超限由 WebSocket 库直接以协议级关闭帧断开连接，**不会**先发送应用层错误消息。
- 客户端连接后先发送 `session.authenticate` 完成会话鉴权，再发送 `table.join` 加入牌桌。除 `system.ping` 和 `session.authenticate` 外，未鉴权的消息一律返回 `authentication_required`。
- 心跳：客户端发送 `system.ping`，服务端回复 `system.pong`。该心跳同时用于刷新管理台的在线状态（客户端每 30 秒一次，90 秒无心跳视为离线）。
- `version` 允许为 `1` 或缺省（`0` 按 1 处理）。其他值返回 `unsupported_protocol_version`。
- 服务端**不发送**连接问候消息。`system.hello` 仅作为常量保留，无任何发送点；客户端不应等待它。

## 2. 统一消息外壳

```json
{
  "version": 1,
  "type": "table.snapshot",
  "requestId": "req_01J...",
  "sequence": 128,
  "tableId": "room_01J...",
  "handId": "hand_01J...",
  "tableRevision": 52,
  "serverTime": 1787540000000,
  "payload": {}
}
```

| 字段 | 方向 | 说明 |
|---|---|---|
| `version` | 双向 | 协议主版本，当前为 1 |
| `type` | 双向 | 消息类型 |
| `requestId` | 双向 | 客户端生成；命令回执原样带回，用于幂等和关联；广播事件不带 |
| `sequence` | 服务端→客户端 | 单牌桌严格递增的事件序号。**仅 `table.snapshot` 和广播事件携带**；定向命令回执不携带 |
| `tableId` | 双向 | 目标牌桌（等同房间 ID） |
| `handId` | 双向 | 当前手牌；非牌局消息可省略 |
| `tableRevision` | 双向 | 客户端动作所基于、服务端事件产生后的牌桌版本 |
| `serverTime` | 服务端→客户端 | Unix 毫秒时间 |
| `payload` | 双向 | 与 `type` 对应的对象 |

金额统一为 JSON 整数。标识符为最多 64 字节的字符串，不在标识符中承载密钥或个人信息。

## 3. 请求语义与恢复

### 3.1 幂等

以下客户端消息被视为幂等请求，**必须携带非空 `requestId`**，否则返回 `request_id_required`：

`table.leave`、`table.ready.set`、`table.action.submit`、`table.hole_cards.reveal`、`table.hole_cards.view.request`、`table.hole_cards.view.respond`、`table.seat.change.request`、`table.seat.swap.respond`、`table.runout.choose`、`table.time_extension.use`、`table.rebuy`、`table.voice.state.set`、`table.chat.send`、`table.player.interact`、`table.spectate.enter`、`table.seat.take`、`table.spectator.settings.set`

服务端按账号串行处理这些请求，并缓存最近 256 条 `(userID, requestID)` 结果。重复 `requestId` 直接返回第一次的回执，不再次执行。

牌局动作（`table.action.submit`）还必须携带唯一 `actionId`、当前 `handId` 和 `tableRevision`。

`table.join` 和 `table.snapshot.request` 不走幂等缓存。

### 3.2 序号与补发

- 同一牌桌的服务端事件按 `sequence` 排序，缓冲区保留最近 256 条。
- 客户端发现缺号时应停止提交动作，并发送 `table.snapshot.request` 携带最后连续收到的 `lastSequence`。
- **`table.snapshot` 是按接收者个性化生成的，其 payload 从不写入共享缓冲区**，只以同类型的空壳占位记录序号。
- 因此补发规则为：缺失区间必须完整保留**且不包含任何快照占位**，才能按原序号补发共享事件，并以 `table.replay.completed`（payload 含 `lastSequence` 和 `replayed`）结束；只要区间跨越了一个快照占位，补发即判定为不安全，服务端改为直接返回一份以当前最新序号标记的完整个人 `table.snapshot`。
- 客户端序号已经追平却仍显式请求快照（典型是动作被 `stale_revision` 拒绝后重新同步）时，服务端**同样返回完整快照**，而不是空的 `table.replay.completed`。此前那样做会让客户端带着过期的 `tableRevision` 反复被拒，表现为下注按钮怎么点都没反应。
- 客户端确认追平后才重新开放牌局动作。

实践含义：牌局中任何状态变化都会产生快照占位，所以断线稍久几乎必然走"整份快照"路径。共享事件补发主要在只有聊天、互动或语音变化的静默期生效。

## 4. 客户端消息

| 类型 | 用途 | 关键 payload |
|---|---|---|
| `session.authenticate` | 绑定登录会话 | `accessToken`, `deviceId` |
| `table.join` | 加入或恢复牌桌 | `lastSequence?`；外壳 `tableId` 省略时按账号当前房间解析 |
| `table.leave` | 请求离桌 | 空对象 |
| `table.ready.set` | 设置下一手准备状态 | `ready` |
| `table.rebuy` | 两手之间从账户钱包补充牌桌筹码 | `amount` |
| `table.hole_cards.reveal` | 符合条件时主动公开自己的底牌 | 空对象 |
| `table.hole_cards.view.request` | 已弃牌玩家申请私下查看另一名本手发过底牌的玩家（含已弃牌玩家）的底牌 | `targetUserId` |
| `table.hole_cards.view.respond` | 被申请玩家同意或拒绝私下看牌 | `pendingRequestId`, `accept` |
| `table.seat.change.request` | 两手之间向目标座位的玩家发起换位申请（目标座位为空时直接移动，当前客户端 UI 只提供换位申请） | `targetSeat` |
| `table.seat.swap.respond` | 被申请玩家同意或拒绝换位 | `pendingRequestId`, `accept` |
| `table.runout.choose` | 进入发牌次数选择阶段后选择发一次或发两次 | `count`（1 或 2） |
| `table.snapshot.request` | 补发事件或请求完整快照 | `lastSequence?`, `reason?` |
| `table.action.submit` | 提交牌局动作 | `actionId`, `action`, `raiseTo?` |
| `table.time_extension.use` | 当前行动玩家主动使用一张 30 秒加时卡 | 空对象 |
| `table.chat.send` | 发送文字、快捷语或表情 | `clientMessageId`, `kind`, `content` |
| `table.player.interact` | 对同桌其他玩家发送赞赏或嘲讽 | `targetUserId`, `kind`（`praise` 或 `taunt`） |
| `table.spectate.enter` | 进入观战位。手间立即生效；牌局进行中且本人参与本手时只记录意向，本手结算后生效 | 无 |
| `table.seat.take` | 观战者上桌。手间立即入座；牌局进行中只记录意向。座位满时立即拒绝，不排队 | 无 |
| `table.spectator.settings.set` | 房主调整观战位设置，立即生效并随快照广播 | `feeBigBlinds`（0～100）, `voiceAllowed`, `chatAllowed`, `emoteAllowed` |
| `table.voice.state.set` | 同步当前用户语音加入和开麦状态 | `joined`, `microphoneEnabled` |
| `system.ping` | 业务心跳 | 可为空 |

动作示例：

```json
{
  "version": 1,
  "type": "table.action.submit",
  "requestId": "req_123",
  "tableId": "room_9527",
  "handId": "hand_88",
  "tableRevision": 52,
  "payload": {
    "actionId": "action_123",
    "action": "raise",
    "raiseTo": 400
  }
}
```

`action` 仅允许 `fold`、`check`、`call`、`bet`、`raise`、`all_in`。`raiseTo` 只用于 `bet` 和 `raise`，表示本轮累计投入目标，并且必须是房间小盲的整数倍；全下不受整倍数限制。

合法动作、金额范围和比例快捷额度**全部由服务端计算并放在快照的 `currentAction` 中下发**（见第 6 节）。客户端按钮只是操作意图，不能作为合法性依据。

聊天示例：

```json
{
  "version": 1,
  "type": "table.chat.send",
  "requestId": "req_456",
  "tableId": "room_9527",
  "payload": {
    "clientMessageId": "message_local_1",
    "kind": "text",
    "content": "漂亮的一手"
  }
}
```

`kind` 允许 `text`、`quick_text` 和 `emoji`。自由文本按 Unicode 字符计数，限制 1～200 字符；服务端还限制每人每 10 秒最多 5 条。服务端确认后客户端才显示为发送成功。

## 5. 服务端消息

服务端消息分两类，**理解这个划分是理解本协议的关键**：

- **定向回执**：只发给命令发起者，原样带回 `requestId`，不携带 `sequence`。它只表示"命令已被接受或拒绝"，不携带完整牌局状态。
- **广播事件**：发给牌桌全体，携带 `sequence`，写入共享缓冲区。

状态变化的通用模式是：**先给发起者一条定向回执，再向全桌广播一轮个性化 `table.snapshot`**。

### 5.1 连接与会话

| 类型 | 用途 |
|---|---|
| `session.authenticated` | 会话鉴权成功，payload 含 `user`、`deviceId` 和 `serverInstanceId`（服务端进程启动时生成的随机标识，见 6.8） |
| `system.pong` | 心跳响应 |
| `system.error` | 通用错误；同时用作多个牌桌命令的失败回执，payload 为 `{code, message?}` |

**同一账号的重复连接**：同一用户在同一牌桌上只有最新一条连接生效。新连接完成 `table.join` 后，服务端以关闭码 **4001**（reason `superseded by a newer connection`）主动关闭旧连接；旧连接的关闭**不会**把玩家标为断线或取消准备。手机切换网络时旧的死连接往往要等 TCP 超时才被察觉，若按它的关闭改动牌桌状态，玩家会明明在线却显示已断线、自动准备失效。收到 4001 的客户端不应自动重连去抢回连接。

**被移出房间**：房主或管理员把玩家移出房间时，服务端以关闭码 **4002** 关闭该玩家的连接，关闭原因为 `removed_by_owner` 或 `removed_by_administrator`。客户端据此直接说明发生了什么并回到大厅，不再自动重连——此前用通用的 policy violation，客户端只能靠随后重连时 `table.join` 被拒推断，表现为「牌桌操作失败 permission_denied」。

### 5.2 命令回执对照表

| 客户端命令 | 成功回执 | 成功回执 payload | 失败回执 |
|---|---|---|---|
| `table.join` | `table.joined` | `roomId`, `chatHistory`, `resumedFromSequence`, `voiceMembers` | `system.error` |
| `table.leave` | `table.leave` | `{"left": true}` | `system.error` |
| `table.ready.set` | `table.ready.set` | `{"ready": bool}` | `system.error` |
| `table.action.submit` | `table.action.accepted` | 动作执行结果 | `table.action.rejected` |
| `table.time_extension.use` | `table.time_extension.accepted` | `{"remaining": int}` | `table.time_extension.rejected` |
| `table.rebuy` | `table.rebuy.accepted` | `{"amount": int}` | `table.rebuy.rejected` |
| `table.hole_cards.reveal` | `table.hole_cards.revealed` | `{"revealed": true}` | `table.hole_cards.reveal.rejected` |
| `table.hole_cards.view.request` | `table.hole_cards.view.request` | `{"requested": true}` | `system.error` |
| `table.hole_cards.view.respond` | `table.hole_cards.view.respond` | `{"accepted": bool}` | `system.error` |
| `table.seat.change.request` | `table.seat.change.request` | `{"requested": true}` | `system.error` |
| `table.seat.swap.respond` | `table.seat.swap.respond` | `{"accepted": bool}` | `system.error` |
| `table.runout.choose` | `table.runout.choose` | `{"count": int}` | `system.error` |
| `table.voice.state.set` | `table.voice.state.set` | `{"joined": bool, "microphoneEnabled": bool}` | `system.error` |
| `table.chat.send` | `table.chat.accepted` | 已接受的消息对象 | `table.chat.rejected` |
| `table.player.interact` | `table.player.interact.accepted` | 与广播一致的互动载荷 | `system.error` |
| `table.spectate.enter` | `table.spectate.entered` | `{"pending": bool}`，`pending` 为真表示等本手结束后生效 | `system.error` |
| `table.seat.take` | `table.seat.taken` | `{"pending": bool}` | `system.error` |
| `table.spectator.settings.set` | `table.spectator.settings.set` | 回显设置 | `system.error` |
| `table.snapshot.request` | `table.replay.completed` 或 `table.snapshot` | 见 3.2 | `system.error` |

注意几处容易误读的地方：

- 多个命令的成功回执**复用了请求本身的 `type`**（如 `table.ready.set`、`table.runout.choose`、`table.seat.change.request`）。客户端靠 `requestId` 而非 `type` 区分请求与回执。
- `table.hole_cards.view.request` 的回执只表示"申请已送达"，不表示对方已同意。同意结果通过目标玩家的 `table.hole_cards.view.respond` 及随后的快照体现。
- `table.action.accepted` 只是"动作已进入规则引擎并成功执行"的确认，**不包含新的牌局状态**；新状态在紧随其后的快照里。客户端不得把本地点击当作成功，必须等回执或新快照。

### 5.3 广播事件

服务端只广播四种事件：

| 类型 | 用途 | 是否可补发 |
|---|---|---|
| `table.snapshot` | 对当前接收者裁剪后的完整牌桌状态。所有牌局状态变化都通过它下发 | 否（个性化，仅占位记录序号） |
| `table.chat.message` | 向牌桌成员广播最终聊天消息 | 是 |
| `table.player.interaction` | 广播赞赏或嘲讽动画、音效所需的数据 | 是 |
| `table.voice.state` | 广播当前牌桌语音成员及其开麦状态 | 是 |

最终聊天消息包含：`messageId`、`clientMessageId`、`userId`、`displayName`、`kind`、`content`、`sentAt`。

互动广播包含 `interactionId`（取自请求的 `requestId`）、`fromUserId`、`fromDisplayName`、`targetUserId`、`targetDisplayName`、`kind`、`sentAt`。只能对仍在同一牌桌的其他玩家发送，服务端按发送者限制为至少间隔 1.5 秒，违反时返回 `player_interaction_too_frequent`。

`table.voice.state` 的 `members` 包含 `userId`、`displayName`、`joined`、`microphoneEnabled`，按 `userId` 排序。说话状态由 RTC 音量回调在客户端实时计算，不经过游戏服务转发。连接断开或离桌时服务端自动移除对应语音成员状态。

语音加入/退出的**元数据**会持久化到审计表（事件 `voice.joined`、`voice.left`，带 `roomId`；`voice.left` 的 `reason` 为 `self`、`left_table` 或 `disconnected`），只在加入状态真正变化时记录，仅切换麦克风不记录，也不记录任何音频内容。管理员可通过 `GET /v1/admin/audit?limit=&userId=` 查询。

聊天历史**不是**独立消息：加入牌桌时随 `table.joined` 的 `chatHistory` 字段一次性返回最近 50 条。

### 5.4 房间关闭与强制离桌

服务端没有 `table.closed` 消息。

- 管理员将玩家请出房间时，服务端直接以 WebSocket 关闭帧断开该玩家连接，状态码 `StatusPolicyViolation`，原因字符串 `removed_by_administrator`。客户端应据此提示并返回大厅，而不是等待应用层消息。
- 最后一名成员离桌导致房间关闭时，房内已无连接，因此不需要也不会发送任何通知。

### 5.5 历史设计中未实现的消息类型

以下类型出现在本文的早期版本中，**服务端和客户端均无任何实现**，不要按它们编写新客户端：

`table.player.joined`、`table.player.left`、`table.ready.changed`、`table.closed`、`table.blind.posted`、`table.showdown`、`table.pots.updated`、`table.chat.history`

它们的职责已全部并入 `table.snapshot`（玩家进出、准备状态、盲注、摊牌、底池）或 `table.joined`（聊天历史）。

早期还声明过 `system.hello`、`table.hand.started`、`table.hole_cards.dealt`、`table.board.dealt`、`table.hand.settled` 五个常量，同样从未发送，已于 2026-09-02 从代码中删除。底牌不通过独立消息下发，而是作为快照的 `holeCards` 字段只发给本人；手牌开始、公共牌和结算分别体现为快照的 `handId`/`phase`、`board` 和 `settlement`。

## 6. 快照

`table.snapshot` 的 payload 即服务端 `tablemanager.Snapshot`，按接收者逐人生成。字段如下。

### 6.1 全体可见

| 字段 | 说明 |
|---|---|
| `roomId`, `roomCode` | 房间标识与六位房间码 |
| `ownerUserId` | 当前房主 |
| `roomRevision`, `tableRevision` | 房间成员版本与牌桌状态版本 |
| `phase` | 状态机阶段 |
| `handId` | 当前手牌，无牌局时省略 |
| `dealerSeat`, `smallBlindSeat`, `bigBlindSeat` | 按钮与盲注**座位号** |
| `board` | 公共牌，无牌时为空数组（**持久化时也必须写空数组而非 SQL `NULL`**） |
| `seats[]` | 见 6.2 |
| `currentAction` | 见 6.3，无人需要行动时省略 |
| `lastAction` | 最近一次已确认动作：`actionId`、`handId`、`userId`、`action`、`tableRevision`。客户端用它驱动一次性音效等反馈 |
| `totalPot` | 当前底池**总额** |
| `settlement` | 结算结果，见 6.4 |
| `maxBuyIn` | 房间单人最大带入 |
| `joinLocked` | 房主是否已关闭房间入口；为 false 时省略 |
| `spectators[]` | 观战位上的成员：`userId`、`displayName`、`connected`、`stack`、`canSeeHoleCards`（本手已付费或免费模式）、`pendingSeat`（已申请本手结束后上桌）。观战者**不出现在** `seats[]` 里 |
| `spectatorSettings` | 房主对观战位的设置：`feeBigBlinds`、`voiceAllowed`、`chatAllowed`、`emoteAllowed` |
| `spectatorFees` | 本手看牌费明细：`handId`、`feePerSpectator`、`payers[]`、`recipients[]`（各含 `userId`、`displayName`、`amount`）。只在该手（含结算展示期）下发 |
| `draining` | 为 `true` 表示服务端正在优雅停机：本手打完后不再开新局，`table.ready.set {"ready": true}` 返回 `server_draining`，自动准备倒计时也不会安排；重启完成后该字段消失（见 6.8） |

### 6.2 `seats[]`

`userId`、`displayName`、`seat`、`stack`、`ready`、`connected`、`participating`、`folded`、`allIn`、`streetBet`（本轮投入）、`totalBet`（本手累计投入）、`position`、`lastAction`、`lastCommitted`、`lastActionTo`、`timeExtensions`（剩余加时卡）、`pendingSpectate`（已申请本手结束后进入观战，为 false 时省略）、`holeCards`（**只对本手有效付费的观战者填充**，其他接收者一律省略）。

牌局进行中加入房间的新成员会以“待入座”条目出现在 `seats[]` 中：`participating` 为 `false`，没有位置、投入和底牌；本手结算后转为正式座位，从下一手开始参与。

### 6.3 `currentAction`

```json
{
  "userId": "user_4",
  "seat": 4,
  "deadline": 1787540020000,
  "options": {
    "toCall": 80,
    "canFold": true,
    "canCheck": false,
    "canCall": true,
    "canBet": false,
    "canRaise": true,
    "canAllIn": true,
    "minRaiseTo": 180,
    "maxRaiseTo": 1260
  },
  "suggestions": [
    { "label": "half_pot", "action": "raise", "raiseTo": 260 },
    { "label": "all_in", "action": "all_in", "raiseTo": 1260 }
  ]
}
```

- **合法动作是布尔标志，不是字符串数组。** `canCall` 在剩余筹码小于 `toCall` 时为 `false`，此时玩家只能弃牌或 `all_in`。
- **比例快捷额度由服务端计算**并放在 `suggestions` 中。`label` 取值：`quarter_pot`、`third_pot`、`half_pot`、`two_thirds_pot`、`pot`、`overbet_120`、`min_raise`、`all_in`。
- 面对下注时比例以"完成跟注后的底池"（`totalPot + toCall`）为基数，结果按最接近的小盲整数倍取整，再钳制到 `[minRaiseTo, maxRaiseTo]`。低于 `minRaiseTo` 的项会被服务端改写为 `label: "min_raise"`；取整或钳制后 `raiseTo` 重复的项会被去重，因此 `suggestions` 的长度是可变的。
- 客户端应将 `raiseTo - 该玩家 streetBet` 显示为"本次投入"，将 `raiseTo` 显示为"加注至"。
- `deadline` 是服务端权威截止时间。客户端必须用自己的周期 ticker 刷新倒计时显示，不能只在收到快照时重绘。

### 6.4 `settlement`

`handId`、`potAwards[]`、`refunds`、`stacksByPlayer`、`ledgerEntries`、`showdown`、`revealedHands[]`、`runoutBoards[][]`（仅发两次时存在）。

每个 `potAward` 含 `potIndex`、`runoutIndex`（发两次时标明所属牌面）、`amount`、`winnerPlayerIds`、`payouts`。

**主池与边池的拆分只在 `settlement.potAwards` 中出现。** 牌局进行中快照只提供 `totalPot` 一个总额，不提供分层底池明细；客户端如需展示边池，必须自行按 `seats[].totalBet` 推导或等待结算。

### 6.5 仅对特定接收者可见

| 字段 | 可见范围 |
|---|---|
| `holeCards` | 仅本人 |
| `canShowHoleCards` | 仅本人，表示当前是否可主动亮牌 |
| `autoReadyCancelled` | 仅本人 |
| `spectating` | 仅本人，表示接收者在观战位 |
| `seats[].holeCards` | 仅本手有效付费（或免费模式）的观战者；上桌玩家与欠费观战者永远收不到别人的手牌 |
| `privateReveals[]` | 仅获准的申请者，`category` 固定为 `private_view` |
| `holeCardViewRequests[]` | 仅被申请的目标玩家 |
| `seatSwapRequests[]` | 仅被申请的目标玩家 |
| `voluntaryReveals[]` | 全体（已主动公开的底牌） |
| `runoutChoice` | 全体，含 `eligiblePlayerIds`、`choices`、`deadline` |
| `autoReadyDeadline` | 全体 |

裁剪在服务端逐接收者完成，**不允许先构造含全部底牌的对象再依赖客户端隐藏**。快照不得包含牌堆顺序、其他玩家未公开底牌、服务端随机数状态、TRTC 密钥或其他用户的鉴权信息。

### 6.6 不在快照中的信息

- **小盲、大盲金额不在快照里。** 快照只有 `maxBuyIn` 和盲注**座位号**。客户端从 REST 房间接口（`FriendRoom`）获取 `smallBlind` 和 `bigBlind`。
- 牌局历史、钱包余额、筹码流水均通过 REST 获取，不走 WebSocket。

### 6.7 相关时序约定

房主离桌后，服务端按成员加入时间、再按座位号将 `ownerUserId` 转移给最早加入的剩余成员。主动亮牌只允许在本手结算后由已弃牌玩家、或因其他玩家全部弃牌而获胜的玩家使用，公开状态持续到下一手开始。每手结算后服务端设置 10 秒 `autoReadyDeadline`（发两次结算为 15 秒，客户端分两块牌面先后展示需要额外时间）；短暂断线不等同于主动取消，客户端发送 `table.ready.set {"ready": false}` 才取消本轮自动准备，再发送 `ready: true` 可恢复。

### 6.8 优雅停机与重启

服务端收到停止信号后进入**排空**：不再开新局、取消所有牌桌的自动准备倒计时、向每桌广播一次带 `draining: true` 的快照，然后等待所有牌桌到达手间空档（上限由服务端 `SHUTDOWN_DRAIN_TIMEOUT_SECONDS` 控制，默认 120 秒）再退出。进行中的手照常进行，行动超时仍由服务端推进。

**进行中的手无法跨重启恢复**：底牌与洗牌状态只存在于旧进程内存里，不会写入任何外部存储。排空超时或进程崩溃时，那一手直接作废，牌桌按 `room_members` 中上一手结算后的座位与筹码重建；本手投入从未落库，因此不需要退还。

客户端识别这种情况的依据是 `session.authenticated` 中的 `serverInstanceId`：重连后若与上次不同，而重连前最后一份快照带有 `handId` 且没有 `settlement`，即可确定那一手作废，应向玩家说明并提示筹码已恢复到上一手结算后的状态。仅凭「重连后 `handId` 变了」不能下这个结论——玩家掉线期间那一手可能已经正常结算并进入下一手。

## 7. 错误码

错误码通过 `system.error`、`table.action.rejected`、`table.chat.rejected`、`table.rebuy.rejected`、`table.time_extension.rejected`、`table.hole_cards.reveal.rejected` 的 payload `code` 字段返回。

### 7.1 传输与会话

| 错误码 | 含义 |
|---|---|
| `unsupported_protocol_version` | `version` 不是 1 或缺省 |
| `unsupported_message_type` | 未知的 `type` |
| `authentication_required` | 未鉴权、令牌失效或会话已撤销 |
| `request_id_required` | 幂等请求缺少 `requestId` |
| `invalid_request` | payload 结构不合法 |
| `service_unavailable` | 依赖服务未装配 |
| `table_not_joined` | 该连接尚未加入牌桌 |
| `server_draining` | 服务端正在优雅停机，本手结束后不再开新局；`table.ready.set {"ready": false}` 仍被接受 |
| `internal_error` | 未归类的服务端错误；服务端会同时记录结构化日志 |

### 7.2 房间与权限

| 错误码 | 含义 |
|---|---|
| `room_not_found` | 房间不存在或已关闭 |
| `permission_denied` | 不是该房间成员或无操作权限 |
| `room_full` | 房间已满 |
| `already_in_room` | 账号已在其他房间 |
| `invalid_room_password` | 房间密码错误 |
| `seat_occupied` | 目标座位已被占用 |
| `invalid_seat` | 座位号不合法 |
| `spectators_full` | 观战位已满（最多 10 人） |
| `spectator_cannot_ready` | 观战者不能准备（取消准备会被静默接受） |
| `spectator_cannot_move` | 观战者不能换座位 |
| `invalid_spectator_settings` | 观战位设置越界（看牌费需在 0～100 个大盲之间） |

### 7.3 牌局规则

| 错误码 | 含义 |
|---|---|
| `not_seated` | 玩家未入座 |
| `not_your_turn` | 当前不是该玩家行动 |
| `no_action_required` | 当前没有待处理的行动 |
| `stale_hand` | `handId` 已过期 |
| `stale_revision` | `tableRevision` 已过期，需要同步 |
| `illegal_action` | 当前局面不允许该动作 |
| `invalid_action` | 动作名称不合法 |
| `invalid_amount` | `raiseTo` 不合法（越界或非小盲整数倍） |
| `hand_in_progress` | 补码和换位仅允许在两手之间；未弃牌的参局玩家不能中途离桌（已弃牌或未参局玩家可以） |
| `not_enough_ready_players` | 已准备玩家不足以开局 |
| `no_time_extensions` | 本手的两张加时卡已用完 |
| `time_extension_expired` | 当前行动已超时，不能再主动加时 |
| `runout_choice_not_available` | 当前不在发牌次数选择阶段，或玩家无权选择 |

### 7.4 亮牌与换位

| 错误码 | 含义 |
|---|---|
| `hole_cards_not_revealable` | 当前玩家、手牌或阶段不允许主动公开底牌 |
| `hole_card_view_not_available` | 不满足弃牌后私下看牌申请条件 |
| `hole_card_view_request_not_found` | 私下看牌申请已处理或失效 |
| `invalid_seat_swap` | 换位目标不合法（目标是自己或请求缺少 `requestId`） |
| `seat_swap_request_not_found` | 换位申请已处理或失效 |

### 7.5 钱包与带入

| 错误码 | 含义 |
|---|---|
| `insufficient_wallet_chips` | 账户娱乐筹码不足 |
| `maximum_buy_in_exceeded` | 本次带入或补码会超过房间最大带入 |
| `rebuy_required` | 牌桌筹码为零，完成补码前不能准备下一手 |
| `invalid_chip_amount` | 筹码数量不是正整数或超限 |
| `invalid_buy_in` | 带入金额不合法 |
| `invalid_table_balance` | 牌桌余额状态不合法 |
| `table_chips_not_conserved` | 检测到筹码不守恒，操作已回滚 |

### 7.6 聊天与互动

| 错误码 | 含义 |
|---|---|
| `chat_muted` | 玩家被管理员禁言 |
| `content_rejected` | 聊天内容不符合规则 |
| `rate_limited` | 聊天频率过高（每人每 10 秒最多 5 条）。同一错误码也用于 REST 与 WebSocket 握手的分层限流：HTTP 状态 `429` 并带 `Retry-After`，覆盖登录/注册/刷新（按 IP）、密码错误（按用户名）、房间与钱包操作（按用户）、TRTC 凭证（按用户）以及单 IP 并发连接数，参数见[运行保障指南](OPERATIONS_GUIDE.md) |
| `invalid_message` | 消息结构或长度不合法 |
| `invalid_player_interaction` | 互动请求参数不合法（含对自己发送） |
| `player_not_at_table` | 互动目标不在同一牌桌 |
| `player_interaction_too_frequent` | 互动间隔小于 1.5 秒 |
| `spectator_chat_disabled` | 房主已关闭观战者的文字聊天 |
| `spectator_emote_disabled` | 房主已关闭观战者的赞赏与嘲讽 |
| `spectator_voice_disabled` | 房主已关闭观战者的麦克风。TRTC 的推流在客户端，服务端只能拒绝状态广播，客户端须配合关麦 |

### 7.7 已废弃的文档错误码

本文早期版本列出的 `table_not_found` 和 `message_too_large` **服务端从不产生**：

- 房间不存在实际返回 `room_not_found`。
- 超过 16 KiB 的帧由 WebSocket 库直接关闭连接，没有应用层错误消息。

错误响应不得回显令牌、签名、服务端堆栈、数据库语句或其他玩家隐藏状态。

## 8. 顺序、广播和隐私

- 每张牌桌由一个服务端执行单元串行处理消息并生成序号；广播时持有发布锁，保证序号与写入顺序一致。
- 广播前按接收者生成可见数据，不能先生成包含全部底牌的对象再依赖客户端隐藏。
- 玩家个人屏蔽当前保存在客户端：客户端隐藏对应文字消息并同步调用 RTC 单人静音。管理员禁言由服务端持久化并在发送入口强制执行。
- 语音连接状态不进入牌局状态机；TRTC 故障不能阻止牌局动作、快照和聊天。
- 当前所有牌桌状态、事件缓冲区和语音成员状态都在**单个服务进程内存**中。协议本身不阻碍多实例，但在 Redis 路由和单写者租约落地前，不能通过启动第二个实例获得安全的横向扩展。

## 9. 兼容策略

- v1 内新增可选字段时，旧客户端必须忽略未知字段。
- 删除字段、改变字段含义或改变金额单位需要提升协议主版本。
- 客户端解析快照时应对所有字段提供缺省值，不要求服务端始终下发 `omitempty` 字段。
- 服务端在灰度期可同时支持相邻两个主版本；不支持的客户端收到明确升级提示后断开。

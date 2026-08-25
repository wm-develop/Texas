# WebSocket 协议 v1

状态：阶段 1 基线。牌局状态以服务端事件和快照为唯一事实来源。

## 1. 连接与编码

- 传输：生产环境使用 `wss`，本地开发可使用 `ws`。
- 编码：UTF-8 JSON 文本帧；单帧首版上限 16 KiB。
- 客户端连接后先完成会话鉴权，再加入牌桌。
- 心跳：客户端可发送 `system.ping`，服务端回复 `system.pong`；底层连接保活不替代业务心跳。
- `version` 固定为 `1`。收到不支持的版本时返回 `unsupported_protocol_version`。

## 2. 统一消息外壳

```json
{
  "version": 1,
  "type": "table.action.submit",
  "requestId": "req_01J...",
  "sequence": 128,
  "tableId": "table_01J...",
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
| `requestId` | 双向 | 客户端生成；响应原样带回，用于幂等和关联 |
| `sequence` | 服务端→客户端 | 单牌桌严格递增的事件序号 |
| `tableId` | 双向 | 目标牌桌 |
| `handId` | 双向 | 当前手牌；非牌局消息可省略 |
| `tableRevision` | 双向 | 客户端动作所基于、服务端事件产生后的牌桌版本 |
| `serverTime` | 服务端→客户端 | Unix 毫秒时间 |
| `payload` | 双向 | 与 `type` 对应的对象 |

金额统一为 JSON 整数。标识符为最多 64 字节的字符串，不在标识符中承载密钥或个人信息。

## 3. 请求语义

- 所有会改变状态的客户端请求必须携带唯一 `requestId`。
- 牌局动作还必须携带唯一 `actionId`、当前 `handId` 和 `tableRevision`。
- 服务端按账号保存近期幂等结果；重复请求返回第一次结果，不再次执行。
- 同一牌桌的服务端事件按 `sequence` 排序。客户端发现缺号时停止提交动作并请求快照。
- 重连时客户端提交最后确认的 `sequence`；服务端可补发事件，无法补发时返回完整快照。
- 共享事件最多保留近期 256 条。包含个人底牌的快照只在缓冲区记录序号标记，不保留共享 payload；补发区间经过快照标记时必须发送当前用户的完整私人快照。
- 补发完成后服务端发送 `table.replay.completed`，payload 包含 `lastSequence` 和 `replayed`；客户端确认追平后才重新开放牌局动作。

## 4. 客户端消息

| 类型 | 用途 | 关键 payload |
|---|---|---|
| `session.authenticate` | 绑定登录会话 | `accessToken`, `deviceId` |
| `table.join` | 加入或恢复牌桌 | `lastSequence?` |
| `table.leave` | 请求离桌 | `reason` |
| `table.ready.set` | 设置下一手准备状态 | `ready` |
| `table.snapshot.request` | 主动请求完整快照 | `lastSequence?`, `reason` |
| `table.action.submit` | 提交牌局动作 | `actionId`, `action`, `raiseTo?` |
| `table.chat.send` | 发送文字、快捷语或表情 | `clientMessageId`, `kind`, `content` |
| `table.voice.state.set` | 同步当前用户语音加入和开麦状态 | `joined`, `microphoneEnabled` |
| `table.chat.history.request` | 获取最近聊天 | `beforeMessageId?`, `limit` |
| `system.ping` | 业务心跳 | 可为空 |

动作示例：

```json
{
  "version": 1,
  "type": "table.action.submit",
  "requestId": "req_123",
  "tableId": "table_9527",
  "handId": "hand_88",
  "tableRevision": 52,
  "payload": {
    "actionId": "action_123",
    "action": "raise",
    "raiseTo": 400
  }
}
```

`action` 仅允许 `fold`、`check`、`call`、`bet`、`raise`、`all_in`。`raiseTo` 只用于 `bet` 和 `raise`，表示本轮累计投入目标。

聊天示例：

```json
{
  "version": 1,
  "type": "table.chat.send",
  "requestId": "req_456",
  "tableId": "table_9527",
  "payload": {
    "clientMessageId": "message_local_1",
    "kind": "text",
    "content": "漂亮的一手"
  }
}
```

`kind` 允许 `text`、`quick_text` 和 `emoji`。自由文本按 Unicode 字符计数，首版限制 1～200 字符；服务端过滤并确认后客户端才显示为发送成功。

## 5. 服务端消息

### 5.1 连接与错误

| 类型 | 用途 |
|---|---|
| `system.hello` | 公布服务器时间、协议版本和连接 ID |
| `session.authenticated` | 会话鉴权成功 |
| `system.pong` | 心跳响应 |
| `system.error` | 通用错误，不用于可恢复的牌局动作拒绝 |

### 5.2 牌桌生命周期

| 类型 | 用途 |
|---|---|
| `table.joined` | 加入牌桌成功 |
| `table.player.joined` | 玩家进入等待区或座位 |
| `table.player.left` | 玩家离开 |
| `table.ready.changed` | 准备状态变化 |
| `table.snapshot` | 对当前接收者裁剪后的完整状态 |
| `table.closed` | 房间关闭 |

### 5.3 手牌事件

| 类型 | 用途 |
|---|---|
| `table.hand.started` | 手牌、按钮、盲注和参局玩家确定 |
| `table.hole_cards.dealt` | 只发给底牌拥有者 |
| `table.blind.posted` | 盲注投入 |
| `table.action.required` | 指定玩家行动及合法动作、金额范围和截止时间 |
| `table.action.accepted` | 动作已执行及筹码变化 |
| `table.action.rejected` | 动作未执行，包含稳定错误码和最新版本 |
| `table.board.dealt` | 翻牌、转牌或河牌 |
| `table.showdown` | 可公开玩家的牌和最佳牌型 |
| `table.pots.updated` | 主池和边池变化 |
| `table.hand.settled` | 各底池赢家、分配和结算后筹码 |

`table.action.required` 示例：

```json
{
  "seat": 4,
  "userId": "user_4",
  "deadline": 1787540020000,
  "toCall": 80,
  "actions": ["fold", "call", "raise", "all_in"],
  "minRaiseTo": 180,
  "maxRaiseTo": 1260
}
```

### 5.4 聊天

| 类型 | 用途 |
|---|---|
| `table.chat.accepted` | 确认发送者的本地消息 ID |
| `table.chat.message` | 向允许接收的牌桌成员广播最终消息 |
| `table.chat.rejected` | 频率、权限或内容校验失败 |
| `table.chat.history` | 最近消息分页结果 |

最终聊天消息至少包含：`messageId`、`clientMessageId`、`userId`、`displayName`、`kind`、`content` 和 `sentAt`。

### 5.5 语音状态

| 类型 | 用途 |
|---|---|
| `table.voice.state.set` | 确认发送者的语音加入/开麦状态 |
| `table.voice.state` | 广播当前牌桌语音成员及其开麦状态 |

`table.voice.state` 的 `members` 包含 `userId`、`displayName`、`joined` 和 `microphoneEnabled`。说话状态仍由 RTC 音量回调在客户端实时计算，不经过游戏服务转发。游戏连接断开或离桌时，服务端自动移除对应语音成员状态；RTC 音频连接保持独立故障域。

## 6. 快照

客户端发现序号缺口时通过 `table.snapshot.request` 携带最后连续收到的 `lastSequence`。若缺失区间仍完整保留且全部为可共享事件，服务端按原序号补发并返回 `table.replay.completed`；否则直接返回以当前最新序号标记的 `table.snapshot`。

`table.snapshot` 至少包含：

- `tableId`、`sequence`、`tableRevision`、`phase`、`serverTime`。
- 房间规则、按钮座位、小盲和大盲座位。
- 座位、玩家公开资料、筹码、连接和准备状态。
- 当前 `handId`、公共牌、各玩家本轮投入和本手总投入。
- 主池、边池、当前行动座位、截止时间和合法动作。
- 仅对接收者可见的本人底牌。

快照不得包含牌堆顺序、其他玩家未公开底牌、服务端随机数状态、TRTC 密钥或其他用户的鉴权信息。

## 7. 稳定错误码

| 错误码 | 含义 |
|---|---|
| `authentication_required` | 未登录或会话失效 |
| `permission_denied` | 没有目标房间或操作权限 |
| `unsupported_protocol_version` | 协议版本不支持 |
| `invalid_message` | JSON 或字段不合法 |
| `message_too_large` | 超过帧或内容限制 |
| `rate_limited` | 请求频率过高 |
| `table_not_found` | 牌桌不存在或已关闭 |
| `not_seated` | 玩家未入座 |
| `not_your_turn` | 当前不是该玩家行动 |
| `stale_hand` | `handId` 已过期 |
| `stale_revision` | `tableRevision` 已过期，需要同步 |
| `illegal_action` | 当前局面不允许该动作 |
| `invalid_amount` | 下注目标不合法 |
| `chat_muted` | 玩家被禁言 |
| `content_rejected` | 聊天内容不符合规则 |

错误响应不得回显令牌、签名、服务端堆栈、数据库语句或其他玩家隐藏状态。

## 8. 顺序、广播和隐私

- 每张牌桌由一个服务端执行单元串行处理消息并生成序号。
- 广播前按接收者生成可见数据，不能先生成包含全部底牌的对象再依赖客户端隐藏。
- `table.hole_cards.dealt` 只发给所属玩家的有效连接。
- 屏蔽关系在服务端过滤文字消息，在客户端同步调用 RTC 单人静音。
- 语音连接状态不进入牌局状态机；TRTC 故障不能阻止牌局动作、快照和聊天。

## 9. 兼容策略

- v1 内新增可选字段时，旧客户端必须忽略未知字段。
- 删除字段、改变字段含义或改变金额单位需要提升协议主版本。
- 服务端在灰度期可同时支持相邻两个主版本；不支持的客户端收到明确升级提示后断开。

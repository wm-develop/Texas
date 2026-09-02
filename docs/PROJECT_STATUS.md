# 项目现状

> 状态基线：`main` / `v0.2.0`（2026-09-01）
>
> 本文描述当前代码已经做到什么、还没有做到什么。历史阶段记录保留在各阶段清单中；当历史规划与本文冲突时，以当前源码、自动化测试和本文为准。

## 1. 产品边界

“好友德州”是一套供熟人快速组织私人牌局的纯娱乐筹码德州扑克系统，不提供公开匹配、公开房间发现、真钱充值、提现、筹码交易或现实资产兑换，也不面向互联网公开传播。

- 客户端：Flutter OH，同一套 Dart 代码覆盖 Web、Windows、Android、HarmonyOS。
- 服务端：Go 模块化单体，权威裁决牌局、会话、房间和筹码变化。
- 持久化：生产环境使用 PostgreSQL；本地可使用进程内仓储。
- 语音：腾讯云 TRTC，音频不经过游戏服务转发。
- iOS：架构未主动排除，但尚未纳入正式构建、签名和验收范围。
- 发布：GitHub Release 只发布源码标签，不直接提供各平台安装包。

## 2. 已交付能力

### 账号、钱包与管理

- 用户名和密码注册/登录、轮换式 Refresh Token、注销和会话撤销。
- 登录用户名、牌桌昵称和密码的个人资料修改。
- 娱乐筹码钱包、无支付虚拟充值、不可变流水、入桌带入、手间补码和离桌返还。
- 首位管理员一次性初始化；注册开关、账号创建/停用/恢复/软删除、批量操作、密码重置、用户名和筹码调整。
- 管理员查看在线状态、当前房间并强制离桌；持久化文字禁言和管理审计。

### 房间与牌局

- 六位房间码、可选房间密码、动态 2～10 人牌桌，创建时不预设人数。
- 房主标识与按加入顺序自动移交；最后一名成员离开后房间关闭。
- 2～10 人无限注德州扑克：位置、按钮/盲注轮转、四条下注街、主池/边池、平分和余数分配、摊牌与结算。
- 小盲最低 10、大盲最低 20 且为小盲整数倍；普通下注/加注和自定义滑块按小盲整数倍，All in 可为非整倍数。
- 1/4、1/3、1/2、2/3、满池、1.2 倍超池、All in 和自定义额度；比例加注以完成跟注后的底池为基数。
- 普通行动 30 秒；仅剩两名未弃牌玩家时 60 秒；每手每人两张 30 秒加时卡。
- 每手结束后 10 秒自动准备（发两次结算为 15 秒，留足两块牌面的展示时间），允许主动取消；筹码为零时要求补码。
- 牌局进行中加入房间的新玩家以待入座状态观战，本手结算后自动入座；已弃牌或本手未参局的玩家可随时离桌，弃牌者的剩余筹码在本手结算后幂等返还钱包。
- 结算后主动亮牌、弃牌后的私下看牌申请（目标可以是仍参局或已弃牌的玩家）、点击其他玩家发起双方确认的换位申请。
- 满足“两名未弃牌玩家、至少一人全下、对手已跟注或全下、后续无需行动”时协商发一次/发两次；只有双方都选两次才运行两块公共牌。

### 实时、聊天、语音与交互

- WebSocket 鉴权、请求幂等、牌桌序号、缺口补发、私人快照和断线恢复。
- 文字、快捷语、表情、历史恢复、收起状态未读提醒、本地屏蔽和服务端禁言。
- TRTC 主动加入、自由麦、成员/开麦/说话状态、单人静音和播放音量。
- 下注、过牌、弃牌、All in 音效；点击头像发送赞赏/嘲讽动画和音效，带服务端冷却。

### 多端体验

- 全端横屏和应用级全屏；Windows 可缩放窗口，手机牌桌采用独立响应式布局。
- 本人手牌与摊牌直接显示在玩家框中，支持两块公共牌时仍可看到对手摊牌。
- 图层顺序为公共牌背景、玩家框、玩家本轮下注筹码、好友互动气泡；筹码和互动不会被玩家框遮挡。
- Android 使用 `WindowInsets.displayCutout`、HarmonyOS 使用 `getWindowAvoidArea(TYPE_CUTOUT)`，通过 MethodChannel 补充原生挖孔安全区。
- HarmonyOS 提供横屏数字输入面板，避免系统数字输入区域无法滚动或遮挡内容。
- HarmonyOS 模块声明支持手机、平板、PC/二合一等设备类型。

## 3. 当前架构

```text
Flutter 客户端
  ├─ HTTPS REST：账号、资料、钱包、房间、历史、管理、TRTC 凭证
  ├─ WSS：牌桌动作、快照、聊天、语音状态和好友互动
  └─ TRTC：独立音频媒体通道

Go 单实例游戏服务
  ├─ transport：HTTP/WebSocket、鉴权、CORS/Origin、安全头
  ├─ room/tablemanager：房间成员、权威牌桌和事件广播
  ├─ holdem：纯规则、牌型、底池、状态机
  ├─ account/bankroll/history/chat/ledger：业务仓储接口
  └─ postgres：生产仓储、事务与迁移

PostgreSQL：账户、会话、钱包、房间、手牌、行动、结算、聊天和审计
TRTC：牌桌语音
```

客户端当前使用 Flutter 自带的 `StatefulWidget`、`ChangeNotifier` 和显式 service/client 对象组织状态，没有引入 Riverpod 或 `go_router`。服务端为模块化单体；运行中的房间/牌桌和在线状态仍由单个进程持有。

实时协议是**快照驱动**的：牌局状态变化统一通过按接收者裁剪的 `table.snapshot` 下发，模式为"给发起者一条定向回执 + 向全桌广播一轮个性化快照"。除快照外只有聊天、玩家互动和语音状态三种共享广播事件。早期设计中的细粒度手牌事件（`table.hand.started`、`table.board.dealt`、`table.action.required` 等）从未实现，协议文档已于 2026-09-01 按源码校正。

## 4. 数据库版本

当前共有六组迁移：

1. `000001_phase3_core`：账户、会话、钱包、房间、手牌、聊天和账本基础表。
2. `000002_admin_console`：角色、账号状态、注册开关和管理审计。
3. `000003_admin_account_management`：管理员账号与钱包治理。
4. `000004_chat_moderation`：持久化文字禁言。
5. `000005_runout_boards`：发两次公共牌和分池结算。
6. `000006_dynamic_room_capacity`：创建房间不预设人数，容量动态扩展至 10 人。

生产环境必须先运行 `migrate up`，再启动对应版本游戏服务。服务启动会校验迁移版本和 SQL 校验和。

## 5. 已有验证证据

- 规则引擎固定种子模拟 100,000 手，筹码守恒且每手必然结束。
- 牌桌管理层成员生命周期属性测试：5 组固定种子、每组 400 次随机操作（入房、离桌、准备、行动、断线、重连、补码、换位、亮牌），每步之后断言全体成员快照均可生成、筹码守恒、无负余额。该测试已验证能复现并拦截“牌局进行中加入导致全桌快照失败”这类缺陷。
- 10 个独立 WebSocket 客户端连续完成 100 手，生成 1,000 条玩家账本记录并逐手守恒。
- Go 单元、集成和真实 PostgreSQL 集成测试已通过；Flutter 静态检查和单元/Widget 测试已通过。
- Web、Windows、Android Release 和 HarmonyOS 签名 HAP 均曾成功构建。
- Web/Android/HarmonyOS 三端牌局以及 Web、Windows、Android、HarmonyOS 间多组 TRTC 双向语音已实机验证。
- Android Release 曾因 R8/资源裁剪启动闪退，当前构建已关闭两者并验证可启动。
- HarmonyOS 曾出现白屏和数字输入问题，当前依赖/启动链及自定义数字面板已修复并实机确认。

以上是历史验证基线，不代表每次提交自动保持通过。每次发布仍应按目标平台重新执行测试、构建和真机冒烟。

2026-09-01 交接复核时，`go test ./... -count=1`、`go vet ./...`、`flutter analyze` 和 48 项 Flutter 测试均通过。第一次 Go 全量运行曾出现 `TestWebSocketReconnectRestoresCurrentHandAndPrivateCards` 的一次时序性失败；该用例随后连续运行 3 次通过，全量测试再次运行也通过。

同日交接接手方独立复跑 `go vet ./...` 与 `go test ./... -count=1` 全部通过，未复现上述抖动（`internal/transport` 包耗时约 34 秒）。该用例仍应视为观察对象：若再次出现，应检查等待条件与连接关闭的同步，而不是长期依赖重跑掩盖问题。

## 6. 明确未完成或有风险的部分

- Redis 尚未接入；不支持多游戏服务实例、权威牌桌租约、实例故障自动接管或无损热迁移。
- 运行中的牌桌状态主要在内存中。数据库保留业务记录，但服务进程异常退出后不能完整恢复进行中的一手牌。
- 备份、恢复演练、账本对账、健康巡检与告警均已提供可执行方案（`deploy/backup/`、`deploy/monitor/`，见[备份恢复指南](BACKUP_AND_RESTORE_GUIDE.md)与[运行保障指南](OPERATIONS_GUIDE.md)）；生产服务器已完成备份安装与首次演练，对账定时任务与告警通道**需在服务器上安装并验证通知能送达后才算生效**。24 小时故障注入尚未做。
- 分层限流已实现：登录/注册/刷新按 IP、密码错误按用户名、房间与钱包操作按用户、TRTC 凭证按用户、单 IP WebSocket 并发上限；`TRUSTED_PROXIES` 提供可信代理解析。`/metrics` 以 Bearer 令牌保护，暴露 HTTP、WebSocket、牌桌动作、活跃牌桌、快照广播失败与限流计数。**生产环境需配置 `TRUSTED_PROXIES` 与 `METRICS_TOKEN` 后才生效**。
- 举报、账号注销、隐私提示和语音加入/退出元数据尚未完成。
- 已有 GitHub Actions（`.github/workflows/ci.yml`）：服务端 gofmt/vet/test 与 -race、客户端 analyze/test、shellcheck 与仓库卫生检查。构建、迁移、发布和回滚仍是文档化的人工流程。
- Android Release 签名已支持通过 `android/key.properties` 配置独立发布密钥（缺失时回退调试签名并告警），维护者需按[Android 发布签名配置指南](ANDROID_SIGNING_GUIDE.md)完成一次性配置；HarmonyOS 签名依赖维护者本机配置；iOS 未验证。
- 客户端牌桌页已按职责拆分：`table_prototype_page.dart` 从 3180 行降至约 970 行，组件分入 `table_labels`、`table_card_widgets`、`table_status_widgets`、`table_board_center`、`table_seat_widgets`、`table_chat_panel`、`table_action_bar`、`table_rebuy_dialog`、`table_canvas`。提取出的组件为公开类，可被 Widget 测试直接覆盖。
- Flutter 测试为 18 个文件、71 项，其中 `table_layout_regression_test.dart` 覆盖 2～10 人座位排布、紧凑横屏布局、玩家框内手牌与中文牌型、图层顺序（下注筹码须绘制于玩家框之后）以及公共牌区域几何。该测试已验证能捕获图层顺序写反的回归。真机多端冒烟仍不可省略。
- 客户端版本已统一为 `0.2.0`：`pubspec.yaml` 为 `0.2.0+2`，Android/Windows 自动取自 pubspec，HarmonyOS 需在 `ohos/AppScope/app.json5` 手动同步（当前 `versionCode` 为 `2000`）。
- `internal/protocol/messages.go` 中 `system.hello`、`table.hand.started`、`table.hole_cards.dealt`、`table.board.dealt`、`table.hand.settled` 五个常量没有任何发送点，属于早期事件驱动设计的残留死代码，待清理。`revisionFromError` 比较的 `stale_table_revision` 也与规则引擎实际产生的 `stale_revision` 不匹配，该分支恒不命中（两个分支都返回 0，当前无功能影响）。

## 7. 推荐下一步

截至 2026-09-01，备份/演练/对账、成员生命周期属性测试、CI、牌桌页拆分与布局回归、分层限流、`/metrics` 与告警脚本均已落地。剩余顺序：

1. 在生产服务器上完成运行保障的安装与验证：`TRUSTED_PROXIES`、`METRICS_TOKEN`、告警 webhook、对账与巡检定时任务，并实际触发一次告警确认送达。
2. 举报、账号注销与隐私提示。账本追加不可变且外键 `ON DELETE RESTRICT`，注销只能是软删除加脱敏，需先明确产品规则。
3. 清理 `internal/protocol/messages.go` 的死常量与 `revisionFromError` 的失效分支；继续从 `_TablePrototypePageState` 抽出连接/命令控制器。
4. 24 小时稳定性与弱网验收、发布冒烟清单。
5. 再设计 Redis 路由、单写者租约、牌桌快照恢复和多实例故障模型；不要直接把进程内状态复制到 Redis。

详细接手入口见[项目交接文档](PROJECT_HANDOVER.md)，完整开发演进见[开发历程](DEVELOPMENT_HISTORY.md)。

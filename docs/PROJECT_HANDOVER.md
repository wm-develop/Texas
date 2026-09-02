# 项目交接文档

> 交接基线：`main` 分支，最近源码标签 `v0.1.1`，2026-09-01。
>
> 目标接手工具：Claude Code。本文同样适用于任何后续维护者。

## 1. 接手时先读什么

建议严格按以下顺序建立上下文：

1. [项目现状](PROJECT_STATUS.md)：当前已实现、未实现和风险，是状态总入口。
2. [根 README](../README.md)：项目定位、目录、最短启动路径和文档索引。
3. [德州扑克规则规格](TEXAS_HOLDEM_RULES_V1.md)：规则与金额语义的权威文档。
4. [WebSocket 协议](WEBSOCKET_PROTOCOL_V1.md)：实时消息、快照、隐私和错误码。
5. [验收指南](ACCEPTANCE_GUIDE.md)：发布前必须在真实环境完成的稳定性、弱网与冒烟验收。
6. [生产更新手册](PRODUCTION_UPDATE_GUIDE.md)和[自建部署指南](SELF_HOSTING_GUIDE.md)。
7. `CLAUDE.md`：在本仓库内开发时必须遵守的约束。

历史阶段清单已于 2026-09-02 合并进[开发历程](DEVELOPMENT_HISTORY.md)并从仓库移除，原文可在 Git 历史中查到。历史文档用于理解演进，不覆盖当前代码事实。

### 1.1 文档分层

| 层次 | 文档 | 使用方式 |
|---|---|---|
| 当前事实 | 项目现状、规则规格、WebSocket 协议、隐私说明 | 与代码冲突时先确认产品决策，再在同一提交里更新代码、测试和文档 |
| 自建与运维 | 自建部署、生产更新、备份与恢复、运行保障、验收指南、Android 签名、工具链 | 按需查阅；改动步骤时注意与本地副本成对维护 |
| 决策记录 | `docs/decisions/ADR-*.md` | 记录「为什么这么选」与「什么条件下重新评估」，不记录实现细节 |
| 历史 | 开发历程、产品与架构规划、`docs/releases/` | 只用于理解演进，**不是事实来源** |

`docs/local/` 被整目录忽略，存放含真实域名、IP、容器名的运维副本，只在维护者本机存在，不在仓库也不在 GitHub 上。公开文档一律使用 `example.com` 与占位值；两者成对维护的规则见该目录内的 `README.md`。仓库卫生检查会拦截误提交到 `docs/local/` 的文件。

## 2. 产品意图与不可越界事项

- 目标用户是快速组织牌局的熟人玩家；不做公开大厅、陌生人推荐或公开传播。
- 所有筹码都是娱乐虚拟筹码。不得增加真实支付、提现、交易或现实价值描述。
- 游戏服务是规则、时间、筹码和可见信息的唯一权威来源。客户端只能提交意图并显示确认后的状态。
- 底牌和私人看牌结果必须按接收者裁剪。不得先向所有客户端广播完整数据再依赖 UI 隐藏。
- TRTC、文字聊天和牌局是独立故障域；语音故障不能阻止牌局操作和结算。
- 客户端中文是主要用户界面语言，牌型不能直接显示 `one_pair`、`two_pair` 等内部枚举。
- GitHub Release 只发布源码；维护者根据自己的服务地址、TRTC 应用和签名构建客户端。

## 3. 仓库与版本状态

```text
apps/poker_client/       Flutter OH 客户端
services/game_server/    Go 权威游戏服务、迁移和 Dockerfile
deploy/                  本地 PostgreSQL/Redis 开发编排
docs/                    产品、规则、协议、部署、阶段和发布文档
.env.example             无敏感值的配置模板
CLAUDE.md                Claude Code 仓库工作约定
```

- 默认分支：`main`。
- 已有标签：`v0.1.0`、`v0.1.1`。
- 客户端 `pubspec.yaml` 仍为 `0.1.0+1`，下次发版前需明确同步版本号。
- 生产部署为单游戏服务实例 + PostgreSQL + HTTPS/WSS 反向代理 + Web 静态站点。
- Redis 只出现在开发编排中，运行时尚未接入。

接手第一步应执行：

```powershell
git status --short --branch
git log --oneline --decorate -20
git tag --list --sort=version:refname
```

工作区可能包含维护者本地的 `.env`、Flutter/Go 工具链、签名和生产更新私有副本。这些文件被忽略，不要删除、覆盖或提交。

## 4. 客户端结构与关键入口

### 应用与会话

- `apps/poker_client/lib/main.dart`：应用入口。
- `lib/app/poker_app.dart`：页面切换、会话恢复、登录/大厅/牌桌顶层编排。
- `lib/core/network/game_api_client.dart`：REST、Access Token 刷新和错误映射。
- `lib/core/network/game_socket_client.dart`：WebSocket 生命周期、鉴权、序号和消息分发。
- `lib/core/auth/auth_session.dart`：当前会话模型。

客户端没有使用 Riverpod 或 `go_router`。状态主要由 `StatefulWidget`、`ChangeNotifier`、回调和显式 service 对象组织；不要根据旧规划擅自假设已有全局状态框架。

牌桌状态是**快照驱动**的：客户端不消费细粒度手牌事件，而是把每一份 `table.snapshot` 整体替换为当前牌桌状态。它只额外处理 `table.joined`、各类 `*.accepted`/`*.rejected` 回执、`table.chat.message`、`table.player.interaction`、`table.voice.state` 和 `table.replay.completed`。新增牌桌信息时，默认做法是往快照里加字段，而不是发明新事件类型。

### 主要功能页

- `features/auth/presentation/auth_page.dart`：注册/登录、格式提示、首管理员隐藏入口。
- `features/lobby/presentation/lobby_page.dart`：大厅、房间码、建房、带入和入口工具栏。
- `features/table/presentation/table_prototype_page.dart`：牌桌主页面、聊天、语音、动作和大部分弹窗。
- `features/table/presentation/table_layout.dart`：响应式牌桌几何和座位位置。
- `features/table/presentation/table_player_seat.dart`：玩家框、本人/摊牌手牌和状态。
- `features/table/presentation/table_action_strip.dart`：底部动作与横向溢出处理。
- `features/table/domain/table_snapshot.dart`：按协议解析牌桌快照。
- `features/admin/presentation/admin_page.dart`：账号治理。
- `features/profile/presentation/profile_page.dart`：用户名、牌桌昵称和密码。

`table_prototype_page.dart` 已超过 100 KB，是当前最明显的客户端技术债。新增复杂牌桌功能前，优先将连接/命令控制器、聊天/语音侧栏、弹窗和布局拆分；拆分时必须保留现有消息幂等和 mounted/lifecycle 检查。

### 平台适配

- `lib/core/platform/native_cutout_insets.dart`：Flutter 侧挖孔 MethodChannel。
- Android `MainActivity.kt`：`WindowInsets.displayCutout`。
- HarmonyOS `EntryAbility.ets`：`getWindowAvoidArea(AvoidAreaType.TYPE_CUTOUT)`。
- `lib/core/platform/system_ui_controller.dart`：移动端横屏/全屏。
- `lib/core/widgets/platform_number_field.dart`：HarmonyOS 横屏数字面板。
- `lib/core/voice/*` 与 `web/trtc_bridge.js`：原生/Web TRTC 适配。

安全区原则：先消费 Flutter `MediaQuery`/`SafeArea` 已提供的 inset，只把原生 API 发现但 Flutter 未覆盖的差值补进去。不要恢复固定 32 像素之类的猜测边距。

### 牌桌 UI 约束

移动牌桌经历过多轮遮挡问题，以下是已确认的设计约束：

- 公共牌区域必须完整可读，但不再无边界地覆盖玩家框。
- 玩家框仅在自身黑色边界内位于公共牌之上，不能扩展一块透明大区域遮挡桌面。
- 每位玩家的本轮下注筹码位于其玩家框之上，不能遮住位置标识。
- 赞赏/嘲讽气泡是最顶层，不能被玩家框或公共牌遮挡。
- 本人手牌和结算摊牌均显示在对应玩家框内；“发两次”也不另建中央摊牌浮层。
- 手机侧边信息优先放左侧；右侧要为可常驻展开的聊天区域保留空间。
- 底部动作栏在窄窗口必须可横向滚动或自适应，不能让“自定义”消失。

## 5. 服务端结构与关键入口

- `cmd/server`：启动 HTTP/WebSocket 服务。
- `cmd/migrate`：数据库迁移命令。
- `internal/config`：环境变量、来源和令牌有效期校验。
- `internal/transport`：REST、WebSocket、鉴权、心跳、CORS/Origin 和安全头。
- `internal/account`：账号、会话、角色和注册开关。
- `internal/bankroll` / `internal/ledger`：钱包、带入/补码/返还和账本。
- `internal/room`：房间生命周期、成员、房主移交、协议命令和持久化编排。
- `internal/game/holdem`：纯规则引擎、牌型、底池、动作合法性和结算。
- `internal/game/tablemanager`：运行中牌桌的单写者管理。
- `internal/chat`：消息、历史和禁言。
- `internal/history`：最近牌局。
- `internal/postgres`：生产仓储和事务实现。
- `internal/protocol/messages.go`：WebSocket 消息类型常量。
- `migrations`：嵌入式版本化 SQL。

### 关键不变量

- 一张牌桌在一个进程内只有一个权威执行者，所有动作顺序处理。
- `raiseTo` 是本轮累计投入目标，不是本次额外投入。
- 普通下注/加注为小盲整数倍；All in 例外。
- 钱包扣减、牌桌加码和账本必须在同一事务；重复 request/action/hand 不能重复写账。
- 一手结算前后筹码守恒；只有 `virtual_top_up` 可以创造娱乐筹码。
- 空公共牌必须持久化为空数组而不是 SQL `NULL`。曾有翻前弃牌结算把 `board_cards=NULL` 写入非空列、导致动作回滚的事故。
- 快照和事件在广播前按用户裁剪；私人补发缺口无法安全重放时直接发送完整私人快照。
- 状态变化统一走"给发起者一条定向回执 + 向全桌广播一轮个性化快照"。`table.snapshot` 的 payload 从不写入共享事件缓冲区，只留占位序号，因此跨越快照占位的补发一律降级为整份快照。
- 合法动作、金额区间和比例快捷额度都由服务端算好放进快照的 `currentAction`；客户端不重算规则。
- 服务端截止时间是倒计时事实来源；客户端必须用自己的周期 ticker 刷新显示，不能依赖其他界面事件触发重绘。

## 6. 配置与秘密

公开模板为根目录 `.env.example`：

```dotenv
PORT=8080
STORAGE_BACKEND=memory
DATABASE_URL=
DATABASE_AUTO_MIGRATE=false
ALLOWED_ORIGINS=
AUTH_ACCESS_TOKEN_TTL_SECONDS=900
AUTH_REFRESH_TOKEN_TTL_SECONDS=2592000
TRTC_SDK_APP_ID=
TRTC_SECRET_KEY=
TRTC_USER_SIG_EXPIRE_SECONDS=3600
TRTC_DEBUG_TOKEN=
```

- `.env`、`secrets/`、证书、Android keystore 和 HarmonyOS `build-profile.json5` 均不得提交。
- `ALLOWED_ORIGINS` 是逗号分隔的完整 Web Origin，不含路径、通配符、中文标点或尾随斜杠。
- 正式客户端用 `--dart-define` 注入 HTTPS/WSS 地址；仓库文档只能使用 `example.com` 占位符。
- `docs/PRODUCTION_UPDATE_GUIDE_local.md` 是被忽略的本地私有副本，含真实生产地址。修改公开手册的步骤或结构时，同步修改本地副本，但保留其真实值且绝不提交。

## 7. 本地开发与验证

下列命令中的 `<仓库根>` 指本机检出目录，`<Flutter根>` 指本机 Flutter OH 安装目录。**不要照抄绝对路径**：仓库可以检出到任意位置，Flutter 也不必与仓库同盘。

### 服务端

仓库内自带一份被忽略的便携 Go 工具链，位于 `<仓库根>\.toolchains\go`。

```powershell
cd <仓库根>\services\game_server
& '..\..\.toolchains\go\bin\go.exe' test ./... -count=1
& '..\..\.toolchains\go\bin\go.exe' vet ./...
& '..\..\.toolchains\go\bin\go.exe' run .\cmd\server
```

若 Go 已加入 `PATH`，直接使用 `go` 即可。真实 PostgreSQL 集成测试需要仅指向可丢弃测试库的 `TEST_DATABASE_URL`，不能使用生产数据库。

### 客户端

```powershell
cd <仓库根>\apps\poker_client
$env:GIT_LFS_SKIP_SMUDGE = '1'
& '<Flutter根>\bin\flutter.bat' pub get
& '<Flutter根>\bin\flutter.bat' analyze
& '<Flutter根>\bin\flutter.bat' test
```

本地运行时传入：

```powershell
& '<Flutter根>\bin\flutter.bat' run -d <设备ID> `
  --dart-define=GAME_SERVER_URL=ws://<可访问地址>:8080/ws `
  --dart-define=GAME_HTTP_SERVER_URL=http://<可访问地址>:8080
```

真机不能使用开发机的 `127.0.0.1`，除非已经配置 ADB/HDC 端口反向转发。完整构建命令和产物路径见[生产更新手册](PRODUCTION_UPDATE_GUIDE.md)。

### 改动后的最小验证矩阵

| 改动 | 最低验证 |
| --- | --- |
| Go 规则/房间/协议 | `go test ./...`、`go vet ./...`，相关集成用例 |
| 数据库/仓储 | 迁移测试、真实 PostgreSQL 集成测试、重复迁移 `count: 0` |
| Dart 业务/UI | `flutter analyze`、`flutter test` |
| Android/HarmonyOS 原生代码 | 对应 Release 构建 + 真机启动/横屏/挖孔/输入 |
| Web 语音 | Web Release + 浏览器安全上下文下双端通话 |
| 牌桌布局/图层 | 窄 Windows、Android、HarmonyOS；2/6/10 人和发两次摊牌 |
| 协议/可见性 | 两个以上账号，断线重连、序号缺口和非本人底牌不可见 |

## 8. 数据库、部署与发布

- 新数据库迁移只追加新的编号文件，不修改已发布迁移；服务会校验旧迁移校验和。
- 生产更新顺序：记录旧镜像 → 备份 PostgreSQL → 拉取源码 → 构建镜像 → `migrate up` → 替换容器 → 检查 `/readyz` 和日志 → 发布客户端。
- 只改客户端不需要迁移数据库；是否更新服务端由协议/REST 是否变化决定。
- Web 是静态站点；API 域名反向代理到 `127.0.0.1:8080` 并启用 WebSocket。
- Android 当前 Release 使用调试签名且关闭 R8/资源裁剪；正式分发前必须建立独立签名和回归。
- HarmonyOS HAP 依赖本地 DevEco 签名，签名材料不在仓库。
- Windows 必须分发整个 `Release` 目录，不能只复制 EXE。

逐条命令以[生产更新手册](PRODUCTION_UPDATE_GUIDE.md)和[自建部署指南](SELF_HOSTING_GUIDE.md)为准。

## 9. 后续工作建议

### P0：先降低现有版本风险

1. 将 `table_prototype_page.dart` 拆为页面控制器、牌桌画布、聊天/语音、动作栏和弹窗组件。
2. 为 2/6/10 人、两种手机长宽比、窄 Windows、两块公共牌、摊牌和最高层互动增加布局/Widget 回归测试。
3. 统一 `pubspec.yaml` 版本号、Git tag 和 Release 文档；建立每次发布的四端冒烟记录。
4. 增加客户端崩溃/服务端错误的最小诊断信息，保持不记录底牌、令牌和密钥。

### P1：完成单实例生产保障

截至 2026-09-01 已落地：PostgreSQL 定时备份、异机保存与恢复演练；账本对账任务、`/metrics` 与告警；分层限流与可信代理；GitHub Actions 与仓库卫生检查；账号注销与隐私说明（规则见 [PRIVACY_NOTICE.md](PRIVACY_NOTICE.md)）。举报因熟人局定位不做。

管理审计查询界面与语音加入/退出元数据也已完成（2026-09-02）。仍待完成：

1. 24 小时故障注入与弱网验收。
2. 发布冒烟清单。

### P2：多实例（已评估，暂缓）

ADR 已完成：[ADR-002](decisions/ADR-002-MULTI-INSTANCE.md)。结论是**暂不实施**——多实例解决不了「部署要停牌局」这个真实痛点，引入 Redis 反而把一个单点变成两个单点，而进行中的那一手在任何方案下都无法跨实例恢复。ADR 中已写明 Redis 只放租约与 fencing epoch、崩溃接管等于作废当前手、按房间码路由需要的协议改动、以及上线前必须证明的四项验收（含双写拦截与隐藏信息断言）。当前架构仍然不能通过简单启动第二个容器获得安全的横向扩展。

应当先做的是 ADR 里列出的替代方案：优雅停机等待手间空档、重启后告知客户端本手作废、以及先把容量测出来。

## 10. 易踩坑清单

- 不要相信旧文档中的 Riverpod、`go_router`、邀请码和“多次发牌未支持”等描述；这些已在当前文档中校正。
- 协议是快照驱动的。不要等待 `table.action.required`、`table.hand.started`、`table.board.dealt`、`table.hand.settled`、`table.showdown`、`table.pots.updated`、`table.closed` 或 `table.chat.history`——这些消息服务端从不发送，其中一部分只是 `internal/protocol/messages.go` 里的死常量。
- 合法动作在快照里是 `canFold`/`canCheck`/`canCall`/... 布尔标志，不是 `actions` 字符串数组。
- 快照里没有小盲和大盲金额，只有盲注座位号和 `maxBuyIn`；盲注金额来自 REST 房间接口。
- 牌局进行中没有边池明细，只有 `totalPot`；分层底池只在 `settlement.potAwards` 出现。
- 房间不存在的错误码是 `room_not_found`，不是 `table_not_found`；被管理员请出房间没有应用层消息，表现为 WebSocket 以 `removed_by_administrator` 关闭。
- 不要把客户端点击成功当作牌局成功；必须等待 `table.action.accepted` 或新快照。
- 动作失败先查服务端结构化日志。客户端倒计时继续并不能证明动作到达了规则引擎，持久化事务也可能让已计算动作整体回滚。
- Flutter 倒计时需要 ticker；只根据快照 deadline 计算但不主动 `setState` 会表现为“点别的控件才刷新”。
- 不要重新打开 Android R8/资源裁剪，除非已完成 Release 真机回归。
- 不要把 HarmonyOS 原生系统数字键盘重新用于横屏金额输入，当前设备上曾出现数字不可达和输入闪退。
- 不要用固定安全边距代替 Android/HarmonyOS 原生挖孔 API。
- 不要在生产执行 `migrate down`；应用镜像回滚与数据库回滚是两件事。
- 不要使用 `postgres:latest`，固定 PostgreSQL 主版本。
- 不要在文档、提交、日志或截图中泄漏真实域名、数据库 URL、TRTC SecretKey 或签名路径。
- 2026-09-01 交接复核时，断线重连测试曾偶发一次“连接状态尚未变更”的时序失败，随后单测连续 3 次和全量重跑均通过；若再次出现，应检查等待条件/连接关闭同步，而不是长期依赖重跑。

## 11. 完成交接的检查清单

- [ ] 能在本机启动 Go 服务并通过 `/healthz`、`/readyz`。
- [ ] 能运行 Go 和 Flutter 自动化检查。
- [ ] 能在至少一个客户端注册/登录、创建两人房并完成一手。
- [ ] 理解 `raiseTo`、比例加注、短额 All in 和两人发两次条件。
- [ ] 理解钱包与牌桌筹码的事务边界及幂等键。
- [ ] 理解快照按接收者裁剪和断线恢复策略。
- [ ] 能说明为何当前只能单实例运行。
- [ ] 知道真实配置、平台签名和本地生产手册的位置，但不会将其提交。
- [ ] 能按生产更新手册判断一次改动需要数据库、服务端还是仅客户端更新。

完成这些项目后，再开始大规模重构或阶段 3 后续开发。

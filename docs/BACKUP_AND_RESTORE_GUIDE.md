# 数据库备份与恢复指南

> 目标：让生产 PostgreSQL 具备**可自动执行、可验证、可恢复**的备份，并通过定期演练确认恢复真的能用。
>
> 适用对象：运行 `texas-postgres` 容器的生产服务器维护者。

## 1. 为什么这是最高优先级

数据库里是账号、钱包余额、追加式账本、牌局历史和管理审计。除此之外的一切故障都可以恢复：服务崩了可以重启，镜像坏了可以回滚，客户端有问题可以重新构建。**只有数据没了是不可逆的**。

在配置完本文之前，生产环境只有更新版本时的一次性手动 `pg_dump`，没有定时执行、没有异机副本、没有保留策略，也从未验证过备份能否真的恢复出可用的库。

一份从未恢复过的备份不能算备份——它只是一个假设。

## 2. 组成

仓库 `deploy/backup/` 下有四个文件：

| 文件 | 作用 |
|---|---|
| `texas-backup.sh` | 每日备份：导出、校验归档、记录校验和、轮转保留、可选异机复制 |
| `texas-restore-drill.sh` | 恢复演练：把备份恢复到一次性容器，跑一致性校验，报告 RPO/RTO |
| `texas-verify.sql` | 数据一致性校验（筹码守恒、账本与钱包一致等），演练和日常对账共用 |
| `texas-backup.service` / `.timer` | systemd 定时任务单元 |

设计上的两条硬约束：

- **演练脚本绝不触碰生产容器和生产数据卷**，全程只读备份文件、只写自己创建的临时容器，结束即删除。
- 备份脚本任一步骤失败立即以非零码退出，不会留下半成品文件冒充成功的备份。

## 3. 安装

以下命令在生产服务器上执行。

### 3.1 部署脚本

```bash
sudo mkdir -p /opt/texas/bin
cd /opt/texas/texas          # 服务器上的仓库检出目录，按实际路径调整
sudo cp deploy/backup/texas-backup.sh /opt/texas/bin/
sudo cp deploy/backup/texas-restore-drill.sh /opt/texas/bin/
sudo cp deploy/backup/texas-verify.sql /opt/texas/bin/
sudo chmod +x /opt/texas/bin/texas-backup.sh /opt/texas/bin/texas-restore-drill.sh
```

### 3.2 配置文件

创建 `/opt/texas/backup.env`。不写任何一项也能跑（全部有默认值），按需覆盖：

```bash
sudo tee /opt/texas/backup.env >/dev/null <<'EOF'
# 数据库容器与库名，与部署时一致
TEXAS_PG_CONTAINER=texas-postgres
TEXAS_DB_NAME=texas
TEXAS_DB_USER=texas

# 备份根目录（宿主机路径，不在 Docker 卷内）
TEXAS_BACKUP_ROOT=/opt/texas/backups

# 保留策略：每日保留 14 份，每周日的备份额外长期保留 8 份
TEXAS_DAILY_KEEP=14
TEXAS_WEEKLY_KEEP=8

# 异机复制命令。留空则只在本机保留（强烈建议配置，见第 5 节）
# TEXAS_OFFSITE_CMD='/usr/local/bin/coscli cp {} cos://texas/'

# 异机副本加密的 age 公钥。留空则不加密
# TEXAS_AGE_RECIPIENT=age1xxxxxxxxxxxxxxxxxxxxx
EOF
sudo chmod 600 /opt/texas/backup.env
```

该文件不含数据库密码——备份通过 `docker exec` 在容器内以本地连接执行，不需要密码。

### 3.3 先手动跑一次

在挂上定时任务之前，务必先手动确认能跑通：

```bash
sudo /opt/texas/bin/texas-backup.sh /opt/texas/backup.env
```

预期输出以 `全部完成` 结尾，并在 `/opt/texas/backups/daily/` 下生成 `.dump` 与 `.sha256` 各一份。

### 3.4 启用定时任务

```bash
sudo cp deploy/backup/texas-backup.service /etc/systemd/system/
sudo cp deploy/backup/texas-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now texas-backup.timer
```

确认已排期：

```bash
systemctl list-timers texas-backup.timer
```

若服务器不使用 systemd，可改用 cron：

```cron
10 4 * * * /opt/texas/bin/texas-backup.sh /opt/texas/backup.env >> /var/log/texas-backup.log 2>&1
```

## 4. 恢复演练

**这一步不能省。** 备份的价值完全取决于它能不能恢复。

```bash
sudo /opt/texas/bin/texas-restore-drill.sh
```

不带参数时演练最近一份每日备份；也可以指定文件：

```bash
sudo /opt/texas/bin/texas-restore-drill.sh /opt/texas/backups/weekly/texas_20260907_041000.dump
```

脚本会依次完成：校验和核对 → 启动一次性 `postgres:17-alpine` 容器 → `pg_restore` → 运行 `texas-verify.sql` → 打印结果与 RPO/RTO → 删除临时容器。

退出码为 0 且所有检查显示 `PASS` 才算演练成功。

### 输出怎么读

| 检查项 | 含义 |
|---|---|
| `migration_state` | 迁移记录存在且版本号连续无缺口 |
| `row_counts` | 各表行数（信息性，用于与生产库比对） |
| `ledger_conservation` | **筹码守恒**：账本净增量必须恰好等于虚拟充值总额 |
| `settlement_per_hand` | 每手结算的 `table_delta` 之和为 0，赢家所得等于输家所失 |
| `wallet_matches_ledger` | 每个钱包余额等于该用户最新一条账本记录的期末余额 |
| `non_negative_balances` | 无负余额 |
| `orphan_references` | 账本与钱包没有引用不存在的用户 |

`ledger_conservation` 和 `settlement_per_hand` 是这套系统最核心的业务不变量——只有 `virtual_top_up` 允许创造筹码，其余操作都只是在钱包与牌桌之间搬运。它们通过，说明恢复出的不只是"一个能启动的库"，而是**账目自洽的库**。

### RPO 与 RTO

- **RPO**（可接受的数据丢失量）：脚本报告备份距今多久。每日备份意味着最坏情况会丢失约 24 小时的牌局与账本数据。若不可接受，提高备份频率。
- **RTO**（恢复耗时）：脚本报告纯恢复耗时。真实故障还要加上发现问题、决策、切换服务的时间，实际 RTO 会明显更长。

### 演练频率与记录

建议**每月至少一次**，以及每次数据库版本升级后。每次记录：

```text
演练日期 / 备份文件名 / RPO / RTO / 校验结果 / 执行人 / 异常与处理
```

### 演练记录

#### 2026-09-01 首次演练（基线）

| 项 | 结果 |
|---|---|
| 备份文件 | `daily/texas_20260901_161209.dump` |
| RPO | 0 分钟（手动生成备份后立即演练，非日常值） |
| RTO | 3 秒（纯恢复耗时） |
| 校验 | 七项全部 `PASS` |
| 数据规模 | users=13, wallets=13, ledger=1024, hands=194, hand_players=898, chat=58, audit=17 |
| 执行人 | 维护者 |

关键校验明细：

- `migration_state`：applied=6, max_version=6，迁移完整且连续。
- `ledger_conservation`：`diff=0`，账本净增量与虚拟充值总额精确相等。
- `settlement_per_hand`：194 手牌全部收支平衡。
- `wallet_matches_ledger`：13 个钱包余额均与各自最新账本记录一致。
- `non_negative_balances` / `orphan_references`：均无异常。

结论：备份可恢复，且恢复出的数据账目自洽。本次未发现需要处理的问题。

遗留项：当时尚未配置有效的异机副本，备份仅存在于生产服务器本机（见第 5 节）。

> 注意：首次演练的 RPO 为 0 只是因为备份刚刚手动生成。启用每日定时备份后，
> 常态最坏 RPO 约为 24 小时。

## 5. 异机副本

**只存在于本机的备份不是备份。** 磁盘损坏、误删目录、服务器欠费被回收，都会同时带走生产库和它旁边的备份——这恰恰是备份本来要防的那类事故。

### 只有一台服务器怎么办

不需要第二台服务器。按推荐程度排序：

**① 对象存储（推荐）**。阿里云 OSS、腾讯云 COS、Backblaze B2、Cloudflare R2 都可以，`rclone` 全部支持。本项目数据库很小（万级账本记录量级下，单份备份通常只有几 MB），**每月成本通常不到一元**。云厂商侧自带多副本冗余，与你的服务器完全独立。若服务器与对象存储在同一云厂商，还可以走内网端点，不消耗公网流量。

具体到腾讯云 COS 的完整步骤见下一小节。

**② 定时拉回本地电脑**。在自己的电脑上做计划任务，从服务器 `scp`/`rsync` 拉取最新备份。缺点是依赖电脑开机，容易断档，适合作为 ① 的补充而非唯一手段。

**③ 云盘快照**（如 ECS 自动快照）。可作为补充，但快照是块设备级别的，对运行中的数据库可能是崩溃一致状态，不如 `pg_dump` 的逻辑备份干净。**不要用它替代本方案**，两者一起用最稳妥。

**④ 同机另一块磁盘/目录**。只能防误删，防不了磁盘损坏和服务器丢失，聊胜于无，不应作为长期方案。

> 在配置好 ① 之前，请把"数据库可能全部丢失"当作当前真实存在的风险，而不是理论风险。

### 完整示例：腾讯云 COS

以下是从零配置到验证的全过程。阿里云 OSS 步骤基本相同，只需在 `rclone config` 中把 provider 换成 `Alibaba`、endpoint 换成对应的 OSS 域名。

**1. 创建存储桶**

在 COS 控制台新建存储桶，记下完整桶名（形如 `texas-backups-1250000000`，末尾是 APPID）和所属地域（如 `ap-guangzhou`）。

- 访问权限必须选**私有读写**。
- 建议开启版本控制，可防止误覆盖。

**2. 创建专用子账号（不要用主账号密钥）**

需要的是**只能编程访问**的子用户，不需要控制台登录。注意不要走「快速新建用户」入口，那个流程是为控制台登录设计的，会要求设置密码与 MFA，且没有填写策略的地方。

*建用户*：访问管理 → 用户 → 用户列表 → **新建用户** → **自定义创建**

- 访问方式：**只勾「编程访问」，取消「控制台访问」**；
- 用户权限：此处可以全部跳过，权限在下一步单独授予；
- 创建完成后立即保存 **SecretId / SecretKey**，页面关闭后无法再次查看。

*授权*，两种方式任选其一：

**方式一（推荐，无需手写 JSON）**：对象存储 COS → 选中存储桶 → 权限管理 → 存储桶访问权限 → 添加用户策略，选择刚建的子用户，勾选「数据读取」和「数据写入」，生效范围为整个存储桶。COS 会自动生成范围限定在该桶上的策略。

**方式二（自定义策略）**：访问管理 → **策略** → 新建自定义策略 → **按策略语法创建** → 空白模板 → 粘贴下面的 JSON → 保存后回到用户列表，把该策略关联给子用户。策略不能在建用户页面直接粘贴，必须先在「策略」里创建。

JSON 中三处占位符必须替换：地域 `ap-guangzhou`、两处 APPID `1250000000`、完整桶名 `texas-backups-1250000000`。

```json
{
  "version": "2.0",
  "statement": [
    {
      "effect": "allow",
      "action": [
        "cos:PutObject",
        "cos:GetObject",
        "cos:HeadObject",
        "cos:GetBucket"
      ],
      "resource": [
        "qcs::cos:ap-guangzhou:uid/1250000000:texas-backups-1250000000/*",
        "qcs::cos:ap-guangzhou:uid/1250000000:texas-backups-1250000000/"
      ]
    }
  ]
}
```

生成该子用户的 SecretId / SecretKey。**这对密钥等同于备份数据的访问权限，不要提交到仓库、不要写进任何文档。**

**3. 在服务器上安装上传工具**

两个工具任选其一。国内服务器推荐 coscli：它由腾讯云自己的域名分发，不会像 rclone 官方安装脚本那样卡在境外下载上。

*方式一：coscli（腾讯云官方，国内下载快）*

```bash
sudo curl -fsSL -o /usr/local/bin/coscli \n    https://cosbrowser.cloud.tencent.com/software/coscli/coscli-linux-amd64
sudo chmod +x /usr/local/bin/coscli
coscli --version
```

创建 `/root/.cos.yaml`（把地域、桶名和密钥换成自己的）：

```yaml
cos:
  base:
    secretid: 你的SecretId
    secretkey: 你的SecretKey
  buckets:
    - name: texas-backups-1250000000
      alias: texas
      region: ap-guangzhou
```

```bash
sudo chmod 600 /root/.cos.yaml
coscli ls cos://texas          # 验证连通
```

*方式二：rclone*

`curl https://rclone.org/install.sh | sudo bash` 在国内服务器上常常卡住——脚本本身能下载，但它内部会去 `downloads.rclone.org` 取二进制包，该域名在国内可能极慢或不通。改为手动安装：

```bash
# 优先尝试系统源
sudo yum install -y rclone     # CentOS/RHEL，需已启用 EPEL
sudo apt install -y rclone     # Debian/Ubuntu

# 系统源没有时，从 GitHub Release 手动下载对应架构的 zip 解压
# 解压后：sudo install -m 755 rclone /usr/local/bin/rclone
```

装好后 `sudo rclone config`：`n` 新建 → 名称 `cos` → 类型 `s3` → provider 选 `TencentCOS` → 粘贴 SecretId 与 SecretKey → endpoint 填 `cos.ap-guangzhou.myqcloud.com`（换成自己的地域）→ 其余默认。

密钥由你亲手输入，保存在 `/root/.config/rclone/rclone.conf`，务必收紧权限：

```bash
sudo chmod 600 /root/.config/rclone/rclone.conf
sudo rclone lsd cos:           # 验证连通
```

**4. 写入配置并去掉注释**

编辑 `/opt/texas/backup.env`。`{}` 会被替换为本次备份文件的路径：

```bash
# coscli
TEXAS_OFFSITE_CMD='/usr/local/bin/coscli cp {} cos://texas/'

# 或 rclone
TEXAS_OFFSITE_CMD='/usr/bin/rclone copy --config /root/.config/rclone/rclone.conf {} cos:texas-backups-1250000000'
```

> **注意两点。**
>
> 1. **参数顺序**：rclone 与 rsync 都是「源在前、目标在后」，必须用 `{}` 明确指出
>    文件位置。若省略 `{}`，脚本会把路径追加到命令末尾，对这两个工具而言等于把
>    源和目标写反。
> 2. **用绝对路径写命令**：`sudo` 的 `secure_path` 与 systemd 的默认 PATH 通常都
>    不包含 `/usr/local/bin`，交互式能跑通的命令在定时任务里会报
>    `command not found`。用 `command -v coscli` 查到真实路径后填绝对路径。

> 备份脚本用 `source` 读取该文件，被注释掉的行不会生效，脚本会打印
> `未配置 TEXAS_OFFSITE_CMD，跳过异机复制（备份仅存在于本机）`。看到这句就说明这一行没生效。

**5. 跑一次验证**

```bash
sudo /opt/texas/bin/texas-backup.sh
```

输出应包含 `异机复制中` 与 `异机复制完成`。随后确认远端确实收到：

```bash
coscli ls cos://texas                        # coscli
sudo rclone ls cos:texas-backups-1250000000  # rclone
```

### 远端保留策略（容易遗漏）

脚本的轮转只清理**本机**目录。`rclone copy` 只上传不删除，因此远端会**无限累积**每一份备份。请在 COS 控制台为该桶添加生命周期规则，例如「对象创建 60 天后删除」，让远端保留独立于本机管理。

这样本机保留 14 天、远端保留 60 天，两级保留互不影响，反而比同步删除更安全——本机被误删不会连带清空远端。

### 命令格式

`TEXAS_OFFSITE_CMD` 支持两种写法：

- **含 `{}`**：`{}` 被替换为本次备份文件的路径。适用于 rclone、rsync、coscli 等「源在前、目标在后」的工具。
- **不含 `{}`**：文件路径被追加到命令末尾。适用于只接收一个文件参数的包装脚本。

```bash
# coscli
TEXAS_OFFSITE_CMD='/usr/local/bin/coscli cp {} cos://texas/'

# rclone
TEXAS_OFFSITE_CMD='/usr/bin/rclone copy --config /root/.config/rclone/rclone.conf {} cos:texas-backups-1250000000'

# rsync 到另一台机器
TEXAS_OFFSITE_CMD='rsync -a --chmod=600 -e "ssh -i /opt/texas/secrets/backup_key" {} backup@example.com:/backups/texas/'

# 自定义包装脚本（脚本内用 $1 取文件路径）
TEXAS_OFFSITE_CMD=/opt/texas/bin/offsite-upload.sh
```

### 加密

备份含全部账号与牌局数据。离开本机前建议加密。配置 `TEXAS_AGE_RECIPIENT` 后，异机副本会先用 [age](https://github.com/FiloSottile/age) 加密再上传：

```bash
# 在一台安全的机器上生成密钥对（不要在生产服务器上生成）
age-keygen -o texas-backup-key.txt
# 输出中的 public key 填入 TEXAS_AGE_RECIPIENT，私钥离线妥善保管
```

私钥丢失等于备份全部作废，请与 Android 签名密钥库同等对待：至少两处独立备份，口令进密码管理器。

## 6. 真实故障时的恢复

以下是生产库损坏时的操作顺序。**先停写入，再恢复**，否则会边恢复边被新写入污染。

```bash
# 1. 停止游戏服务，切断所有写入
docker stop texas-game-server

# 2. 确认要恢复的备份，并先在演练容器里验证它可用
/opt/texas/bin/texas-restore-drill.sh /opt/texas/backups/daily/texas_YYYYMMDD_HHMMSS.dump

# 3. 保留现场：即使库已损坏，也先导出一份当前状态，便于事后取证
docker exec texas-postgres pg_dump -U texas -d texas -Fc \
    > /opt/texas/backups/pre-restore_$(date +%Y%m%d_%H%M%S).dump || true

# 4. 恢复到生产库（--clean 会先删除同名对象）
docker exec -i texas-postgres \
    pg_restore -U texas -d texas --clean --if-exists --no-owner \
    < /opt/texas/backups/daily/texas_YYYYMMDD_HHMMSS.dump

# 5. 对生产库运行一致性校验
docker exec -i texas-postgres psql -U texas -d texas -X -q -f - \
    < /opt/texas/bin/texas-verify.sql

# 6. 校验全部 PASS 后再启动游戏服务
docker start texas-game-server
curl -i http://127.0.0.1:8080/readyz
```

第 2 步不要跳过：在演练容器里先确认这份备份真的能恢复，再动生产库。

### 恢复后必须告知玩家

从备份恢复意味着备份时间点之后的数据全部消失，包括那段时间内的牌局结果、筹码变动和充值。这不是可以悄悄处理的事——务必明确告知所有玩家受影响的时间范围。

## 7. 日常对账

`texas-verify.sql` 是只读的，可以直接对生产库运行，不必等到演练：

```bash
docker exec -i texas-postgres psql -U texas -d texas -X -q -f - \
    < /opt/texas/bin/texas-verify.sql
```

建议每周执行一次。若 `ledger_conservation` 或 `settlement_per_hand` 出现 `FAIL`，说明存在筹码不守恒——**立刻记录现场并排查代码，不要直接改数据把账"调平"**，那会掩盖真正的缺陷。

## 8. 检查清单

首次配置完成后逐项确认：

- [ ] 手动执行 `texas-backup.sh` 成功，生成 `.dump` 与 `.sha256`
- [ ] `systemctl list-timers` 中能看到 `texas-backup.timer` 的下次执行时间
- [ ] 执行 `texas-restore-drill.sh` 全部检查 `PASS`，并记录了 RPO/RTO
- [ ] 已配置 `TEXAS_OFFSITE_CMD`，异机位置确实收到了副本
- [ ] 如启用加密，已在另一台机器上验证私钥能解开副本
- [ ] 演练记录已归档，并约定了每月演练时间
- [ ] `/opt/texas/backup.env` 权限为 `600`

## 9. 与其他文档的关系

- 首次部署与容器创建见[自建部署指南](SELF_HOSTING_GUIDE.md)。
- 版本更新时的一次性备份步骤见[生产环境更新手册](PRODUCTION_UPDATE_GUIDE.md)；本文的定时备份不替代它，发布前仍应单独备份一次。
- 当前风险与待办见[项目现状](PROJECT_STATUS.md)。

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
# TEXAS_OFFSITE_CMD='rclone copy --config /opt/texas/rclone.conf'

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

## 5. 异机副本

**只存在于本机的备份不是备份。** 磁盘损坏、误删目录、服务器被回收都会同时带走生产库和它旁边的备份。

`TEXAS_OFFSITE_CMD` 会以备份文件路径为唯一参数被调用，可以是任何命令。示例：

```bash
# rclone 到对象存储
TEXAS_OFFSITE_CMD='rclone copy --config /opt/texas/rclone.conf'

# rsync 到另一台机器
TEXAS_OFFSITE_CMD='rsync -a --chmod=600 -e "ssh -i /opt/texas/secrets/backup_key" backup@example.com:/backups/texas/'
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

#!/usr/bin/env bash
#
# 好友德州备份恢复演练。
#
# 把一份备份恢复到全新的一次性容器中，运行数据一致性校验，并报告
# RPO（备份落后现在多久）与 RTO（本次恢复实际耗时）。
#
# 绝不触碰生产容器与生产数据卷：全程只读取备份文件，写入独立的临时容器。
#
# 用法：
#   texas-restore-drill.sh                 # 演练最近一份每日备份
#   texas-restore-drill.sh <备份文件路径>   # 演练指定备份
#
# 退出码 0 表示恢复成功且全部校验通过。

set -Eeuo pipefail

CONFIG_FILE="${TEXAS_BACKUP_CONFIG:-/opt/texas/backup.env}"
if [[ -f "$CONFIG_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$CONFIG_FILE"
fi

BACKUP_ROOT="${TEXAS_BACKUP_ROOT:-/opt/texas/backups}"
PG_IMAGE="${TEXAS_PG_IMAGE:-postgres:17-alpine}"
DRILL_CONTAINER="${TEXAS_DRILL_CONTAINER:-texas-restore-drill}"
DRILL_DB="texas_drill"
DRILL_PASSWORD="drill-$(head -c 12 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
VERIFY_SQL="${TEXAS_VERIFY_SQL:-$SCRIPT_DIR/texas-verify.sql}"
LOG_TAG="texas-drill"

log() { printf '%s [%s] %s\n' "$(date -Is)" "$LOG_TAG" "$*"; }
fail() { log "错误：$*" >&2; exit 1; }

cleanup() {
    if docker inspect "$DRILL_CONTAINER" >/dev/null 2>&1; then
        log "清理演练容器"
        docker rm -f "$DRILL_CONTAINER" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

command -v docker >/dev/null 2>&1 || fail "未找到 docker"
[[ -f "$VERIFY_SQL" ]] || fail "找不到校验脚本 $VERIFY_SQL"

# 选择备份文件
if [[ $# -ge 1 ]]; then
    BACKUP_FILE="$1"
else
    BACKUP_FILE="$(find "$BACKUP_ROOT/daily" -maxdepth 1 -name 'texas_*.dump' 2>/dev/null \
        | sort -r | head -n 1)"
    [[ -n "$BACKUP_FILE" ]] || fail "$BACKUP_ROOT/daily 下没有可用备份"
fi
[[ -f "$BACKUP_FILE" ]] || fail "备份文件不存在：$BACKUP_FILE"

log "演练备份：$BACKUP_FILE"

# RPO：备份文件距今多久。真实故障下这段时间的数据会丢失。
BACKUP_EPOCH="$(stat -c %Y "$BACKUP_FILE")"
RPO_SECONDS=$(( $(date +%s) - BACKUP_EPOCH ))
log "RPO：备份生成于 $(date -d "@$BACKUP_EPOCH" -Is)，距今 $((RPO_SECONDS / 3600)) 小时 $(((RPO_SECONDS % 3600) / 60)) 分钟"

# 校验和（若存在）
if [[ -f "$BACKUP_FILE.sha256" ]]; then
    log "校验 sha256"
    (cd "$(dirname "$BACKUP_FILE")" && sha256sum -c "$(basename "$BACKUP_FILE").sha256" >/dev/null) \
        || fail "校验和不匹配，备份文件已损坏"
else
    log "警告：没有找到 .sha256 校验文件，跳过完整性校验"
fi

DRILL_START="$(date +%s)"
cleanup

log "启动一次性数据库容器（$PG_IMAGE）"
docker run -d --name "$DRILL_CONTAINER" \
    -e POSTGRES_DB="$DRILL_DB" \
    -e POSTGRES_USER=drill \
    -e POSTGRES_PASSWORD="$DRILL_PASSWORD" \
    --health-cmd='pg_isready -U drill -d '"$DRILL_DB" \
    --health-interval=2s --health-timeout=3s --health-retries=30 \
    "$PG_IMAGE" >/dev/null || fail "无法启动演练容器"

log "等待容器就绪"
for _ in $(seq 1 60); do
    state="$(docker inspect -f '{{.State.Health.Status}}' "$DRILL_CONTAINER" 2>/dev/null || echo starting)"
    [[ "$state" == "healthy" ]] && break
    sleep 2
done
[[ "$(docker inspect -f '{{.State.Health.Status}}' "$DRILL_CONTAINER")" == "healthy" ]] \
    || fail "演练容器未在预期时间内就绪"

log "恢复数据"
# --no-owner/--no-privileges：演练库的角色名与生产不同，不需要照搬属主
if ! docker exec -i "$DRILL_CONTAINER" \
        pg_restore -U drill -d "$DRILL_DB" --no-owner --no-privileges \
        < "$BACKUP_FILE" 2> /tmp/texas-drill-restore.err; then
    log "pg_restore 报告了问题，输出如下："
    cat /tmp/texas-drill-restore.err >&2
    fail "恢复失败"
fi
if [[ -s /tmp/texas-drill-restore.err ]]; then
    log "pg_restore 警告（通常可忽略）："
    sed 's/^/    /' /tmp/texas-drill-restore.err
fi

DRILL_END="$(date +%s)"
RTO_SECONDS=$(( DRILL_END - DRILL_START ))

log "运行数据一致性校验"
RESULT="$(docker exec -i "$DRILL_CONTAINER" psql -U drill -d "$DRILL_DB" -X -q -f - < "$VERIFY_SQL")" \
    || fail "校验脚本执行失败"

echo
echo "$RESULT"
echo

if grep -q 'FAIL' <<<"$RESULT"; then
    log "校验未通过：恢复出的数据存在一致性问题"
    log "RTO：$RTO_SECONDS 秒（本次恢复耗时，不含定位与决策时间）"
    exit 1
fi

log "全部校验通过"
log "RPO：$((RPO_SECONDS / 60)) 分钟（此备份之后的数据在真实故障中会丢失）"
log "RTO：$RTO_SECONDS 秒（纯恢复耗时，真实故障还需加上发现、决策与切换时间）"
log "演练成功。请把上面的 RPO/RTO 与校验输出记入演练记录。"

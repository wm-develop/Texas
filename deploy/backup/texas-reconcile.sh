#!/usr/bin/env bash
#
# 好友德州账本对账。
#
# 直接对生产库运行 texas-verify.sql（只读），任何一项 FAIL 即以非零码退出，
# 供定时任务与告警捕获。它是备份恢复演练之外、发现筹码不守恒的第二条线：
# 演练只在恢复出的副本上验证，这里则盯着正在运行的库本身。
#
# 发现 FAIL 时不要直接改数据把账「调平」——那会掩盖真正的代码缺陷。
# 应先保留现场（立即做一次备份），再排查产生不一致的操作。
#
# 用法：texas-reconcile.sh [配置文件]

set -Eeuo pipefail

CONFIG_FILE="${1:-/opt/texas/backup.env}"
if [[ -f "$CONFIG_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$CONFIG_FILE"
fi

CONTAINER="${TEXAS_PG_CONTAINER:-texas-postgres}"
DB_NAME="${TEXAS_DB_NAME:-texas}"
DB_USER="${TEXAS_DB_USER:-texas}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
VERIFY_SQL="${TEXAS_VERIFY_SQL:-$SCRIPT_DIR/texas-verify.sql}"
LOG_TAG="texas-reconcile"

log() { printf '%s [%s] %s\n' "$(date -Is)" "$LOG_TAG" "$*"; }
fail() { log "错误：$*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || fail "未找到 docker"
[[ -f "$VERIFY_SQL" ]] || fail "找不到校验脚本 $VERIFY_SQL"
docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -qx true \
    || fail "数据库容器 $CONTAINER 未在运行"

log "对生产库 $DB_NAME 执行一致性校验"
RESULT="$(docker exec -i "$CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -X -q -f - < "$VERIFY_SQL")" \
    || fail "校验脚本执行失败"

echo "$RESULT"

if grep -q 'FAIL' <<<"$RESULT"; then
    log "对账未通过：生产库存在账本不一致，请先备份保留现场，再排查代码缺陷"
    exit 1
fi
log "对账通过"

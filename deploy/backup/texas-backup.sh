#!/usr/bin/env bash
#
# 好友德州 PostgreSQL 定时备份。
#
# 行为：pg_dump 自定义格式 -> 校验归档可读 -> 记录校验和 -> 轮转保留
#       -> 可选异机复制。任一步骤失败立即以非零码退出，便于被计划任务捕获。
#
# 用法：texas-backup.sh [配置文件]
# 默认配置文件：/opt/texas/backup.env（不存在时使用下面的内置默认值）

set -Eeuo pipefail

CONFIG_FILE="${1:-/opt/texas/backup.env}"
if [[ -f "$CONFIG_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$CONFIG_FILE"
fi

CONTAINER="${TEXAS_PG_CONTAINER:-texas-postgres}"
DB_NAME="${TEXAS_DB_NAME:-texas}"
DB_USER="${TEXAS_DB_USER:-texas}"
BACKUP_ROOT="${TEXAS_BACKUP_ROOT:-/opt/texas/backups}"
DAILY_KEEP="${TEXAS_DAILY_KEEP:-14}"
WEEKLY_KEEP="${TEXAS_WEEKLY_KEEP:-8}"
# 异机复制命令。收到一个参数：刚生成的备份文件路径。留空则跳过。
# 例：TEXAS_OFFSITE_CMD='rclone copy --config /opt/texas/rclone.conf'
OFFSITE_CMD="${TEXAS_OFFSITE_CMD:-}"
# 可选加密：填入 age 收件人公钥后，异机副本使用 age 加密。留空则不加密。
AGE_RECIPIENT="${TEXAS_AGE_RECIPIENT:-}"

DAILY_DIR="$BACKUP_ROOT/daily"
WEEKLY_DIR="$BACKUP_ROOT/weekly"
LOG_TAG="texas-backup"

log() { printf '%s [%s] %s\n' "$(date -Is)" "$LOG_TAG" "$*"; }
fail() { log "错误：$*" >&2; exit 1; }

trap 'fail "第 $LINENO 行执行失败"' ERR

command -v docker >/dev/null 2>&1 || fail "未找到 docker"
docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -qx true \
    || fail "数据库容器 $CONTAINER 未在运行"

mkdir -p "$DAILY_DIR" "$WEEKLY_DIR"
STAMP="$(date +%Y%m%d_%H%M%S)"
TARGET="$DAILY_DIR/texas_${STAMP}.dump"

log "开始备份 $DB_NAME -> $TARGET"
# -Fc 自定义格式，便于 pg_restore 选择性恢复；失败时不留下半成品文件
if ! docker exec "$CONTAINER" pg_dump -U "$DB_USER" -d "$DB_NAME" -Fc > "$TARGET"; then
    rm -f "$TARGET"
    fail "pg_dump 失败"
fi

SIZE="$(stat -c %s "$TARGET")"
[[ "$SIZE" -gt 0 ]] || { rm -f "$TARGET"; fail "备份文件大小为 0"; }

# 校验归档结构完整、且包含预期业务表，避免把损坏或空库备份当成成功。
# 校验不通过时**保留**文件并改名为 .unverified：导出本身可能是好的，只是
# 校验环节出了问题，直接删掉反而可能丢掉唯一一份可用备份。
quarantine() {
    mv "$TARGET" "$TARGET.unverified" 2>/dev/null || true
    fail "$1（文件已保留为 $TARGET.unverified，人工确认后再决定是否删除）"
}

log "校验归档结构"
if ! LISTING="$(docker exec -i "$CONTAINER" pg_restore --list < "$TARGET" 2>&1)"; then
    log "pg_restore --list 输出：$LISTING"
    quarantine "归档校验失败，可能已损坏"
fi
for table in users account_wallets bankroll_entries hands; do
    grep -q "TABLE DATA public $table" <<<"$LISTING" || quarantine "归档缺少关键表 $table"
done

sha256sum "$TARGET" > "$TARGET.sha256"
log "备份完成，大小 $(numfmt --to=iec "$SIZE" 2>/dev/null || echo "$SIZE B")"

# 周日的备份额外保留一份长期副本
if [[ "$(date +%u)" == "7" ]]; then
    cp -p "$TARGET" "$WEEKLY_DIR/"
    cp -p "$TARGET.sha256" "$WEEKLY_DIR/"
    log "已归入每周保留目录"
fi

# 轮转：按文件名倒序保留最近 N 份，多余的连同校验和一并删除
prune() {
    local dir="$1" keep="$2" removed=0
    mapfile -t files < <(find "$dir" -maxdepth 1 -name 'texas_*.dump' -printf '%f\n' | sort -r)
    for ((index = keep; index < ${#files[@]}; index++)); do
        rm -f "$dir/${files[index]}" "$dir/${files[index]}.sha256"
        removed=$((removed + 1))
    done
    log "$(basename "$dir") 保留 $keep 份，清理 $removed 份"
}
prune "$DAILY_DIR" "$DAILY_KEEP"
prune "$WEEKLY_DIR" "$WEEKLY_KEEP"

# 异机复制。备份只存在于本机等于没有备份：磁盘损坏或误删会同时带走它们。
if [[ -n "$OFFSITE_CMD" ]]; then
    UPLOAD="$TARGET"
    TEMP_ENCRYPTED=""
    if [[ -n "$AGE_RECIPIENT" ]]; then
        command -v age >/dev/null 2>&1 || fail "配置了 TEXAS_AGE_RECIPIENT 但未安装 age"
        TEMP_ENCRYPTED="$(mktemp "${TMPDIR:-/tmp}/texas_backup_XXXXXX.age")"
        age -r "$AGE_RECIPIENT" -o "$TEMP_ENCRYPTED" "$TARGET" || fail "age 加密失败"
        UPLOAD="$TEMP_ENCRYPTED"
        log "已加密待上传副本"
    fi
    log "异机复制中"
    # 命令中含 {} 时替换为备份文件路径，否则把路径追加到末尾。
    # 前者用于 rclone / rsync 这类“源在前、目标在后”的工具，
    # 后者用于只接收一个文件参数的包装脚本。
    if [[ "$OFFSITE_CMD" == *"{}"* ]]; then
        eval "${OFFSITE_CMD//\{\}/$(printf '%q' "$UPLOAD")}" || fail "异机复制失败"
    else
        # shellcheck disable=SC2086
        $OFFSITE_CMD "$UPLOAD" || fail "异机复制失败"
    fi
    [[ -n "$TEMP_ENCRYPTED" ]] && rm -f "$TEMP_ENCRYPTED"
    log "异机复制完成"
else
    log "未配置 TEXAS_OFFSITE_CMD，跳过异机复制（备份仅存在于本机）"
fi

log "全部完成"

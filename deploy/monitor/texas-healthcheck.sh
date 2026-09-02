#!/usr/bin/env bash
#
# 好友德州服务健康巡检。
#
# 定时访问 /readyz（游戏服务 + 数据库连通性），状态发生变化时告警：
#   健康 -> 故障：立即发「服务不可用」
#   故障 -> 恢复：发「服务已恢复」
# 只在状态翻转时通知，持续故障不会每隔几分钟刷屏；状态记录在本地文件。
#
# 另外检查最近一次备份是否过期：备份脚本悄悄停跑时，轮转会在两周内
# 删光本机副本、两个月内删光远端副本，这里在 36 小时内就把它揪出来。

set -Eeuo pipefail

CONFIG_FILE="${TEXAS_ALERT_CONFIG:-/opt/texas/alert.env}"
if [[ -f "$CONFIG_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$CONFIG_FILE"
fi

READY_URL="${TEXAS_READY_URL:-http://127.0.0.1:8080/readyz}"
STATE_FILE="${TEXAS_HEALTH_STATE:-/var/lib/texas/health.state}"
BACKUP_ROOT="${TEXAS_BACKUP_ROOT:-/opt/texas/backups}"
BACKUP_MAX_AGE_HOURS="${TEXAS_BACKUP_MAX_AGE_HOURS:-36}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ALERT="${TEXAS_ALERT_SCRIPT:-$SCRIPT_DIR/texas-alert.sh}"

mkdir -p "$(dirname "$STATE_FILE")"
previous="$(cat "$STATE_FILE" 2>/dev/null || echo unknown)"

# --- 1. 服务就绪 ----------------------------------------------------------------
if curl -fsS --max-time 5 "$READY_URL" >/dev/null 2>&1; then
    current="up"
else
    current="down"
fi

if [[ "$current" != "$previous" ]]; then
    printf '%s' "$current" > "$STATE_FILE"
    if [[ "$current" == "down" ]]; then
        "$ALERT" "游戏服务不可用" "$READY_URL 无响应或未就绪。检查：docker ps、docker logs texas-game-server、数据库容器状态。" || true
    elif [[ "$previous" != "unknown" ]]; then
        "$ALERT" "游戏服务已恢复" "$READY_URL 重新返回就绪。" || true
    fi
fi

# --- 2. 备份新鲜度 ----------------------------------------------------------------
latest="$(find "$BACKUP_ROOT/daily" -maxdepth 1 -name 'texas_*.dump' -printf '%T@\n' 2>/dev/null | sort -n | tail -1 || true)"
stale_state_file="$STATE_FILE.backup"
previous_backup="$(cat "$stale_state_file" 2>/dev/null || echo fresh)"
if [[ -z "$latest" ]]; then
    backup_state="missing"
else
    age_hours=$(( ( $(date +%s) - ${latest%.*} ) / 3600 ))
    if (( age_hours > BACKUP_MAX_AGE_HOURS )); then
        backup_state="stale"
    else
        backup_state="fresh"
    fi
fi
if [[ "$backup_state" != "$previous_backup" ]]; then
    printf '%s' "$backup_state" > "$stale_state_file"
    case "$backup_state" in
        missing) "$ALERT" "找不到任何数据库备份" "$BACKUP_ROOT/daily 为空。确认 texas-backup.timer 已启用并成功运行过。" || true ;;
        stale)   "$ALERT" "数据库备份已过期" "最近一份备份距今 ${age_hours} 小时，超过 ${BACKUP_MAX_AGE_HOURS} 小时阈值。检查 systemctl status texas-backup.timer 与 journalctl -u texas-backup.service。" || true ;;
        fresh)   [[ "$previous_backup" != "fresh" ]] && { "$ALERT" "数据库备份已恢复正常" "最近一份备份距今 ${age_hours:-0} 小时。" || true; } ;;
    esac
fi

if [[ "$current" == "down" ]]; then
    exit 1
fi

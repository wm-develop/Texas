#!/usr/bin/env bash
#
# 好友德州告警发送。
#
# 把一条文本消息推送到 TEXAS_ALERT_WEBHOOK。默认按「通用 JSON」格式 POST：
#   {"text": "<消息>"}
# 这与企业微信群机器人、飞书自定义机器人（text 类型）、Server酱、
# 以及大多数自建接收端兼容；如需其他格式，用 TEXAS_ALERT_FORMAT 切换。
#
# 用法：
#   texas-alert.sh "<标题>" "<正文>"
#   systemd OnFailure 会以 texas-alert@<单元名>.service 形式调用，
#   此时只传单元名，脚本自行拼装最近日志。
#
# 未配置 webhook 时只写系统日志并成功退出，不阻塞调用方。

set -Eeuo pipefail

CONFIG_FILE="${TEXAS_ALERT_CONFIG:-/opt/texas/alert.env}"
if [[ -f "$CONFIG_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$CONFIG_FILE"
fi

WEBHOOK="${TEXAS_ALERT_WEBHOOK:-}"
FORMAT="${TEXAS_ALERT_FORMAT:-generic}"   # generic | wecom | feishu | bark
HOSTNAME_LABEL="${TEXAS_ALERT_HOST:-$(hostname)}"

log() { logger -t texas-alert "$*" 2>/dev/null || true; printf '%s\n' "$*" >&2; }

if [[ $# -eq 1 && "$1" == *.service ]]; then
    # 由 systemd OnFailure 调用：附带该单元最近的日志，便于不登录服务器也能定位
    UNIT="$1"
    TITLE="[$HOSTNAME_LABEL] $UNIT 执行失败"
    BODY="$(journalctl -u "$UNIT" -n 25 --no-pager -o cat 2>/dev/null | tail -c 1800 || true)"
    [[ -z "$BODY" ]] && BODY="（无法读取 journal 日志）"
else
    TITLE="[$HOSTNAME_LABEL] ${1:-告警}"
    BODY="${2:-}"
fi

MESSAGE="$TITLE
$(date -Is)
$BODY"

if [[ -z "$WEBHOOK" ]]; then
    log "未配置 TEXAS_ALERT_WEBHOOK，告警仅记录到系统日志：$TITLE"
    exit 0
fi

# 用 python 做 JSON 转义，避免手写引号处理出错；服务器通常已有 python3
json_escape() {
    python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' 2>/dev/null \
        || printf '"%s"' "$(sed 's/"/\\"/g' | tr '\n' ' ')"
}
ESCAPED="$(printf '%s' "$MESSAGE" | json_escape)"

case "$FORMAT" in
    wecom)   PAYLOAD="{\"msgtype\":\"text\",\"text\":{\"content\":$ESCAPED}}" ;;
    feishu)  PAYLOAD="{\"msg_type\":\"text\",\"content\":{\"text\":$ESCAPED}}" ;;
    bark)    PAYLOAD="{\"title\":$(printf '%s' "$TITLE" | json_escape),\"body\":$(printf '%s' "$BODY" | json_escape)}" ;;
    *)       PAYLOAD="{\"text\":$ESCAPED}" ;;
esac

if curl -fsS --max-time 15 -H 'Content-Type: application/json' -d "$PAYLOAD" "$WEBHOOK" >/dev/null; then
    log "告警已发送：$TITLE"
else
    log "告警发送失败（webhook 不可达）：$TITLE"
    exit 1
fi

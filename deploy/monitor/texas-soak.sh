#!/usr/bin/env bash
#
# 好友德州 24 小时稳定性观测。
#
# 周期性抓取 /metrics 与 /readyz，把每次采样追加成 CSV，结束时给出判定。
# 关注的不是瞬时值，而是趋势：
#   goroutine  连接回落后不回落 = 每连接协程没退出
#   堆内存      单调上涨不回落   = 对象被长期持有
#   启动时间    发生变化         = 容器静默重启，之前的趋势作废
#   广播失败    大于 0           = 有整桌收不到快照
#   就绪失败    大于 0           = 期间出现过不可用
#
# 用法：
#   ./texas-soak.sh                     # 按默认 24 小时 / 5 分钟采样
#   ./texas-soak.sh --hours 2           # 缩短观测时长（冒烟用）
#   ./texas-soak.sh --interval 60       # 采样间隔（秒）
#   ./texas-soak.sh --report out.csv    # 只对既有 CSV 重新判定，不采样
#
# 需要 METRICS_TOKEN，与游戏服务 .env 中一致。可写入 /opt/texas/soak.env：
#   TEXAS_METRICS_TOKEN=...
#
# 建议放在 tmux / screen 里跑，或 nohup 后台执行；中途 Ctrl-C 会先出判定再退出。

set -Eeuo pipefail

CONFIG_FILE="${TEXAS_SOAK_CONFIG:-/opt/texas/soak.env}"
if [[ -f "$CONFIG_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$CONFIG_FILE"
fi

METRICS_URL="${TEXAS_METRICS_URL:-http://127.0.0.1:8080/metrics}"
READY_URL="${TEXAS_READY_URL:-http://127.0.0.1:8080/readyz}"
TOKEN="${TEXAS_METRICS_TOKEN:-}"
OUTPUT_DIR="${TEXAS_SOAK_DIR:-/opt/texas/soak}"
HOURS="${TEXAS_SOAK_HOURS:-24}"
INTERVAL="${TEXAS_SOAK_INTERVAL:-300}"
# goroutine / 堆内存相对最低点的增长超过这两个阈值即判为疑似泄漏
GOROUTINE_GROWTH="${TEXAS_SOAK_GOROUTINE_GROWTH:-200}"
HEAP_GROWTH_MB="${TEXAS_SOAK_HEAP_GROWTH_MB:-256}"
REPORT_ONLY=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --hours)    HOURS="$2"; shift 2 ;;
        --interval) INTERVAL="$2"; shift 2 ;;
        --report)   REPORT_ONLY="$2"; shift 2 ;;
        --help|-h)  sed -n '2,26p' "$0"; exit 0 ;;
        *) echo "未知参数：$1（--help 查看用法）" >&2; exit 2 ;;
    esac
done

# --- 采样 -----------------------------------------------------------------------

# metric_value <metrics 文本> <指标名> —— 取无标签指标的值，缺失时返回空
metric_value() {
    awk -v name="$2" '$1 == name { print $2; found = 1; exit }
                      END { if (!found) print "" }' <<< "$1"
}

# counter_total <metrics 文本> <指标名前缀> —— 带标签计数器求和
counter_total() {
    awk -v prefix="$2" '
        index($1, prefix) == 1 { total += $NF }
        END { printf "%d", total }
    ' <<< "$1"
}

sample_once() {
    local csv="$1" now metrics ready http_status
    now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    if curl -fsS --max-time 5 "$READY_URL" >/dev/null 2>&1; then
        ready=1
    else
        ready=0
    fi

    http_status="$(curl -sS -o /tmp/texas-soak-metrics.$$ -w '%{http_code}' \
        --max-time 10 -H "Authorization: Bearer $TOKEN" "$METRICS_URL" 2>/dev/null || echo 000)"
    if [[ "$http_status" != "200" ]]; then
        rm -f "/tmp/texas-soak-metrics.$$"
        printf '%s,%s,,,,,,,\n' "$now" "$ready" >> "$csv"
        printf '%s  抓取失败 HTTP %s\n' "$now" "$http_status" >&2
        return
    fi
    metrics="$(cat "/tmp/texas-soak-metrics.$$")"
    rm -f "/tmp/texas-soak-metrics.$$"

    printf '%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
        "$now" "$ready" \
        "$(metric_value "$metrics" texas_process_start_time_seconds)" \
        "$(metric_value "$metrics" texas_goroutines)" \
        "$(metric_value "$metrics" texas_memory_heap_bytes)" \
        "$(metric_value "$metrics" texas_websocket_connections_active)" \
        "$(metric_value "$metrics" texas_tables_active)" \
        "$(counter_total "$metrics" texas_snapshot_broadcast_failures_total)" \
        "$(counter_total "$metrics" texas_rate_limited_total)" \
        >> "$csv"
}

# --- 判定 -----------------------------------------------------------------------

report() {
    local csv="$1"
    awk -F, '
        NR == 1 { next }                        # 跳过表头
        {
            samples++
            if ($2 == 0) readyFailures++
            if ($4 == "") { scrapeFailures++; next }
            if (starts == "") starts = $3
            else if ($3 != starts) restarted = 1
            if (goMin == "" || $4 + 0 < goMin) goMin = $4 + 0
            if ($4 + 0 > goMax) goMax = $4 + 0
            goLast = $4 + 0
            if (heapMin == "" || $5 + 0 < heapMin) heapMin = $5 + 0
            heapLast = $5 + 0
            if ($6 + 0 > connMax) connMax = $6 + 0
            connLast = $6 + 0
            broadcast = $8 + 0
            limited = $9 + 0
            valid++
        }
        END {
            if (valid == 0) { print "没有有效采样，无法判定。"; exit 3 }
            goGrowth = goLast - goMin
            heapGrowthMB = (heapLast - heapMin) / 1048576
            printf "采样 %d 次，其中有效 %d 次\n", samples, valid
            printf "  goroutine     最低 %d / 最高 %d / 最终 %d（相对最低点 +%d）\n", goMin, goMax, goLast, goGrowth
            printf "  堆内存        最低 %.1f MB / 最终 %.1f MB（+%.1f MB）\n", heapMin / 1048576, heapLast / 1048576, heapGrowthMB
            printf "  WebSocket     峰值 %d / 最终 %d\n", connMax, connLast
            printf "  广播失败      %d 次\n", broadcast
            printf "  限流触发      %d 次\n", limited
            printf "  就绪失败      %d 次；抓取失败 %d 次\n", readyFailures + 0, scrapeFailures + 0

            failed = 0
            if (restarted) { print "不通过：进程启动时间发生变化，观测期间服务重启过，先查重启原因再重跑。"; failed = 1 }
            if (readyFailures > 0) { print "不通过：观测期间出现过不可用。"; failed = 1 }
            if (broadcast > 0) { print "不通过：存在整桌快照广播失败，检查服务日志中的 broadcast 记录。"; failed = 1 }
            if (goGrowth > goThreshold) {
                printf "不通过：goroutine 相对最低点增长 %d，超过阈值 %d，疑似协程泄漏。\n", goGrowth, goThreshold
                failed = 1
            }
            if (heapGrowthMB > heapThreshold) {
                printf "不通过：堆内存相对最低点增长 %.1f MB，超过阈值 %d MB，疑似对象未释放。\n", heapGrowthMB, heapThreshold
                failed = 1
            }
            if (connLast > 0 && goGrowth > 0)
                print "提示：结束时仍有连接在线，goroutine 增长可能来自这些连接，清空连接后再看一次更准。"
            if (failed) exit 1
            print "通过：未发现泄漏、重启或广播失败。"
        }
    ' goThreshold="$GOROUTINE_GROWTH" heapThreshold="$HEAP_GROWTH_MB" "$csv"
}

if [[ -n "$REPORT_ONLY" ]]; then
    [[ -f "$REPORT_ONLY" ]] || { echo "找不到 CSV：$REPORT_ONLY" >&2; exit 2; }
    report "$REPORT_ONLY"
    exit $?
fi

if [[ -z "$TOKEN" ]]; then
    echo "未配置 TEXAS_METRICS_TOKEN，无法抓取 /metrics。写入 $CONFIG_FILE 后重试。" >&2
    exit 2
fi

# 指标改名会让缺失项被静默记成 0，整轮观测随之失去意义，先抓一次核对。
preflight="$(curl -fsS --max-time 10 -H "Authorization: Bearer $TOKEN" "$METRICS_URL" 2>/dev/null || true)"
if [[ -z "$preflight" ]]; then
    echo "无法抓取 $METRICS_URL：确认服务在运行、METRICS_TOKEN 正确。" >&2
    exit 2
fi
EXPECTED_METRICS="texas_process_start_time_seconds texas_goroutines texas_memory_heap_bytes texas_websocket_connections_active texas_tables_active texas_snapshot_broadcast_failures_total texas_rate_limited_total"
missing=""
for name in $EXPECTED_METRICS; do
    grep -q "^# TYPE $name " <<< "$preflight" || missing="$missing $name"
done
if [[ -n "$missing" ]]; then
    echo "服务端缺少以下指标，脚本需同步更新：$missing" >&2
    exit 2
fi

mkdir -p "$OUTPUT_DIR"
CSV="$OUTPUT_DIR/soak-$(date -u +%Y%m%d-%H%M%S).csv"
echo "time,ready,start_time,goroutines,heap_bytes,ws_connections,tables,broadcast_failures,rate_limited" > "$CSV"

# Ctrl-C 或 systemd 停止时也要出判定，并且把判定结果作为退出码，
# 便于放进自动化流程；trap 中的 exit 决定脚本最终状态。
finish() {
    local status=0
    echo
    echo "观测结束，记录：$CSV"
    report "$CSV" || status=$?
    trap - EXIT
    exit "$status"
}
trap finish EXIT

deadline=$(( $(date +%s) + $(awk "BEGIN { printf \"%d\", $HOURS * 3600 }") ))
echo "开始观测：$HOURS 小时，每 $INTERVAL 秒一次，记录写入 $CSV"
while (( $(date +%s) < deadline )); do
    sample_once "$CSV"
    remaining=$(( deadline - $(date +%s) ))
    if (( remaining <= 0 )); then
        break
    fi
    sleep "$(( remaining < INTERVAL ? remaining : INTERVAL ))"
done

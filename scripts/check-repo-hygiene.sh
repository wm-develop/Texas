#!/usr/bin/env bash
#
# 仓库卫生检查：确保敏感文件没有被提交，且文档内部链接有效。
#
# 本地和 CI 都可运行：
#   ./scripts/check-repo-hygiene.sh
#
# 检查项针对本项目的具体约定（见 CLAUDE.md），比通用密钥扫描更有针对性。

set -Eeuo pipefail

cd "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

failures=0
report() {
    printf '  [FAIL] %s\n' "$1" >&2
    failures=$((failures + 1))
}
section() { printf '\n== %s ==\n' "$1"; }

# --- 1. 不得被跟踪的敏感文件 --------------------------------------------------
section "敏感文件"

# 每一项是「描述|git ls-files 匹配模式」。.env.example 是公开模板，需排除。
while IFS='|' read -r description pattern; do
    [[ -z "$description" ]] && continue
    matches="$(git ls-files -- "$pattern" | grep -v '^\.env\.example$' || true)"
    if [[ -n "$matches" ]]; then
        report "$description 已被提交：$(tr '\n' ' ' <<<"$matches")"
    fi
done <<'PATTERNS'
环境变量文件|.env
环境变量文件|**/.env
Android 密钥库|**/*.jks
Android 密钥库|**/*.keystore
Android 签名配置|**/key.properties
HarmonyOS 本机签名配置|apps/poker_client/ohos/build-profile.json5
本地生产手册（含真实地址）|docs/PRODUCTION_UPDATE_GUIDE_local.md
rclone 配置|**/rclone.conf
COS 配置|**/.cos.yaml
PATTERNS

# --- 2. 已跟踪文件中的疑似明文密钥 --------------------------------------------
section "疑似明文密钥"

# 只扫描已跟踪的文本文件，跳过本脚本自身与 .env.example 模板。
# 值部分限定为「看起来像真实密钥」的字符集：纯 ASCII 且不含 $ 与尖括号，
# 从而天然排除 shell/YAML 变量引用（$VAR、${VAR}）与 <占位符> 写法。
secret_value='[A-Za-z0-9_@#%^&*+./=-]'
secret_patterns=(
    "TRTC_SECRET_KEY[[:space:]]*=[[:space:]]*${secret_value}\{16,\}"
    "secretkey[[:space:]]*:[[:space:]]*${secret_value}\{16,\}"
    "POSTGRES_PASSWORD[[:space:]]*=[[:space:]]*['\"]\?${secret_value}\{12,\}"
    'BEGIN \(RSA \|EC \|OPENSSH \)\?PRIVATE KEY'
    'AKID[A-Za-z0-9]\{20,\}'
)
# 仍可能命中的示例占位词，逐个排除
placeholder='请替换\|your\|example\|xxx\|placeholder\|CHANGEME\|password-123'
for pattern in "${secret_patterns[@]}"; do
    hits="$(git grep -nI "$pattern" -- \
        ':!scripts/check-repo-hygiene.sh' \
        ':!.env.example' \
        | grep -iv "$placeholder" || true)"
    if [[ -n "$hits" ]]; then
        report "匹配到疑似密钥（模式 $pattern）："
        sed 's/^/         /' <<<"$hits" >&2
    fi
done

# --- 3. Markdown 内部链接 ------------------------------------------------------
section "文档链接"

while read -r file; do
    directory="$(dirname "$file")"
    # 只检查指向仓库内 .md 的相对链接，跳过 http(s) 与锚点。
    # 无匹配时 grep 返回 1，在 pipefail 下必须显式吞掉，否则整个脚本会提前退出。
    targets="$(grep -o '](\([^)]*\.md\)\(#[^)]*\)\?)' "$file" 2>/dev/null \
        | sed 's/^](//;s/)$//;s/#.*$//' || true)"
    [[ -z "$targets" ]] && continue
    # 用 here-string 而非管道，确保 report 的计数落在当前 shell 而不是子 shell
    while read -r target; do
        [[ -z "$target" || "$target" == http* ]] && continue
        if [[ ! -f "$directory/$target" ]]; then
            report "$file 指向不存在的文档：$target"
        fi
    done <<<"$targets"
done < <(git ls-files '*.md')

# --- 4. Shell 脚本可执行位 -----------------------------------------------------
section "脚本可执行位"

while read -r script; do
    mode="$(git ls-files -s "$script" | awk '{print $1}')"
    if [[ "$mode" != "100755" ]]; then
        report "$script 缺少可执行位（当前 $mode），服务器 pull 后无法直接运行"
    fi
done < <(git ls-files '*.sh')

# --- 汇总 ----------------------------------------------------------------------
printf '\n'
if [[ "$failures" -gt 0 ]]; then
    printf '仓库卫生检查未通过：%d 项失败\n' "$failures" >&2
    exit 1
fi
printf '仓库卫生检查通过\n'

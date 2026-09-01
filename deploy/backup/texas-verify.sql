-- 好友德州数据一致性校验。
--
-- 既用于备份恢复演练（验证恢复出的库确实可用），也可直接对生产库运行做账本对账。
-- 每条检查输出一行：check_name / status / detail。status 为 FAIL 即需要人工处理。
--
-- 只读，不修改任何数据。

\pset footer off
\pset tuples_only off

-- 1. 迁移已应用，且版本号连续无缺口（本项目使用自研迁移器，
--    schema_migrations 只有 version/name/checksum/applied_at，没有 dirty 列）
SELECT
    'migration_state' AS check_name,
    CASE
        WHEN count(*) = 0 THEN 'FAIL'
        WHEN count(*) <> max(version) THEN 'FAIL'
        ELSE 'PASS'
    END AS status,
    'applied=' || count(*) || ' max_version=' || COALESCE(max(version)::text, 'none') AS detail
FROM schema_migrations;

-- 2. 各业务表行数（信息性，用于与生产库比对）
SELECT
    'row_counts' AS check_name,
    'INFO' AS status,
    concat_ws(', ',
        'users=' || (SELECT count(*) FROM users),
        'wallets=' || (SELECT count(*) FROM account_wallets),
        'ledger=' || (SELECT count(*) FROM bankroll_entries),
        'hands=' || (SELECT count(*) FROM hands),
        'hand_players=' || (SELECT count(*) FROM hand_players),
        'chat=' || (SELECT count(*) FROM chat_messages),
        'audit=' || (SELECT count(*) FROM audit_events)
    ) AS detail;

-- 3. 筹码守恒：账本中只有 virtual_top_up 允许凭空创造筹码。
--    其余原因（买入、补码、离桌返还、牌局结算）都只在钱包与牌桌之间搬运，
--    因此全部条目的 (wallet_delta + table_delta) 之和必须恰好等于充值总额。
WITH totals AS (
    SELECT
        COALESCE(sum(wallet_delta + table_delta), 0) AS net_created,
        COALESCE(sum(wallet_delta) FILTER (WHERE reason = 'virtual_top_up'), 0) AS topped_up
    FROM bankroll_entries
)
SELECT
    'ledger_conservation' AS check_name,
    CASE WHEN net_created = topped_up THEN 'PASS' ELSE 'FAIL' END AS status,
    'net_created=' || net_created || ' topped_up=' || topped_up
        || ' diff=' || (net_created - topped_up) AS detail
FROM totals;

-- 4. 每手结算内部守恒：同一 hand_id 的结算条目 table_delta 之和必须为 0，
--    即赢家赢得的正好等于输家失去的。
WITH per_hand AS (
    SELECT reference_id, sum(table_delta) AS delta
    FROM bankroll_entries
    WHERE reason = 'hand_settlement'
    GROUP BY reference_id
    HAVING sum(table_delta) <> 0
)
SELECT
    'settlement_per_hand' AS check_name,
    CASE WHEN count(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS status,
    CASE
        WHEN count(*) = 0 THEN 'all hands balanced'
        ELSE count(*) || ' unbalanced hands, e.g. ' || min(reference_id)
    END AS detail
FROM per_hand;

-- 5. 钱包余额与账本一致：每个用户钱包的当前余额，必须等于该用户最新一条
--    账本记录的 wallet_balance_after。
WITH latest AS (
    SELECT DISTINCT ON (user_id) user_id, wallet_balance_after
    FROM bankroll_entries
    ORDER BY user_id, revision_after DESC, created_at DESC
), mismatched AS (
    SELECT w.user_id
    FROM account_wallets w
    JOIN latest l ON l.user_id = w.user_id
    WHERE w.wallet_chips <> l.wallet_balance_after
)
SELECT
    'wallet_matches_ledger' AS check_name,
    CASE WHEN count(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS status,
    CASE
        WHEN count(*) = 0 THEN 'all wallets match latest ledger entry'
        ELSE count(*) || ' mismatched users, e.g. ' || min(user_id)
    END AS detail
FROM mismatched;

-- 6. 余额非负（数据库约束已保证，此处防止约束被绕过或恢复不完整）
SELECT
    'non_negative_balances' AS check_name,
    CASE WHEN count(*) = 0 THEN 'PASS' ELSE 'FAIL' END AS status,
    count(*) || ' negative wallet rows' AS detail
FROM account_wallets
WHERE wallet_chips < 0;

-- 7. 外键完整性抽查：账本与钱包不应引用不存在的用户
SELECT
    'orphan_references' AS check_name,
    CASE WHEN orphan_ledger + orphan_wallet = 0 THEN 'PASS' ELSE 'FAIL' END AS status,
    'ledger=' || orphan_ledger || ' wallets=' || orphan_wallet AS detail
FROM (
    SELECT
        (SELECT count(*) FROM bankroll_entries e
            WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.user_id = e.user_id)) AS orphan_ledger,
        (SELECT count(*) FROM account_wallets w
            WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.user_id = w.user_id)) AS orphan_wallet
) counts;

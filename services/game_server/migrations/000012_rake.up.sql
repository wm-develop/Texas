-- 抽水：管理员按房间设置，每手从底池里抽出一部分筹码打入管理员钱包。
--
-- 每手抽水 = min(底池 × rake_basis_points / 10000 向下取整, rake_cap) + 翻后加抽。
-- rake_cap 为 0 表示比例部分不设上限；翻后加抽只在这手牌发出过翻牌时收取，
-- 不受 rake_cap 限制，且不得超过一个大盲。新房间一律不抽水。
ALTER TABLE rooms
    ADD COLUMN rake_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN rake_basis_points integer NOT NULL DEFAULT 0
        CHECK (rake_basis_points BETWEEN 0 AND 1000),
    ADD COLUMN rake_cap bigint NOT NULL DEFAULT 0 CHECK (rake_cap >= 0),
    ADD COLUMN rake_postflop_enabled boolean NOT NULL DEFAULT false,
    -- 「不超过一个大盲」由服务端校验，不写成跨列约束：房间盲注日后若能调低，
    -- 跨列约束会让那次保存直接失败；引擎遇到越界的规则会退回不抽水。
    ADD COLUMN rake_postflop_amount bigint NOT NULL DEFAULT 0 CHECK (rake_postflop_amount >= 0);

-- 每手的抽水记进牌谱：各人输赢之和加上它恒等于 0。
ALTER TABLE hands
    ADD COLUMN rake bigint NOT NULL DEFAULT 0 CHECK (rake >= 0);

-- 抽水入账是一条独立的流水：user_id 为管理员，room_id 为房间，reference_id 为手号。
-- 按房间汇总这类流水就是该房间的累计抽水。
ALTER TABLE bankroll_entries
    DROP CONSTRAINT bankroll_entries_reason_check;

ALTER TABLE bankroll_entries
    ADD CONSTRAINT bankroll_entries_reason_check CHECK (reason IN (
        'virtual_top_up',
        'buy_in',
        'rebuy',
        'hand_settlement',
        'cash_out',
        'admin_adjustment',
        'account_deletion',
        'rake'
    ));

CREATE INDEX bankroll_entries_rake_room_idx
    ON bankroll_entries(room_id) WHERE reason = 'rake';

-- 管理员修改抽水规则时，服务端在房间聊天里发一条系统公告。
ALTER TABLE chat_messages
    DROP CONSTRAINT chat_messages_kind_check;

ALTER TABLE chat_messages
    ADD CONSTRAINT chat_messages_kind_check CHECK (kind IN ('text', 'quick_text', 'emoji', 'system'));

DELETE FROM chat_messages WHERE kind = 'system';

ALTER TABLE chat_messages
    DROP CONSTRAINT chat_messages_kind_check;

ALTER TABLE chat_messages
    ADD CONSTRAINT chat_messages_kind_check CHECK (kind IN ('text', 'quick_text', 'emoji'));

-- 回退前必须确认没有 rake 流水：流水表不可删改，有这类记录时约束无法重建。
DROP INDEX IF EXISTS bankroll_entries_rake_room_idx;

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
        'account_deletion'
    ));

ALTER TABLE hands DROP COLUMN rake;

ALTER TABLE rooms
    DROP COLUMN rake_postflop_amount,
    DROP COLUMN rake_postflop_enabled,
    DROP COLUMN rake_cap,
    DROP COLUMN rake_basis_points,
    DROP COLUMN rake_enabled;

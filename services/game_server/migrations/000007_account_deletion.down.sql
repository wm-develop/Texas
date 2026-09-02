-- 回退前必须确认没有 account_deletion 流水，否则约束无法重建。
ALTER TABLE bankroll_entries
    DROP CONSTRAINT bankroll_entries_reason_check;

ALTER TABLE bankroll_entries
    ADD CONSTRAINT bankroll_entries_reason_check CHECK (reason IN (
        'virtual_top_up',
        'buy_in',
        'rebuy',
        'hand_settlement',
        'cash_out',
        'admin_adjustment'
    ));

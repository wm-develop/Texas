-- 账号注销：用户自行注销时，其钱包筹码整体转入管理员钱包，
-- 双方各记一条 account_deletion 流水，reference_id 为被注销账号的 user_id。
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

UPDATE bankroll_entries
SET reason = 'virtual_top_up'
WHERE reason = 'admin_adjustment';

ALTER TABLE bankroll_entries
    DROP CONSTRAINT bankroll_entries_reason_check;

ALTER TABLE bankroll_entries
    ADD CONSTRAINT bankroll_entries_reason_check CHECK (reason IN (
        'virtual_top_up',
        'buy_in',
        'rebuy',
        'hand_settlement',
        'cash_out'
    ));

DROP INDEX IF EXISTS hands_ended_hand_idx;
ALTER TABLE hand_actions DROP COLUMN IF EXISTS timed_out;
ALTER TABLE hands
    DROP COLUMN IF EXISTS big_blind_seat,
    DROP COLUMN IF EXISTS small_blind_seat,
    DROP COLUMN IF EXISTS big_blind,
    DROP COLUMN IF EXISTS small_blind;

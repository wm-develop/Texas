-- 回滚前必须没有观战者（seat_number = 0 的成员），否则约束恢复会失败。
DROP INDEX IF EXISTS room_members_seat_unique;
ALTER TABLE room_members
    DROP CONSTRAINT room_members_seat_number_check,
    ADD CONSTRAINT room_members_seat_number_check CHECK (seat_number BETWEEN 1 AND 10),
    ADD CONSTRAINT room_members_room_id_seat_number_key UNIQUE (room_id, seat_number),
    DROP COLUMN spectating;
ALTER TABLE rooms
    DROP COLUMN spectator_fee_big_blinds,
    DROP COLUMN spectator_voice_allowed,
    DROP COLUMN spectator_chat_allowed,
    DROP COLUMN spectator_emote_allowed;

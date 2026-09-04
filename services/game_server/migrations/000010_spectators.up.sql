-- 观战位：成员可以不占座位地留在房间里，付费后能看到所有人的手牌。
-- 房主可设置看牌费（按大盲倍数，0 为免费）与观战者的语音/文字/表情权限。
ALTER TABLE rooms
    ADD COLUMN spectator_fee_big_blinds integer NOT NULL DEFAULT 10
        CHECK (spectator_fee_big_blinds BETWEEN 0 AND 100),
    ADD COLUMN spectator_voice_allowed boolean NOT NULL DEFAULT true,
    ADD COLUMN spectator_chat_allowed  boolean NOT NULL DEFAULT true,
    ADD COLUMN spectator_emote_allowed boolean NOT NULL DEFAULT true;

-- 观战者以 seat_number = 0 表示不占座位；多名观战者共用 0，因此座位唯一约束
-- 只对真实座位（> 0）生效。
ALTER TABLE room_members
    ADD COLUMN spectating boolean NOT NULL DEFAULT false,
    DROP CONSTRAINT room_members_seat_number_check,
    ADD CONSTRAINT room_members_seat_number_check CHECK (
        (spectating AND seat_number = 0) OR
        (NOT spectating AND seat_number BETWEEN 1 AND 10)
    ),
    DROP CONSTRAINT room_members_room_id_seat_number_key;

CREATE UNIQUE INDEX room_members_seat_unique
    ON room_members (room_id, seat_number)
    WHERE seat_number > 0;

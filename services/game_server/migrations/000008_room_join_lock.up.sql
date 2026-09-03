-- 房主可以临时关闭房间入口，阻止新玩家加入。已在房间内的成员不受影响。
ALTER TABLE rooms
    ADD COLUMN join_locked boolean NOT NULL DEFAULT false;

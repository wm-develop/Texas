-- 进行中牌局的状态快照：进程崩溃、OOM 或宿主机重启后据此恢复那一手。
--
-- 优雅停机能等牌局打完，异常退出不会等；在此之前进行中的一手一律作废。
-- 只在牌局进行中存在一行，手结束即删除：手间的权威状态（筹码、准备、座位）
-- 本来就在 room_members 里。
CREATE TABLE table_states (
    room_id     text PRIMARY KEY REFERENCES rooms(room_id) ON DELETE CASCADE,
    hand_id     text NOT NULL,
    revision    bigint NOT NULL CHECK (revision >= 0),
    state       jsonb NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

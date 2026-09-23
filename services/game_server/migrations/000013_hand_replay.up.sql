-- 牌局回放：把以前要靠推算才能知道的开局信息直接记进牌谱。
--
-- 盲注此前不是动作，回放第一帧必须知道谁下了多少盲注。旧牌谱的盲注额从
-- 房间补上（房间关闭后行仍保留、盲注建房后不可改）；盲注座位旧牌谱留 0，
-- 由回放按引擎规则从庄位推出。
ALTER TABLE hands
    ADD COLUMN small_blind bigint NOT NULL DEFAULT 0 CHECK (small_blind >= 0),
    ADD COLUMN big_blind bigint NOT NULL DEFAULT 0 CHECK (big_blind >= 0),
    ADD COLUMN small_blind_seat smallint NOT NULL DEFAULT 0 CHECK (small_blind_seat BETWEEN 0 AND 10),
    ADD COLUMN big_blind_seat smallint NOT NULL DEFAULT 0 CHECK (big_blind_seat BETWEEN 0 AND 10);

UPDATE hands h
SET small_blind = r.small_blind, big_blind = r.big_blind
FROM rooms r
WHERE r.room_id = h.room_id AND h.big_blind = 0;

-- 超时由服务端代为过牌或弃牌的动作，回放里要和本人主动的动作区分开。
ALTER TABLE hand_actions
    ADD COLUMN timed_out boolean NOT NULL DEFAULT false;

-- 牌局记录按时间翻页
CREATE INDEX hands_ended_hand_idx ON hands(ended_at DESC, hand_id DESC);

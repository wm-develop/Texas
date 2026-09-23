-- AI 复盘：每人同时进行的上限、按人单独设的额度，以及模型服务繁忙时的自动重试。
--
-- 全局「每人同时最多几条在排队或分析中」，0 表示不限。
ALTER TABLE review_settings
    ADD COLUMN max_in_flight_per_user integer NOT NULL DEFAULT 0 CHECK (max_in_flight_per_user >= 0);

-- 按人单独设的额度；NULL 表示跟随全局设置，0 表示不限。
ALTER TABLE review_access
    ADD COLUMN daily_limit integer CHECK (daily_limit >= 0),
    ADD COLUMN max_in_flight integer CHECK (max_in_flight >= 0);

-- 模型服务繁忙、超时时自动重试：已重试次数与下一次可以领取的时间。
ALTER TABLE hand_reviews
    ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ADD COLUMN next_attempt_at timestamptz;
CREATE INDEX hand_reviews_user_status_idx ON hand_reviews(user_id, status);

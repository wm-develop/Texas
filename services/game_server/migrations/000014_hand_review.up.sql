-- AI 复盘：名单开通、全局设置、每手复盘的任务与结果。
--
-- 设置是单行表；全局开关默认打开（有没有人能用由名单决定），两个额度为 0 表示不限。
CREATE TABLE review_settings (
    singleton            boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled              boolean NOT NULL DEFAULT true,
    daily_limit_per_user integer NOT NULL DEFAULT 0 CHECK (daily_limit_per_user >= 0),
    monthly_token_budget bigint NOT NULL DEFAULT 0 CHECK (monthly_token_budget >= 0),
    updated_by           text NOT NULL DEFAULT '',
    updated_at           timestamptz NOT NULL DEFAULT now()
);
INSERT INTO review_settings (singleton) VALUES (true);

-- 被管理员开通了 AI 复盘的账号。
CREATE TABLE review_access (
    user_id    text PRIMARY KEY REFERENCES users(user_id) ON DELETE CASCADE,
    granted_by text NOT NULL,
    granted_at timestamptz NOT NULL
);

-- 同一手、同一人、同一版提示词只分析一次，结果永久缓存；失败的可以重试。
-- requested_at 是最近一次排队的时间（额度按它计数），tokens 是各次尝试的累计。
CREATE TABLE hand_reviews (
    review_id      text PRIMARY KEY,
    hand_id        text NOT NULL REFERENCES hands(hand_id) ON DELETE RESTRICT,
    user_id        text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    prompt_version text NOT NULL,
    status         text NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed')),
    model          text NOT NULL DEFAULT '',
    result         jsonb,
    error          text NOT NULL DEFAULT '',
    input_tokens   bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens  bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    created_at     timestamptz NOT NULL,
    requested_at   timestamptz NOT NULL,
    finished_at    timestamptz,
    UNIQUE (hand_id, user_id, prompt_version)
);
CREATE INDEX hand_reviews_queue_idx ON hand_reviews(status, requested_at);
CREATE INDEX hand_reviews_user_requested_idx ON hand_reviews(user_id, requested_at);
CREATE INDEX hand_reviews_finished_idx ON hand_reviews(finished_at);

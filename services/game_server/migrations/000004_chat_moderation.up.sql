CREATE TABLE chat_mutes (
    user_id             text PRIMARY KEY REFERENCES users(user_id) ON DELETE CASCADE,
    muted_by_user_id    text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    muted_at            timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL
);

CREATE INDEX chat_mutes_actor_updated_idx
ON chat_mutes(muted_by_user_id, updated_at DESC);

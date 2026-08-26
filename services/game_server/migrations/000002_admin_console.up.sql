ALTER TABLE users
    ADD COLUMN role text NOT NULL DEFAULT 'player'
    CHECK (role IN ('player', 'admin'));

CREATE INDEX users_role_status_idx ON users(role, status);

CREATE TABLE server_settings (
    singleton             boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    registration_enabled  boolean NOT NULL DEFAULT true,
    updated_by            text REFERENCES users(user_id) ON DELETE SET NULL,
    updated_at            timestamptz NOT NULL DEFAULT now()
);

INSERT INTO server_settings (singleton, registration_enabled)
VALUES (true, true);

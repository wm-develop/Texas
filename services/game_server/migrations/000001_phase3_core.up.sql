CREATE TABLE users (
    user_id         text PRIMARY KEY,
    username        text NOT NULL,
    display_name    text NOT NULL,
    password_hash   text NOT NULL,
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'suspended', 'deleted')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_normalized_idx ON users (lower(username));

CREATE TABLE refresh_sessions (
    session_id          text PRIMARY KEY,
    user_id             text NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    access_token_hash   text NOT NULL UNIQUE,
    refresh_token_hash  text NOT NULL UNIQUE,
    device_id           text NOT NULL DEFAULT '',
    access_expires_at   timestamptz NOT NULL,
    refresh_expires_at  timestamptz NOT NULL,
    revoked_at          timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CHECK (refresh_expires_at > access_expires_at)
);
CREATE INDEX refresh_sessions_user_id_idx ON refresh_sessions(user_id);

CREATE TABLE account_wallets (
    user_id         text PRIMARY KEY REFERENCES users(user_id) ON DELETE RESTRICT,
    wallet_chips    bigint NOT NULL DEFAULT 0 CHECK (wallet_chips >= 0),
    revision        bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE rooms (
    room_id          text PRIMARY KEY,
    room_code        char(6) NOT NULL UNIQUE,
    owner_user_id    text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    preset           text NOT NULL CHECK (preset IN ('casual', 'standard', 'deep')),
    password_hash    text NOT NULL DEFAULT '',
    max_players      smallint NOT NULL CHECK (max_players BETWEEN 2 AND 10),
    small_blind      bigint NOT NULL CHECK (small_blind >= 10),
    big_blind        bigint NOT NULL CHECK (
                         big_blind >= 20 AND
                         big_blind > small_blind AND
                         big_blind % small_blind = 0
                     ),
    max_buy_in       bigint NOT NULL CHECK (max_buy_in >= big_blind),
    action_seconds   integer NOT NULL CHECK (action_seconds BETWEEN 5 AND 120),
    status           text NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open', 'playing', 'closed')),
    revision         bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at       timestamptz NOT NULL DEFAULT now(),
    closed_at        timestamptz
);

CREATE TABLE room_members (
    room_id          text NOT NULL REFERENCES rooms(room_id) ON DELETE CASCADE,
    user_id          text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    seat_number      smallint NOT NULL CHECK (seat_number BETWEEN 1 AND 10),
    table_chips      bigint NOT NULL CHECK (table_chips >= 0),
    ready            boolean NOT NULL DEFAULT false,
    joined_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id),
    UNIQUE (room_id, seat_number),
    UNIQUE (user_id)
);

CREATE TABLE bankroll_entries (
    entry_id             text PRIMARY KEY,
    request_id           text NOT NULL,
    user_id              text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    room_id              text,
    reference_id         text NOT NULL DEFAULT '',
    reason               text NOT NULL CHECK (reason IN (
                             'virtual_top_up', 'buy_in', 'rebuy',
                             'hand_settlement', 'cash_out'
                         )),
    wallet_delta         bigint NOT NULL,
    table_delta          bigint NOT NULL,
    wallet_balance_after bigint NOT NULL CHECK (wallet_balance_after >= 0),
    table_balance_after  bigint NOT NULL CHECK (table_balance_after >= 0),
    revision_after       bigint NOT NULL CHECK (revision_after >= 0),
    created_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, request_id)
);
CREATE INDEX bankroll_entries_user_created_idx
    ON bankroll_entries(user_id, created_at DESC);
CREATE INDEX bankroll_entries_room_reference_idx
    ON bankroll_entries(room_id, reference_id);

CREATE TABLE bankroll_settlements (
    room_id          text NOT NULL,
    hand_id          text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, hand_id)
);

CREATE TABLE hands (
    hand_id          text PRIMARY KEY,
    room_id          text NOT NULL,
    room_code        char(6) NOT NULL,
    dealer_seat      smallint NOT NULL CHECK (dealer_seat BETWEEN 1 AND 10),
    board_cards      text[] NOT NULL DEFAULT '{}',
	pot_awards       jsonb NOT NULL DEFAULT '[]'::jsonb,
	revealed_hands   jsonb NOT NULL DEFAULT '[]'::jsonb,
    showdown         boolean NOT NULL,
    started_at       timestamptz NOT NULL,
    ended_at         timestamptz NOT NULL,
    CHECK (ended_at >= started_at)
);
CREATE INDEX hands_room_ended_idx ON hands(room_id, ended_at DESC);

CREATE TABLE hand_players (
	hand_id          text NOT NULL REFERENCES hands(hand_id) ON DELETE RESTRICT,
    user_id          text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    display_name     text NOT NULL,
    seat_number      smallint NOT NULL CHECK (seat_number BETWEEN 1 AND 10),
    starting_stack   bigint NOT NULL CHECK (starting_stack >= 0),
    ending_stack     bigint NOT NULL CHECK (ending_stack >= 0),
    chip_delta       bigint NOT NULL,
    hole_cards       text[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (hand_id, user_id),
    UNIQUE (hand_id, seat_number),
    CHECK (ending_stack = starting_stack + chip_delta)
);
CREATE INDEX hand_players_user_hand_idx ON hand_players(user_id, hand_id);

CREATE TABLE hand_actions (
    action_id        text PRIMARY KEY,
    hand_id          text NOT NULL REFERENCES hands(hand_id) ON DELETE RESTRICT,
    user_id          text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    sequence_number  integer NOT NULL CHECK (sequence_number > 0),
    street           text NOT NULL CHECK (street IN ('preflop', 'flop', 'turn', 'river')),
    action_type      text NOT NULL,
    committed        bigint NOT NULL CHECK (committed >= 0),
    raise_to         bigint NOT NULL DEFAULT 0 CHECK (raise_to >= 0),
    created_at       timestamptz NOT NULL,
    UNIQUE (hand_id, sequence_number)
);

CREATE TABLE hand_ledger_entries (
	entry_id         text NOT NULL UNIQUE,
	hand_id          text NOT NULL,
    user_id          text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    chip_delta       bigint NOT NULL,
    balance_after    bigint NOT NULL CHECK (balance_after >= 0),
    PRIMARY KEY (hand_id, user_id)
);

CREATE TABLE chat_messages (
    message_id       text PRIMARY KEY,
    client_message_id text NOT NULL,
    room_id          text NOT NULL,
    user_id          text NOT NULL REFERENCES users(user_id) ON DELETE RESTRICT,
    display_name     text NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('text', 'quick_text', 'emoji')),
    content          text NOT NULL,
    sent_at          timestamptz NOT NULL,
	UNIQUE (room_id, user_id, client_message_id)
);
CREATE INDEX chat_messages_room_sent_idx ON chat_messages(room_id, sent_at DESC);

CREATE TABLE audit_events (
    event_id         text PRIMARY KEY,
    actor_user_id    text REFERENCES users(user_id) ON DELETE SET NULL,
    room_id          text,
    event_type       text NOT NULL,
    request_id       text NOT NULL DEFAULT '',
    metadata         jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_room_created_idx ON audit_events(room_id, created_at DESC);

CREATE FUNCTION reject_immutable_row_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$;

CREATE TRIGGER bankroll_entries_immutable
BEFORE UPDATE OR DELETE ON bankroll_entries
FOR EACH ROW EXECUTE FUNCTION reject_immutable_row_change();

CREATE TRIGGER bankroll_settlements_immutable
BEFORE UPDATE OR DELETE ON bankroll_settlements
FOR EACH ROW EXECUTE FUNCTION reject_immutable_row_change();

CREATE TRIGGER hand_ledger_entries_immutable
BEFORE UPDATE OR DELETE ON hand_ledger_entries
FOR EACH ROW EXECUTE FUNCTION reject_immutable_row_change();

CREATE TRIGGER audit_events_immutable
BEFORE UPDATE OR DELETE ON audit_events
FOR EACH ROW EXECUTE FUNCTION reject_immutable_row_change();

BEGIN;

DROP TRIGGER IF EXISTS audit_events_immutable ON audit_events;
DROP TRIGGER IF EXISTS hand_ledger_entries_immutable ON hand_ledger_entries;
DROP TRIGGER IF EXISTS bankroll_entries_immutable ON bankroll_entries;
DROP FUNCTION IF EXISTS reject_immutable_row_change();

DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS hand_ledger_entries;
DROP TABLE IF EXISTS hand_actions;
DROP TABLE IF EXISTS hand_players;
DROP TABLE IF EXISTS hands;
DROP TABLE IF EXISTS bankroll_entries;
DROP TABLE IF EXISTS room_members;
DROP TABLE IF EXISTS rooms;
DROP TABLE IF EXISTS account_wallets;
DROP TABLE IF EXISTS refresh_sessions;
DROP TABLE IF EXISTS users;

COMMIT;

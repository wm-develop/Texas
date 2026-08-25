# PostgreSQL migrations

This directory contains ordered, reversible SQL migrations for the game server.
`000001_phase3_core.up.sql` defines the phase 3 persistence contract; its
matching `down` file is intentionally destructive and is only for disposable
development databases.

The running server still uses in-memory repositories. Do not point a production
database at the server until the PostgreSQL repositories and transaction-level
integration tests are complete. The first transaction boundary to implement is:

1. lock `account_wallets` and the player's `room_members` row;
2. validate wallet balance and the room's maximum buy-in;
3. update both balances;
4. append one idempotent `bankroll_entries` row;
5. commit all four effects together.

Migration execution tooling and database connection configuration will be added
with the PostgreSQL repository implementation. Never apply the `down` migration
to a database containing data that must be retained.

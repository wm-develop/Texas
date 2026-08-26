# PostgreSQL migrations

This directory contains ordered, reversible SQL migrations for the game server.
`000001_phase3_core.up.sql` defines the phase 3 persistence contract; its
matching `down` file is intentionally destructive and is only for disposable
development databases.

The running game server still uses in-memory repositories. The standalone
migration command can prepare a disposable PostgreSQL database now; do not use
that database as the game server's production store until the repositories and
transaction-level integration tests are complete.

Set `DATABASE_URL` in the repository-root `.env`, then run from
`services/game_server`:

```powershell
go run .\cmd\migrate up
go run .\cmd\migrate down --steps 1
```

Every migration runs in a transaction and is recorded with a SHA-256 checksum.
The runner serializes concurrent migration processes with a PostgreSQL advisory
lock and refuses to continue if an already-applied migration was modified.
Set `TEST_DATABASE_URL` to a disposable database when running `go test ./...`
to execute the real upgrade, repeat-run, and rollback integration test.

The first repository transaction boundary to implement is:

1. lock `account_wallets` and the player's `room_members` row;
2. validate wallet balance and the room's maximum buy-in;
3. update both balances;
4. append one idempotent `bankroll_entries` row;
5. commit all four effects together.

Never apply the `down` migration to a database containing data that must be retained.

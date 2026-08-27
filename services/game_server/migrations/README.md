# PostgreSQL migrations

This directory contains ordered, reversible SQL migrations for the game server.
`000001_phase3_core.up.sql` defines the phase 3 persistence contract.
`000002_admin_console.up.sql` adds account roles and the singleton registration
setting used by the administrator console. `000003_admin_account_management.up.sql`
adds the auditable administrator wallet-adjustment ledger reason. Matching
`down` files are intended only for disposable development databases.

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

Never apply the `down` migration to a database containing data that must be retained.

package migrations

import "embed"

// Files contains every ordered migration used by the standalone migration
// command. Keeping SQL embedded makes the command independent of its working
// directory and prevents deploying a binary without its schema files.
//
//go:embed *.up.sql *.down.sql
var Files embed.FS
